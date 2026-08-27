package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	overviewDynamicsDefaultDays = 14
	overviewDynamicsMaxDays     = 90
)

// overviewCustomerKind is the only account kind a headline user number or a
// funnel derived from it may count. The verdict itself lives in the
// user_accounts view (migration 075), which folds in the Keycloak
// service-account rule these queries used to carry themselves along with the
// seeds, the @keycloak.local shells, our own probes and the staff accounts —
// together 10 of the 30 rows in users, i.e. a 55% overstatement of demand.
const overviewCustomerKind = "customer"

// brokenAppSnapshotPredicate is the single source of truth for "an app the
// platform can PROVE is currently broken": a live k8s workload the status
// reconciler observes not-ready and re-stamps within the freshness window.
// Shared by the headline broken COUNT (overviewProjects.Apps.Broken) and the
// not-ready LIST (overviewNotReadyApps) so the two can never disagree again --
// they did: the headline derived broken as total-minus-Ready (28) while the
// honest list applied this predicate (2). Assumes table alias rs.
const brokenAppSnapshotPredicate = `rs.kind = 'App'
	AND rs.summary_json->>'live_source' = 'k8s'
	AND rs.phase NOT IN ('Ready', 'Stopped', 'Orphaned')
	AND rs.last_synced_at > now() - interval '10 minutes'`

// noSignalAppSnapshotPredicate is the blind spot brokenAppSnapshotPredicate
// cannot describe from the inside. That predicate can only ever indict an app
// the reconciler observes live (live_source = 'k8s'); an App row without a live
// workload is therefore counted as neither ready nor broken, and the headline
// broken number stays 0 no matter what state those rows are really in. Nine
// such rows sat behind by_phase.Unknown while apps.broken read 0.
//
// "No health signal" is its own answer and belongs on the panel next to the
// other two, for the same reason overviewNotReadyFreshness exists: an empty
// broken list must mean "nothing is broken", never "we cannot see". The rows
// that land here are the ones nobody can call healthy either -- most often an
// app whose snapshot froze at git-watcher create time because its first build
// never produced a workload, which is exactly the terminal state of a customer
// who registered, created an app and never reached a live URL.
//
// Ready/Stopped/Orphaned are excluded because each is a settled answer, not an
// absence of one. The grace window keys on first_seen_at (set once at insert,
// migration 049) and NOT on last_synced_at: a just-created app legitimately has
// no workload for the minutes its first build runs, while last_synced_at is
// re-stamped every reconcile tick and would keep old rows looking new forever
// (see project memory grace_filter_on_last_synced_at_excludes_live_apps).
// Assumes table alias rs.
const noSignalAppSnapshotPredicate = `rs.kind = 'App'
	AND rs.summary_json->>'live_source' IS DISTINCT FROM 'k8s'
	AND rs.phase NOT IN ('Ready', 'Stopped', 'Orphaned')
	AND rs.first_seen_at < now() - interval '1 hour'`

// appSnapshotFreshnessWindow mirrors the 10-minute freshness cutoff baked into
// brokenAppSnapshotPredicate. It is pulled out as its own constant because
// overviewNotReadyFreshness needs to reason about the SAME window from the
// other side: not "how many broken apps are fresh" but "how many app
// snapshots have gone stale altogether", which is what tells the operator
// whether the not-ready list can be trusted at all.
const appSnapshotFreshnessWindow = "10 minutes"

// liveURLProbeFreshnessWindow is how long an http_checked_at stamp on a
// resource_snapshots row counts as current for the last-mile panel. gitops-agent
// writes summary_json.http_status/http_reason/http_checked_at on its own probe
// cadence (separate from the 10-minute reconcile freshness window above); a
// stamp older than this is treated the same as no stamp at all -- stale, not ok
// -- so a wedged prober reads as blindness rather than as a wave of healthy apps.
const liveURLProbeFreshnessWindow = 30 * time.Minute

// liveURLNeverHTTPMinAge is the age resource_snapshots.first_seen_at (the
// resource's own immutable birth stamp, migration 049) must clear before a
// row that has never once answered HTTP is allowed to leave Dead for
// NeverHTTP. It exists to keep two very different failures from collapsing
// into one bucket: an app with no app_url_http_seen row is EITHER a bot that
// was never a web app (fanvk, sevarateambot -- both weeks old, long-lived,
// never intended to answer HTTP) OR a real web app that has been broken
// since the moment it was created and has therefore never once answered
// either. The panel is the owner's only inventory of what is broken, so the
// second case must stay red -- a week-old app still showing dead-from-birth
// is exactly the outage this panel exists to surface, not noise to file
// away next to the long-lived bots. Seven days is chosen because every
// worker-shaped app measured on prod 2026-08-15 had been alive for weeks,
// while a real deploy-time failure is expected to be caught and fixed on the
// order of hours to days -- a week of silence with zero HTTP responses is
// long past the point where "still building" is a plausible excuse.
const liveURLNeverHTTPMinAge = 7 * 24 * time.Hour

// internalOwnerEmailPrefix and internalOwnerEmailSuffix are the two shapes of
// an in-house owner email: the operator's own address (any alexkekiy@... form)
// and any @dada-tuda.ru staff mailbox. Everyone else is an external customer.
// Kept here rather than reusing a shared helper because no such helper exists
// yet elsewhere in the codebase (checked: no "external owner" predicate is
// defined anywhere else in internal/api).
const (
	internalOwnerEmailPrefix = "alexkekiy"
	internalOwnerEmailSuffix = "@dada-tuda.ru"
)

// isInternalOwnerEmail reports whether email belongs to platform staff rather
// than a customer, by the same two rules used throughout project docs/config
// for "is this our own account": the operator's personal address or any
// @dada-tuda.ru mailbox. An empty email (owner row missing/unjoined) is never
// treated as internal -- an unknown owner must not be hidden from the
// external-first ordering the last-mile panel relies on.
func isInternalOwnerEmail(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return false
	}
	return strings.HasPrefix(e, internalOwnerEmailPrefix) || strings.HasSuffix(e, internalOwnerEmailSuffix)
}

// stuckOperationThreshold is how long an operation may sit in a non-terminal
// status before the admin overview calls it stuck. It reuses boxOperationLease
// (box_operations_worker.go) rather than inventing its own number: that lease
// is the shortest reclaim window any operation processor in this codebase
// runs on (12 minutes = 2x boxOperationTimeout), so an operation older than it
// has outlived every retry budget we actually have, whichever agent owns it.
const stuckOperationThreshold = boxOperationLease

