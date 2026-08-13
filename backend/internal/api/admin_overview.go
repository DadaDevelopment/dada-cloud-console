package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
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
}

type overviewBuilds struct {
	Last7dSuccess  int `json:"last_7d_success"`
	Last7dFailed   int `json:"last_7d_failed"`
	Last7dCanceled int `json:"last_7d_canceled"`
	Last24h        int `json:"last_24h"`
}

type overviewDomains struct {
	Active  int `json:"active"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
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

	ctx := c.Request.Context()

	users, err := h.overviewUsers(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate users")
		return
	}
	projects, err := h.overviewProjects(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate projects")
		return
	}
	builds, err := h.overviewBuilds(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate builds")
		return
	}
	domains, err := h.overviewDomains(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate domains")
		return
	}
	notReady, err := h.overviewNotReadyApps(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list not-ready apps")
		return
	}
	noSignal, err := h.overviewNoSignalApps(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list apps without a health signal")
		return
	}
	notReadyFreshness, err := h.overviewNotReadyFreshness(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check not-ready freshness")
		return
	}
	notReadyOther, err := h.overviewNotReadyOtherResources(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list not-ready resources")
		return
	}
	domainIssues, err := h.overviewDomainIssues(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list domain issues")
		return
	}
	stuckOps, err := h.overviewStuckOperations(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list stuck operations")
		return
	}
	failedBuilds, err := h.overviewFailedLatestBuilds(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list failed latest builds")
		return
	}
	dynamics, err := h.overviewDynamics(ctx, days)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate dynamics")
		return
	}

	c.JSON(http.StatusOK, gin.H{
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
	})
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
			count(*) FILTER (WHERE status = 'failed')
		FROM domain_hostnames`,
	).Scan(&out.Active, &out.Pending, &out.Failed)
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
	AppName      string `json:"app_name"`
	ProjectName  string `json:"project_name"`
	CommitSha    string `json:"commit_sha"`
	ErrorMessage string `json:"error_message,omitempty"`
	AgeSeconds   int    `json:"age_seconds"`
}

// overviewFailedLatestBuilds lists apps whose MOST RECENT build failed, using
// the same "latest build per git_repo via LATERAL" pattern apps.go already
// uses for the app list's build-status badge. This is deliberately about the
// latest build only, not any failed build ever: an app can be Ready right now
// (last successful deploy still running) while its latest push is broken, and
// that gap is invisible everywhere else on this panel -- Ready apps never
// appear in overviewNotReadyApps, and overviewBuilds only reports a 7-day
// failure COUNT with no way to tell which app it belongs to.
func (h *Handler) overviewFailedLatestBuilds(ctx context.Context) ([]overviewFailedBuild, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT gr.app_name, p.display_name, lb.commit_sha, COALESCE(lb.error_message, ''),
		       extract(epoch FROM now() - lb.created_at)::int
		FROM git_repos gr
		JOIN projects p ON p.id = gr.project_id
		JOIN LATERAL (
			SELECT status, commit_sha, error_message, created_at
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
		if err := rows.Scan(&b.AppName, &b.ProjectName, &b.CommitSha, &b.ErrorMessage, &b.AgeSeconds); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
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