// terminalOperationStatuses is the status set overviewStuckOperations excludes,
// derived from classifyOperationStatus (deploy_hooks.go) rather than written out
// again as SQL literals. It exists because this panel hand-wrote its own list
// and left "Committed" out of it: gitops-agent ends an operation at Committed
// (db.MarkCommitted) and nothing ever advances that row, so the panel reported
// every finished deploy as a stuck operation -- 466 of them, all false, drowning
// the handful of real breakages an operator opens this page to find. Three
// definitions of "terminal" disagreed in one codebase; this makes the panel read
// the same function the deploy-hook poller does, and TestStuckOperationsExcludes
// AllTerminalStatuses pins them together so a new terminal status cannot be
// added to the model without this query following it.
var allOperationStatusesForStuckCheck = []models.OperationStatus{
	models.OperationStatusCreated,
	models.OperationStatusValidated,
	models.OperationStatusQueued,
	models.OperationStatusRendering,
	models.OperationStatusCommittingToGit,
	models.OperationStatusCommitted,
	models.OperationStatusWaitingForArgoSync,
	models.OperationStatusSyncing,
	models.OperationStatusReconciling,
	models.OperationStatusReady,
	models.OperationStatusFailed,
	models.OperationStatusCancelled,
	models.OperationStatusWaitingForApproval,
}

var terminalOperationStatuses = func() []string {
	out := []string{}
	for _, s := range allOperationStatusesForStuckCheck {
		if terminal, _ := classifyOperationStatus(s); terminal {
			out = append(out, string(s))
		}
	}
	return out
}()

type overviewUsers struct {
	Total     int `json:"total"`
	New24h    int `json:"new_24h"`
	New7d     int `json:"new_7d"`
	New30d    int `json:"new_30d"`
	Active48h int `json:"active_48h"`
}

type overviewApps struct {
	Total    int            `json:"total"`
	Ready    int            `json:"ready"`
	Broken   int            `json:"broken"`
	NoSignal int            `json:"no_signal"`
	ByPhase  map[string]int `json:"by_phase"`
}

type overviewProjects struct {
	Total     int          `json:"total"`
	Apps      overviewApps `json:"apps"`
	Databases int          `json:"databases"`
	Caches    int          `json:"caches"`
}

type overviewBuilds struct {
	Last7dSuccess  int `json:"last_7d_success"`
	Last7dFailed   int `json:"last_7d_failed"`
	Last7dCanceled int `json:"last_7d_canceled"`
	Last24h        int `json:"last_24h"`
}

// overviewDomains counts domain_hostnames by status for the god-admin
// overview card. Failed and Retired both start from status='failed', split
// by the same hostnameReasonAppDeleted marker overviewDomainIssues already
// filters on: demoteAppHostnames (DeleteApp) stamps that reason on every
// hostname of a deleted app, so those rows are terminal-by-design tombstones,
// not live problems. Before this split Failed counted both together and
// reported the tombstones as breakage on the same response whose issues list
// a few lines down had already excluded them -- the two numbers on one
// screen disagreed about what "failed" means. Retired keeps the tombstones
// visible instead of dropping them, so Active+Pending+Failed+Retired always
// equals count(*) from domain_hostnames.
type overviewDomains struct {
	Active  int `json:"active"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
	Retired int `json:"retired"`
}

// overviewMoney is the money card of the god-admin overview.
//
// RevenueTotal and MarginTotal are MODELLED: what the consumption formula would
// charge for observed usage, computed identically for a free account and a
// paying one. PaidTotal is the only settled figure (succeeded payments in the
// window), MeteredTotal the only ledger-backed one (app_usage hours), and
// UncollectedTotal their difference. The card used to show the modelled pair
// alone, which reads as income the platform has never received; the settled
// three carry no omitempty precisely because zero is the number worth showing.
type overviewMoney struct {
	Available        bool                 `json:"available"`
	Note             string               `json:"note,omitempty"`
	Currency         string               `json:"currency,omitempty"`
	Days             int                  `json:"days,omitempty"`
	HardwareTotal    float64              `json:"hardware_total,omitempty"`
	RevenueTotal     float64              `json:"revenue_total,omitempty"`
	MarginTotal      float64              `json:"margin_total,omitempty"`
	TopLossMakers    []adminCostLossMaker `json:"top_loss_makers,omitempty"`
	PaidTotal        float64              `json:"paid_total"`
	MeteredTotal     float64              `json:"metered_total"`
	UncollectedTotal float64              `json:"uncollected_total"`
}

// overviewLiveURLs is the "last mile" the builds card cannot see: builds.
// last_7d_success only proves an image built and pushed, never that the app's
// public URL actually answers. Checked+Stale together equal the denominator --
// every App row with an active hostname -- so a caller can tell "we probed
// everything and it's fine" from "we do not know" at a glance. Checked splits
// into OK, AppResponded and Dead (see isDeadProbeStatus for the boundary);
// Stale is its own bucket rather than folded into Dead, because an empty
// DeadApps list next to a large Stale must read as "the prober went blind",
// never as "everything is healthy" (see project memory
// admin_broken_panel_read_health_from_own_blindness -- the identical mistake,
// here for URL probing instead of the k8s reconciler).
//
// Dead and Checked are aggregate counts that mix two different owner classes:
// external customer apps (the product signal an operator watches for) and our
// own internal apps (the operator's own account or an @dada-tuda.ru staff
// mailbox, see isInternalOwnerEmail). DeadExternal/DeadInternal and
// CheckedExternal/CheckedInternal split those same rows by that same
// predicate so an operator can tell "our product is breaking for customers"
// from "we broke our own tooling again" without reading DeadApps line by
// line. Dead == DeadExternal + DeadInternal and Checked == CheckedExternal +
// CheckedInternal always hold; the aggregate fields are kept for backward
// compatibility with existing callers.
//
// NeverHTTP is a row that isDeadProbeResult would otherwise sentence to Dead,
// but which app_url_http_seen (see hasServedHTTP, app_url_watcher.go) has
// never once seen answer HTTP AND whose resource_snapshots.first_seen_at is
// older than liveURLNeverHTTPMinAge (7 days). summary_json.worker=true is a
// declaration the owner or the framework default made at create time;
// app_url_http_seen is an observation gitops-agent writes on every passing
// probe. They are kept apart on purpose: fanvk and sevarateambot (long-lived
// Telegram bots with no listening socket, measured 2026-08-15) were created
// without the worker flag, so folding NeverHTTP into Workers would hide that
// the flag itself is wrong for them. The age gate exists because "never
// answered HTTP" alone cannot tell a bot that was never a web app apart from
// a real web app that has been broken since the moment it was born and has
// therefore also never answered -- collapsing both into NeverHTTP would hide
// a genuine day-one outage from the one inventory the owner has. A row only
// ever lands in NeverHTTP, never in Dead or DeadApps -- but only when
// app_url_http_seen is actually reachable for it and the row has cleared the
// age gate; see overviewLiveURLs for the degrade-to-Dead rule when the
// lookup is not reachable, and for the young-row case, which stays Dead. An
// app that DID once answer and has since gone dark keeps landing in Dead and
// DeadApps unconditionally, regardless of age, because that is a real
// outage, not an app that was never a web app.
type overviewLiveURLs struct {
	Checked         int               `json:"checked"`
	OK              int               `json:"ok"`
	Dead            int               `json:"dead"`
	AppResponded    int               `json:"app_responded"`
	Workers         int               `json:"workers"`
	NeverHTTP       int               `json:"never_http"`
	Stale           int               `json:"stale"`
	DeadApps        []overviewDeadApp `json:"dead_apps"`
	DeadExternal    int               `json:"dead_external"`
	DeadInternal    int               `json:"dead_internal"`
	CheckedExternal int               `json:"checked_external"`
	CheckedInternal int               `json:"checked_internal"`
}

// deadProbeStatuses is the exact set of http_status values that mean "the
// last-mile probe reached a proxy with no backend behind it", not "the app
// answered with an error". 502/503/504 are the codes an ingress controller
// itself generates when it cannot reach any pod for the route (Bad Gateway,
// Service Unavailable, Gateway Timeout); a bare 0 is the probe never getting
// a response at all (dial error, timeout, TLS failure). Every other status,
// including 404/401/403/500, is the target application itself answering --
// proof the last mile is NOT dead, whatever the app chose to say. This
// distinction exists because two real apps (telemost-bot, reels-tracker)
// were counted as dead for answering 404 on `/` while their `/health` route
// answered 200 the whole time, and a third (n8n behind a stale hash-domain
// ingress pointing at a deleted Service) answered 503 with no backend at
// all -- collapsing both into one "dead" bucket made an operator unable to
// tell a headless API from a genuinely broken route.
var deadProbeStatuses = map[int]bool{
	0:   true,
	502: true,
	503: true,
	504: true,
}

// isDeadProbeStatus reports whether status belongs to deadProbeStatuses --
// the status-only half of the boundary, kept separate because http_status
// alone cannot name the emitter (see isDeadProbeResult).
func isDeadProbeStatus(status int) bool {
	return deadProbeStatuses[status]
}

// appAuthoredReasonPrefix is the http_reason prefix gitops-agent writes when
// a 502/503/504 arrived with a body the application itself produced rather
// than ingress-nginx's default error page (classifyLivenessResponse,
// gitops-agent/internal/worker/livenessprobe.go).
const appAuthoredReasonPrefix = "app_status_"

// isDeadProbeResult is the full Dead predicate: a 5xx is only evidence of a
// backend-less route when nothing behind the route authored it. Measured on
// production 2026-08-15, fonbet-value answered 503 from its own container
// with a JSON body listing its readiness blockers while ingress-nginx served
// a byte-identical status class for n8n, whose Service no longer exists --
// the code alone cannot separate them, so the probe now records who wrote
// the body and this predicate reads it. A bare 0 stays dead unconditionally:
// no response at all carries no authorship.
func isDeadProbeResult(status int, reason string) bool {
	if !isDeadProbeStatus(status) {
		return false
	}
	if status == 0 {
		return true
	}
	return !strings.HasPrefix(reason, appAuthoredReasonPrefix)
}

// overviewDeadApp names one app behind live_urls.dead so an operator can act
// on it instead of staring at a count. CheckedAt is a pointer because a dead
// app is by definition one gitops-agent DID manage to probe (a stale/never-
// probed app lands in live_urls.stale instead, never here) -- but the pointer
// keeps the zero-value JSON contract honest if that ever changes.
type overviewDeadApp struct {
	Name        string     `json:"name"`
	ProjectName string     `json:"project_name"`
	OwnerEmail  string     `json:"owner_email"`
	Hostname    string     `json:"hostname"`
	HTTPStatus  int        `json:"http_status"`
	HTTPReason  string     `json:"http_reason"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	External    bool       `json:"external"`
}

type overviewDayPoint struct {
	Date         string `json:"date"`
	Signups      int    `json:"signups"`
	BuildSuccess int    `json:"build_success"`
	BuildFailed  int    `json:"build_failed"`
	NewApps      int    `json:"new_apps"`
}

// GetAdminOverview returns a single aggregate snapshot of platform state for the
// god-admin dashboard: user growth, project/app/database counts, build health,
// domain health, a best-effort cost breakdown, and a per-day dynamics series.
// Platform-admin only (/platform-admins group, same gate as /admin/audit).
//
// Money is best-effort: an OpenCost outage never fails the request (see
// cost.go's fail-open cache-aside pattern and the cost-warmer boot-block
// postmortem) — money.available is false and the rest of the payload is still
// returned.
//
// @ID          getAdminOverview
// @Summary     Platform state overview (platform-admin only)
// @Description Returns a single aggregate snapshot of platform state: user growth, project/app/database counts, build health over the last 7 days, domain health, a best-effort cost breakdown by project (OpenCost), and a per-day dynamics series. Platform-admin only (/platform-admins group); every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       days query    int false "Length of the dynamics series in days (default 14, max 90)"
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/overview [get]
func (h *Handler) GetAdminOverview(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isAdminReader(claims) {
		respondForbidden(c)
		return
	}

	days := overviewDynamicsDefaultDays
	if v, err := strconv.Atoi(c.Query("days")); err == nil && v > 0 {
		days = v
	}
	if days > overviewDynamicsMaxDays {
		days = overviewDynamicsMaxDays
	}

	out, err := h.BuildAdminOverview(c.Request.Context(), days)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, out)
}

// BuildAdminOverview collects the same snapshot GetAdminOverview has always
// returned, minus the HTTP transport concerns (auth, days clamping,
// c.JSON). Pulled out so pulse_export.go can capture the identical payload
// on a timer without going through gin at all -- see the "panel blindness
// read as health" postmortem this export exists to route around.
func (h *Handler) BuildAdminOverview(ctx context.Context, days int) (map[string]any, error) {
	users, err := h.overviewUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate users")
	}
	projects, err := h.overviewProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate projects")
	}
	builds, err := h.overviewBuilds(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate builds")
	}
	domains, err := h.overviewDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate domains")
	}
	notReady, err := h.overviewNotReadyApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list not-ready apps")
	}
	noSignal, err := h.overviewNoSignalApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps without a health signal")
	}
	notReadyFreshness, err := h.overviewNotReadyFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check not-ready freshness")
	}
	notReadyOther, err := h.overviewNotReadyOtherResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list not-ready resources")
	}
	domainIssues, err := h.overviewDomainIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list domain issues")
	}
	stuckOps, err := h.overviewStuckOperations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list stuck operations")
	}
	failedBuilds, err := h.overviewFailedLatestBuilds(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list failed latest builds")
	}
	dynamics, err := h.overviewDynamics(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate dynamics")
	}
	liveURLs, err := h.overviewLiveURLs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate live URL health")
	}
	platformHealthOut := h.overviewPlatformHealth(ctx, h.platformHealthClientset(), h.cfg.PlatformHealthNamespaces)
	return map[string]any{
		"users":               users,
		"projects":            projects,
		"builds":              builds,
		"domains":             domains,
		"money":               h.overviewMoney(ctx),
		"not_ready":           notReady,
		"no_signal":           noSignal,
		"not_ready_freshness": notReadyFreshness,
		"not_ready_other":     notReadyOther,
		"domain_issues":       domainIssues,
		"stuck_operations":    stuckOps,
		"failed_builds":       failedBuilds,
		"dynamics":            dynamics,
		"dynamics_days":       days,
		"live_urls":           liveURLs,
		"platform_health":     platformHealthOut,
	}, nil
}

// overviewUsers counts customer accounts and how many of them acted in the last
// 48 hours. SignUp is excluded from the activity query because provisioning
// writes that row in the same statement that creates the user
// (backend/internal/auth/provision.go): counting it would report every fresh
// registration as an active customer without a single product action.
func (h *Handler) overviewUsers(ctx context.Context) (overviewUsers, error) {
	var out overviewUsers
	err := h.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE created_at >= now() - interval '24 hours'),
			count(*) FILTER (WHERE created_at >= now() - interval '7 days'),
			count(*) FILTER (WHERE created_at >= now() - interval '30 days')
		FROM user_accounts
		WHERE account_kind = $1`,
		overviewCustomerKind,
	).Scan(&out.Total, &out.New24h, &out.New7d, &out.New30d)
	if err != nil {
		return out, err
	}

	err = h.pool.QueryRow(ctx, `
		SELECT count(DISTINCT a.actor_id)
		FROM audit_events a
		JOIN user_accounts u ON u.id = a.actor_id
		WHERE a.created_at >= now() - interval '48 hours'
		  AND a.action <> 'SignUp'
		  AND u.account_kind = $1`,
		overviewCustomerKind,
	).Scan(&out.Active48h)
	return out, err
}

func (h *Handler) overviewProjects(ctx context.Context) (overviewProjects, error) {
	var out overviewProjects
	out.Apps.ByPhase = map[string]int{}

	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&out.Total); err != nil {
		return out, err
	}

	rows, err := h.pool.Query(ctx, `
		SELECT phase, count(*)
		FROM resource_snapshots
		WHERE kind = 'App'
		GROUP BY phase`,
	)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var phase string
		var n int
		if scanErr := rows.Scan(&phase, &n); scanErr != nil {
			rows.Close()
			return out, scanErr
		}
		if phase == "" {
			phase = "Unknown"
		}
		out.Apps.ByPhase[phase] = n
		out.Apps.Total += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM resource_snapshots
		WHERE kind IN ('ServiceDatabase', 'ServiceDatabaseV2')`,
	).Scan(&out.Databases); err != nil {
		return out, err
	}

	if err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM resource_snapshots
		WHERE kind = 'ServiceCacheV2'`,
	).Scan(&out.Caches); err != nil {
		return out, err
	}

	out.Apps.Ready = out.Apps.ByPhase["Ready"]

	if err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM resource_snapshots rs
		WHERE `+brokenAppSnapshotPredicate).Scan(&out.Apps.Broken); err != nil {
		return out, err
	}

	if err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM resource_snapshots rs
		WHERE `+noSignalAppSnapshotPredicate).Scan(&out.Apps.NoSignal); err != nil {
		return out, err
	}

	return out, nil
}

func (h *Handler) overviewBuilds(ctx context.Context) (overviewBuilds, error) {
	var out overviewBuilds
	err := h.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'success'  AND created_at >= now() - interval '7 days'),
			count(*) FILTER (WHERE status = 'failed'   AND created_at >= now() - interval '7 days'),
			count(*) FILTER (WHERE status = 'canceled' AND created_at >= now() - interval '7 days'),
			count(*) FILTER (WHERE created_at >= now() - interval '24 hours')
		FROM builds`,
	).Scan(&out.Last7dSuccess, &out.Last7dFailed, &out.Last7dCanceled, &out.Last24h)
	return out, err
}

func (h *Handler) overviewDomains(ctx context.Context) (overviewDomains, error) {
	var out overviewDomains
	err := h.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'active'),
			count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status = 'failed' AND (status_reason IS NULL OR status_reason <> $1)),
			count(*) FILTER (WHERE status = 'failed' AND status_reason = $1)
		FROM domain_hostnames`,
		hostnameReasonAppDeleted,
	).Scan(&out.Active, &out.Pending, &out.Failed, &out.Retired)
	return out, err
}

type overviewNotReadyApp struct {
	Name        string `json:"name"`
	ProjectName string `json:"project_name"`
	Phase       string `json:"phase"`
	Reason      string `json:"reason,omitempty"`
	OwnerEmail  string `json:"owner_email"`
}

// overviewNotReadyApps lists apps the platform can currently PROVE are broken:
// a real workload the gitops-agent status reconciler observes live and reports
// as not-ready. The panel deliberately excludes three classes that a naive
// "phase != Ready" filter swept in and made the board cry wolf:
//
//   - Deliberately-off apps. A k8s snapshot with desired replicas 0 is written
//     phase=Stopped, not broken. Excluded.
//   - Apps with no live workload at all (live_source != 'k8s'): orphan-GC-cleared
//     platform infra that renders no Deployment, and never-deployed shells whose
//     snapshot froze at git-watcher create-time (Unknown/Pending). No workload
//     means nothing to be "not ready" — the platform has no health signal to
//     assert breakage from.
//   - Stale ghost snapshots left behind by a move/rename. The app's healthy twin
//     lives under the new environment and gets its last_synced_at bumped every
//     reconcile tick (30s); the abandoned old-env row keeps a k8s live_source but
//     freezes at Pending because no workload matches it there anymore. A live
//     signal older than the freshness window is not current truth, so it drops.
//
// A genuinely crashlooping app keeps a matching Deployment, so the reconciler
// re-stamps its last_synced_at every 30s — freshness can never hide a real
// outage, only surface a stale ghost. Capped so a platform-wide incident cannot
// balloon the payload.
//
// Phase alone cannot tell an operator WHOSE problem this is: the gitops-agent
// status reconciler (livePhase, gitops-agent/internal/worker/statusreconciler.go)
// folds OOMKilled/CrashLoopBackOff/ImagePullBackOff/ErrImagePull into the
// single phase string "CrashLoop" so the console's phase badge has one
// red state to render. That collapse is fine for a badge but was silently
// read by the operator as "this app's own code is broken" -- an
// ImagePullBackOff means our registry never delivered the image, the
// opposite conclusion. Reason surfaces the specific kube waiting reason the
// reconciler also stamps into summary_json (same patch, never collapsed) so
// this list can tell the two apart without changing what phase itself means
// anywhere else it is read.
func (h *Handler) overviewNotReadyApps(ctx context.Context) ([]overviewNotReadyApp, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT rs.name, p.display_name, rs.phase, COALESCE(rs.summary_json->>'reason', ''), COALESCE(u.email, '')
		FROM resource_snapshots rs
		JOIN projects p     ON p.id = rs.project_id
		LEFT JOIN users u   ON u.id = p.owner_id
		WHERE `+brokenAppSnapshotPredicate+`
		ORDER BY rs.last_synced_at DESC
		LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []overviewNotReadyApp{}
	for rows.Next() {
		var a overviewNotReadyApp
		if err := rows.Scan(&a.Name, &a.ProjectName, &a.Phase, &a.Reason, &a.OwnerEmail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type overviewNoSignalApp struct {
	Name        string `json:"name"`
	ProjectName string `json:"project_name"`
	Phase       string `json:"phase"`
	OwnerEmail  string `json:"owner_email"`
	AgeSeconds  int    `json:"age_seconds"`
}

// overviewNoSignalApps names the rows counted by apps.no_signal so the operator
// can act on them instead of reading a bare number. Oldest first: age is the
// whole point, since a row that has been signal-less for days is an app that
// never made it to a live URL, while a young one is usually mid-first-build.
// Capped like the not-ready list so a mass event cannot balloon the payload.
func (h *Handler) overviewNoSignalApps(ctx context.Context) ([]overviewNoSignalApp, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT rs.name, p.display_name, rs.phase, COALESCE(u.email, ''),
		       extract(epoch FROM now() - rs.first_seen_at)::int
		FROM resource_snapshots rs
		JOIN projects p     ON p.id = rs.project_id
		LEFT JOIN users u   ON u.id = p.owner_id
		WHERE `+noSignalAppSnapshotPredicate+`
		ORDER BY rs.first_seen_at ASC
		LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []overviewNoSignalApp{}
	for rows.Next() {
		var a overviewNoSignalApp
		if err := rows.Scan(&a.Name, &a.ProjectName, &a.Phase, &a.OwnerEmail, &a.AgeSeconds); err != nil {
			return nil, err
		}
		if a.Phase == "" {
			a.Phase = "Unknown"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// overviewNotReadyFreshness answers the question overviewNotReadyApps cannot
// ask about itself: is the not-ready LIST actually watching anything right
// now? brokenAppSnapshotPredicate requires last_synced_at inside the last 10
// minutes, so a dead or wedged gitops-agent status reconciler makes every App
// snapshot age out of that window at once -- the not-ready list quietly goes
// to zero and the panel reads as "all green" while it has simply gone blind.
// StaleApps counts App/k8s snapshots that fell out of the freshness window;
// NewestSyncAgeSeconds is how long ago the MOST recently reconciled App
// snapshot was written, platform-wide -- the one number that tells the
// operator whether the reconciler is alive at all. Blind is true once that
// number itself exceeds the freshness window, meaning nothing has been
// reconciled recently enough for the not-ready list to be trusted.
type overviewNotReadyFreshness struct {
	StaleApps            int  `json:"stale_apps"`
	NewestSyncAgeSeconds *int `json:"newest_sync_age_seconds"`
	Blind                bool `json:"blind"`
}

func (h *Handler) overviewNotReadyFreshness(ctx context.Context) (overviewNotReadyFreshness, error) {
	var out overviewNotReadyFreshness
	var newestAgeSeconds *float64
	err := h.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE rs.last_synced_at <= now() - interval '`+appSnapshotFreshnessWindow+`'),
			extract(epoch FROM now() - max(rs.last_synced_at))
		FROM resource_snapshots rs
		WHERE rs.kind = 'App' AND rs.summary_json->>'live_source' = 'k8s'`,
	).Scan(&out.StaleApps, &newestAgeSeconds)
	if err != nil {
		return out, err
	}
	if newestAgeSeconds != nil {
		seconds := int(*newestAgeSeconds)
		out.NewestSyncAgeSeconds = &seconds
		out.Blind = time.Duration(seconds)*time.Second > 10*time.Minute
	} else {
		out.Blind = true
	}
	return out, nil
}

type overviewNotReadyResource struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	ProjectName    string `json:"project_name"`
	Phase          string `json:"phase"`
	AgeSeconds     int    `json:"age_seconds"`
	KindLagSeconds int    `json:"kind_lag_seconds"`
	Unmaintained   bool   `json:"unmaintained"`
}

// otherSnapshotAbandonLagSeconds is how far a non-App snapshot may lag behind
// the newest sync of its OWN kind before we stop calling it broken. The status
// reconciler stamps every row it still maintains within a single tick, so rows
// of one kind share a last_synced_at down to the second; a row left minutes
// behind its own kind is one the reconciler has provably stopped visiting.
// Fifteen minutes is thirty reconcile ticks -- far past any scheduling jitter,
// far short of the days-long freezes this exists to catch.
const otherSnapshotAbandonLagSeconds = 900

// overviewNotReadyOtherResources is brokenAppSnapshotPredicate's sibling for
// everything that predicate's `kind = 'App'` clause throws away: managed
// databases (ServiceDatabaseV2, live_source=crossplane), KServe AI models, and
// raw CRD-backed resources. These never showed up on the "what's broken" panel
// at all, so a stuck database restore or a wedged model deploy was invisible
// to anyone reading this dashboard. orphan-gc/-cleared are excluded because
// those live_sources mark resources orphan-GC has already swept, not something
// currently broken.
//
// Staleness is judged against the row's own kind rather than an absolute
// window. A non-App snapshot only changes when the status reconciler visits
// the matching live CR, and the reconciler visits every CR of a kind in one
// pass -- so the newest last_synced_at for a kind is that writer's proof of
// life, and a row far behind it has been abandoned by a writer that is
// demonstrably still running. Three ways a row gets abandoned, all observed in
// production: its CR was deleted (nothing left to iterate onto), its name is
// claimed by several environments so the reconciler refuses to guess, or it
// belongs to a kind whose writer was retired. None of them mean the resource
// is broken, yet all three used to print here as breakage -- three PublicApi
// rows sat in this list for 7 to 20 days while their pods served traffic.
//
// Unmaintained rows are still returned, never filtered: hiding them would
// trade a false alarm for the blindness this panel already learned to fear.
// They carry the lag that proves the abandonment so the operator can act on
// the right thing -- the writer, not the resource. When a whole kind stops
// being written, every row's lag stays near zero and the rows keep reading as
// broken, which is the correct alarm for a dead reconciler.
func (h *Handler) overviewNotReadyOtherResources(ctx context.Context) ([]overviewNotReadyResource, error) {
	rows, err := h.pool.Query(ctx, `
		WITH kind_freshness AS (
			SELECT rs.kind,
			       rs.summary_json->>'live_source' AS live_source,
			       max(rs.last_synced_at) AS newest
			FROM resource_snapshots rs
			WHERE rs.kind <> 'App'
			  AND rs.summary_json->>'live_source' IN ('crossplane', 'crd', 'kserve')
			GROUP BY 1, 2
		)
		SELECT rs.kind, rs.name, p.display_name, rs.phase,
		       extract(epoch FROM now() - rs.last_synced_at)::int,
		       GREATEST(extract(epoch FROM kf.newest - rs.last_synced_at)::int, 0)
		FROM resource_snapshots rs
		JOIN projects p ON p.id = rs.project_id
		JOIN kind_freshness kf
		  ON kf.kind = rs.kind
		 AND kf.live_source = rs.summary_json->>'live_source'
		WHERE rs.kind <> 'App'
		  AND rs.summary_json->>'live_source' IN ('crossplane', 'crd', 'kserve')
		  AND rs.phase NOT IN ('Ready', 'Stopped', 'Orphaned')
		ORDER BY rs.last_synced_at ASC
		LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []overviewNotReadyResource{}
	for rows.Next() {
		var r overviewNotReadyResource
		if err := rows.Scan(&r.Kind, &r.Name, &r.ProjectName, &r.Phase, &r.AgeSeconds, &r.KindLagSeconds); err != nil {
			return nil, err
		}
		r.Unmaintained = r.KindLagSeconds > otherSnapshotAbandonLagSeconds
		out = append(out, r)
	}
	return out, rows.Err()
}

type overviewDomainIssue struct {
	Stage       string `json:"stage"`
	Hostname    string `json:"hostname"`
	Status      string `json:"status"`
	CertStatus  string `json:"cert_status,omitempty"`
	ProjectName string `json:"project_name"`
	AgeSeconds  int    `json:"age_seconds"`
}

// overviewDomainIssues surfaces the failed and stuck rows behind
// overviewDomains' active/pending/failed counts, which the console panel
// summed up but never rendered as a list an operator could act on. Two
// stages, both cheap indexed lookups capped at 50 rows each:
//
//   - "hostname": domain_hostnames rows that are status=failed, or still
//     pending after more than a day (the DNS record the owner was told to add
//     never showed up, or cert issuance never completed). Rows whose
//     status_reason is hostnameReasonAppDeleted are excluded: demoteAppHostnames
//     (DeleteApp) and ReapOrphanedAppHostnames (background pass) both stamp
//     that reason deliberately, on an app the operator chose to remove, not
//     something that broke. Counting it here made this panel report a fake
//     poison-pill for every app deletion (see project memory
//     project_admin_broken_panel_read_health_from_own_blindness.md's sibling
//     bug: a terminal-by-design row is not the same thing as a stuck one).
//     Pending rows stamped hostnameReasonAwaitingFirstDeploy are excluded from
//     the same "pending past a day" branch for the identical reason: a
//     managed default hostname sits there by design until its app's first
//     successful build/deploy lands an Ingress -- ReconcilePendingHostnames
//     will not let that row fail on its own (see domains.go), so counting it
//     here would report every app still waiting on its first build as a
//     domain problem the operator needs to act on.
//   - "authorization": domain_authorizations rows that failed apex
//     verification, or have sat in pending for over a day (the TXT record was
//     never published).
func (h *Handler) overviewDomainIssues(ctx context.Context) ([]overviewDomainIssue, error) {
	out := []overviewDomainIssue{}

	hostRows, err := h.pool.Query(ctx, `
		SELECT dh.hostname, dh.status, dh.cert_status, p.display_name,
		       extract(epoch FROM now() - dh.updated_at)::int
		FROM domain_hostnames dh
		JOIN environments e ON e.id = dh.environment_id
		JOIN projects p     ON p.id = e.project_id
		WHERE (dh.status = 'failed' AND (dh.status_reason IS NULL OR dh.status_reason <> $1))
		   OR (dh.status = 'pending' AND dh.created_at < now() - interval '1 day'
		       AND (dh.status_reason IS NULL OR dh.status_reason <> $2))
		ORDER BY dh.updated_at ASC
		LIMIT 50`,
		hostnameReasonAppDeleted, hostnameReasonAwaitingFirstDeploy,
	)
	if err != nil {
		return nil, err
	}
	for hostRows.Next() {
		var i overviewDomainIssue
		i.Stage = "hostname"
		if err := hostRows.Scan(&i.Hostname, &i.Status, &i.CertStatus, &i.ProjectName, &i.AgeSeconds); err != nil {
			hostRows.Close()
			return nil, err
		}
		out = append(out, i)
	}
	hostRows.Close()
	if err := hostRows.Err(); err != nil {
		return nil, err
	}

	authRows, err := h.pool.Query(ctx, `
		SELECT da.apex_domain, da.status, p.display_name,
		       extract(epoch FROM now() - da.updated_at)::int
		FROM domain_authorizations da
		JOIN projects p ON p.id = da.project_id
		WHERE da.status = 'failed'
		   OR (da.status = 'pending' AND da.created_at < now() - interval '1 day')
		ORDER BY da.updated_at ASC
		LIMIT 50`,
	)
	if err != nil {
		return nil, err
	}
	for authRows.Next() {
		var i overviewDomainIssue
		i.Stage = "authorization"
		if err := authRows.Scan(&i.Hostname, &i.Status, &i.ProjectName, &i.AgeSeconds); err != nil {
			authRows.Close()
			return nil, err
		}
		out = append(out, i)
	}
	authRows.Close()
	return out, authRows.Err()
}

type overviewStuckOperation struct {
	ID           string `json:"id"`
	Action       string `json:"action"`
	ResourceKind string `json:"resource_kind"`
	ResourceName string `json:"resource_name"`
	Status       string `json:"status"`
	ProjectName  string `json:"project_name"`
	AgeSeconds   int    `json:"age_seconds"`
}

type overviewStuckOperations struct {
	Count  int                      `json:"count"`
	Oldest []overviewStuckOperation `json:"oldest"`
}

// overviewStuckOperations lists operations rows that never reached a terminal
// status within stuckOperationThreshold -- the symptom is always the same
// regardless of resource_kind: whatever agent was supposed to claim and finish
// the row (gitops-agent, portainer-agent, the box worker) died, crashed, or
// lost the row, and it sits invisible in operations forever unless someone runs
// a manual query. Capped to the 20 oldest so a genuine platform-wide stall
// (every agent down at once) cannot balloon the payload; Count is the true
// total so the operator can tell "1 stuck operation" from "400 stuck
// operations, showing the 20 oldest".
//
// Terminality comes from terminalOperationStatuses, not from literals written
// out here. WaitingForApproval is excluded on top of it: that row is parked on
// a human decision by design, so counting it as a dead agent would recreate the
// same false-positive class in a new place.
func (h *Handler) overviewStuckOperations(ctx context.Context) (overviewStuckOperations, error) {
	var out overviewStuckOperations
	thresholdSeconds := stuckOperationThreshold.Seconds()
	settled := append(append([]string{}, terminalOperationStatuses...), string(models.OperationStatusWaitingForApproval))

	if err := h.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM operations o
		WHERE o.status <> ALL($2)
		  AND o.created_at < now() - make_interval(secs => $1)`,
		thresholdSeconds, settled,
	).Scan(&out.Count); err != nil {
		return out, err
	}

	rows, err := h.pool.Query(ctx, `
		SELECT o.id, o.action, o.resource_kind, o.resource_name, o.status,
		       p.display_name, extract(epoch FROM now() - o.created_at)::int
		FROM operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.status <> ALL($2)
		  AND o.created_at < now() - make_interval(secs => $1)
		ORDER BY o.created_at ASC
		LIMIT 20`,
		thresholdSeconds, settled,
	)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	out.Oldest = []overviewStuckOperation{}
	for rows.Next() {
		var op overviewStuckOperation
		if err := rows.Scan(&op.ID, &op.Action, &op.ResourceKind, &op.ResourceName, &op.Status, &op.ProjectName, &op.AgeSeconds); err != nil {
			return out, err
		}
		out.Oldest = append(out.Oldest, op)
	}
	return out, rows.Err()
}

type overviewFailedBuild struct {
	AppName       string    `json:"app_name"`
	ProjectID     uuid.UUID `json:"project_id"`
	ProjectName   string    `json:"project_name"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	CommitSha     string    `json:"commit_sha"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	FailReason    string    `json:"fail_reason,omitempty"`
	AgeSeconds    int       `json:"age_seconds"`
}

// overviewFailedLatestBuilds lists apps whose MOST RECENT build failed, using
// the same "latest build per git_repo via LATERAL" pattern apps.go already
// uses for the app list's build-status badge. This is deliberately about the
// latest build only, not any failed build ever: an app can be Ready right now
// (last successful deploy still running) while its latest push is broken, and
// that gap is invisible everywhere else on this panel -- Ready apps never
// appear in overviewNotReadyApps, and overviewBuilds only reports a 7-day
// failure COUNT with no way to tell which app it belongs to.
//
// ProjectID/EnvironmentID/FailReason ride along so the console can offer a
// one-click "Auto-fix with AI" action straight from this row (TriggerAutofix,
// autofix.go) instead of making the operator navigate to the app first --
// FailReason is what lets the button gate on isRepoFixable the same way the
// per-app surfaces already do (frontend/lib/build-failure.ts), so an admin
// is never offered a run against a platform_error/git_auth_failed/app_deleted
// build that no PR could fix.
func (h *Handler) overviewFailedLatestBuilds(ctx context.Context) ([]overviewFailedBuild, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT gr.app_name, gr.project_id, p.display_name, gr.environment_id,
		       lb.commit_sha, COALESCE(lb.error_message, ''), COALESCE(lb.fail_reason, ''),
		       extract(epoch FROM now() - lb.created_at)::int
		FROM git_repos gr
		JOIN projects p ON p.id = gr.project_id
		JOIN LATERAL (
			SELECT status, commit_sha, error_message, fail_reason, created_at
			FROM builds b
			WHERE b.git_repo_id = gr.id
			ORDER BY b.created_at DESC
			LIMIT 1
		) lb ON true
		WHERE lb.status = 'failed'
		ORDER BY lb.created_at DESC
		LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []overviewFailedBuild{}
	for rows.Next() {
		var b overviewFailedBuild
		if err := rows.Scan(&b.AppName, &b.ProjectID, &b.ProjectName, &b.EnvironmentID,
			&b.CommitSha, &b.ErrorMessage, &b.FailReason, &b.AgeSeconds); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// overviewDeadAppCap bounds live_urls.dead_apps the same way every other
// overview list is capped: a platform-wide outage must not balloon the
// payload, and 20 named apps, external owners first, is enough for an
// operator to start triage without reading a raw count.
const overviewDeadAppCap = 20

// overviewLiveURLs answers the question builds.last_7d_success cannot: of the
// apps with an active public hostname, how many of them actually respond? The
// denominator is every App row (kind='App', not deleted -- deletion removes
// the resource_snapshots row rather than soft-deleting it) with a non-empty
// summary_json.url and url_status='active'; gitops-agent's status reconciler
// is the sole writer of both that url/url_status pair and the probe fields
// consumed below (summary_json.http_status/http_reason/http_checked_at).
//
// Those probe fields do not exist in the database yet as this lands -- the
// reconciler writes them on its own rollout schedule. Every row is therefore
// expected to read as stale on day one, and the query and Go-side parsing
// below treat a missing/unparseable http_status, http_reason or
// http_checked_at as "no result", never as an error: an empty summary_json
// key and a malformed one collapse to the same stale outcome rather than
// failing the whole overview request.
//
// A row only counts as Checked (and only then as OK, AppResponded or Dead)
// when its http_checked_at parses and falls inside
// liveURLProbeFreshnessWindow; everything else -- absent, unparseable, or
// older than the window -- is Stale. A row flagged worker=true is Workers:
// it serves no HTTP at all, so whatever a leftover domain in front of it
// answers is neither health nor death. Dead is exactly isDeadProbeResult
// (http_status 0, or a 502/503/504 that ingress-nginx itself authored);
// every other non-2xx/3xx status, including a 5xx the app wrote, is
// AppResponded. DeadApps only ever names Dead rows --
// an app answering 404/401/etc is not in it -- sorted external-owner-first
// (see isInternalOwnerEmail) and capped at overviewDeadAppCap.
//
// A row that would otherwise be Dead is reclassified as NeverHTTP only when
// BOTH hold: app_url_http_seen (LEFT JOIN environments e, LEFT JOIN
// app_url_http_seen ahs ON ahs.namespace = e.namespace AND ahs.app_name =
// rs.name) has no row for it -- this app has never once answered an HTTP
// probe -- AND rs.first_seen_at is older than liveURLNeverHTTPMinAge (7
// days). Age alone is not the gate and never-served alone is not the gate;
// both are required because "never answered HTTP" by itself cannot tell a
// bot that was never a web app (fanvk, sevarateambot -- weeks old) apart
// from a real web app broken from birth, which has also, by definition,
// never answered. A young row that has never served HTTP stays Dead: it is
// either a brand-new bot too young to trust yet or a same-day deploy
// failure, and either way the owner must see it while it is fresh, not have
// it silently reclassified out of the one inventory that would show it. This
// is a re-labeling of a subset of what the isDeadProbeResult predicate above
// already flags as dead; it must never widen which statuses count as dead in
// the first place. If the join cannot resolve a namespace for the row
// (rs.environment_id has no matching environments row), ever-served-http is
// treated as true and the row stays Dead regardless of age -- the same
// fail-loud-not-silent rule hasServedHTTP applies to its own database
// errors, so a lookup this handler cannot answer never quietly clears a real
// alert.
func (h *Handler) overviewLiveURLs(ctx context.Context) (overviewLiveURLs, error) {
	out := overviewLiveURLs{DeadApps: []overviewDeadApp{}}

	rows, err := h.pool.Query(ctx, `
		SELECT rs.name, p.display_name, COALESCE(u.email, ''),
		       COALESCE(rs.summary_json->>'url', ''),
		       COALESCE(rs.summary_json->>'http_status', ''),
		       COALESCE(rs.summary_json->>'http_reason', ''),
		       COALESCE(rs.summary_json->>'http_checked_at', ''),
		       COALESCE(rs.summary_json->>'worker', '') = 'true',
		       CASE WHEN e.namespace IS NULL THEN true ELSE (ahs.namespace IS NOT NULL) END,
		       rs.first_seen_at
		FROM resource_snapshots rs
		JOIN projects p            ON p.id = rs.project_id
		LEFT JOIN users u          ON u.id = p.owner_id
		LEFT JOIN environments e   ON e.id = rs.environment_id
		LEFT JOIN app_url_http_seen ahs
		       ON ahs.namespace = e.namespace AND ahs.app_name = rs.name
		WHERE rs.kind = 'App'
		  AND COALESCE(rs.summary_json->>'url', '') <> ''
		  AND rs.summary_json->>'url_status' = 'active'`,
	)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	deadApps := []overviewDeadApp{}
	now := time.Now()
	for rows.Next() {
		var name, projectName, ownerEmail, url, httpStatusRaw, httpReason, checkedAtRaw string
		var worker, everServedHTTP bool
		var firstSeenAt time.Time
		if err := rows.Scan(&name, &projectName, &ownerEmail, &url, &httpStatusRaw, &httpReason, &checkedAtRaw, &worker, &everServedHTTP, &firstSeenAt); err != nil {
			return out, err
		}

		checkedAt, fresh := parseFreshProbeTimestamp(checkedAtRaw, now)
		if !fresh {
			out.Stale++
			continue
		}

		out.Checked++
		external := !isInternalOwnerEmail(ownerEmail)
		if external {
			out.CheckedExternal++
		} else {
			out.CheckedInternal++
		}

		httpStatus, _ := strconv.Atoi(strings.TrimSpace(httpStatusRaw))
		if httpStatus != 0 && httpStatus < 400 {
			out.OK++
			continue
		}

		if worker {
			out.Workers++
			continue
		}

		if !isDeadProbeResult(httpStatus, httpReason) {
			out.AppResponded++
			continue
		}

		if !everServedHTTP && now.Sub(firstSeenAt) >= liveURLNeverHTTPMinAge {
			out.NeverHTTP++
			continue
		}

		out.Dead++
		if external {
			out.DeadExternal++
		} else {
			out.DeadInternal++
		}
		deadApps = append(deadApps, overviewDeadApp{
			Name:        name,
			ProjectName: projectName,
			OwnerEmail:  ownerEmail,
			Hostname:    url,
			HTTPStatus:  httpStatus,
			HTTPReason:  httpReason,
			CheckedAt:   checkedAt,
			External:    external,
		})
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	sort.SliceStable(deadApps, func(i, j int) bool {
		return deadApps[i].External && !deadApps[j].External
	})
	if len(deadApps) > overviewDeadAppCap {
		deadApps = deadApps[:overviewDeadAppCap]
	}
	out.DeadApps = deadApps

	return out, nil
}

// parseFreshProbeTimestamp parses an RFC3339 http_checked_at value and reports
// whether it both parsed and falls inside liveURLProbeFreshnessWindow of now.
// An empty or unparseable value returns (nil, false) rather than an error --
// summary_json is a schemaless bag gitops-agent writes to independently of
// this handler, so a missing or malformed stamp is a normal "no fresh result"
// outcome for this panel, not a fault to surface as a 500.
func parseFreshProbeTimestamp(raw string, now time.Time) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, false
	}
	if now.Sub(t) > liveURLProbeFreshnessWindow {
		return nil, false
	}
	return &t, true
}

// overviewMoney returns the same platform business economics as
// /admin/costs (hardware spend, revenue, margin, top loss-makers), reusing
// buildAdminCostSummary (admin_costs.go) over the shared cached snapshot so
// this second call costs no extra OpenCost/Beget round-trip. The overview
// card intentionally shows business numbers (real hardware bill, revenue,
// margin), not raw per-project OpenCost allocation crumbs -- owners read
// "Деньги" as P&L, not a resource-usage debug view. Never fails the overview
// request: any degradation in buildAdminCostSummary yields
// money.available=false with a note.
func (h *Handler) overviewMoney(ctx context.Context) overviewMoney {
	if h.opencost == nil {
		return overviewMoney{Available: false, Note: "OpenCost not configured"}
	}

	summary := h.buildAdminCostSummary(ctx, 30)
	if !summary.Available {
		note := summary.Note
		if note == "" {
			note = "cost data temporarily unavailable"
		}
		return overviewMoney{Available: false, Note: note}
	}

	return overviewMoney{
		Available:     true,
		Currency:      costCurrency,
		Days:          summary.Days,
		HardwareTotal: round2(summary.HardwareTotal),
		RevenueTotal:  round2(summary.TotalRevenue),
		MarginTotal:   round2(summary.TotalRevenue - summary.HardwareTotal),
		TopLossMakers: summary.TopLossMakers,

		PaidTotal:        summary.Money.PaidRUB,
		MeteredTotal:     summary.Money.MeteredRUB,
		UncollectedTotal: summary.Money.UncollectedRUB,
	}
}

// overviewDynamics returns a per-day series for the last `days` days: signups,
// builds split success/failed, and apps first seen. "First seen" is the frozen
// resource_snapshots.first_seen_at (set once at insert, migration 049); it
// replaced a min(last_synced_at) proxy that collapsed every live app onto the
// current day, because last_synced_at is re-stamped every ~30s reconcile.
func (h *Handler) overviewDynamics(ctx context.Context, days int) ([]overviewDayPoint, error) {
	since := time.Now().AddDate(0, 0, -days+1)

	byDate := make(map[string]*overviewDayPoint)
	dates := make([]string, 0, days)
	for i := 0; i < days; i++ {
		d := since.AddDate(0, 0, i).Format("2006-01-02")
		dates = append(dates, d)
		byDate[d] = &overviewDayPoint{Date: d}
	}

	signupRows, err := h.pool.Query(ctx, `
		SELECT to_char(created_at, 'YYYY-MM-DD'), count(*)
		FROM user_accounts
		WHERE created_at >= $1 AND account_kind = $2
		GROUP BY 1`,
		since, overviewCustomerKind,
	)
	if err != nil {
		return nil, err
	}
	for signupRows.Next() {
		var d string
		var n int
		if err := signupRows.Scan(&d, &n); err != nil {
			signupRows.Close()
			return nil, err
		}
		if p, ok := byDate[d]; ok {
			p.Signups = n
		}
	}
	signupRows.Close()
	if err := signupRows.Err(); err != nil {
		return nil, err
	}

	buildRows, err := h.pool.Query(ctx, `
		SELECT to_char(created_at, 'YYYY-MM-DD'), status, count(*)
		FROM builds
		WHERE created_at >= $1 AND status IN ('success', 'failed')
		GROUP BY 1, 2`,
		since,
	)
	if err != nil {
		return nil, err
	}
	for buildRows.Next() {
		var d, status string
		var n int
		if err := buildRows.Scan(&d, &status, &n); err != nil {
			buildRows.Close()
			return nil, err
		}
		p, ok := byDate[d]
		if !ok {
			continue
		}
		if status == "success" {
			p.BuildSuccess = n
		} else {
			p.BuildFailed = n
		}
	}
	buildRows.Close()
	if err := buildRows.Err(); err != nil {
		return nil, err
	}

	appRows, err := h.pool.Query(ctx, `
		SELECT to_char(first_seen_at, 'YYYY-MM-DD'), count(*)
		FROM resource_snapshots
		WHERE kind = 'App' AND first_seen_at >= $1
		GROUP BY 1`,
		since,
	)
	if err != nil {
		return nil, err
	}
	for appRows.Next() {
		var d string
		var n int
		if err := appRows.Scan(&d, &n); err != nil {
			appRows.Close()
			return nil, err
		}
		if p, ok := byDate[d]; ok {
			p.NewApps = n
		}
	}
	appRows.Close()
	if err := appRows.Err(); err != nil {
		return nil, err
	}

	out := make([]overviewDayPoint, 0, days)
	for _, d := range dates {
		out = append(out, *byDate[d])
	}
	return out, nil
}
