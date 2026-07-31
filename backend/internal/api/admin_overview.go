package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
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

type overviewUsers struct {
	Total     int `json:"total"`
	New24h    int `json:"new_24h"`
	New7d     int `json:"new_7d"`
	New30d    int `json:"new_30d"`
	Active48h int `json:"active_48h"`
}

type overviewApps struct {
	Total   int            `json:"total"`
	Ready   int            `json:"ready"`
	Broken  int            `json:"broken"`
	ByPhase map[string]int `json:"by_phase"`
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

type overviewMoney struct {
	Available     bool                 `json:"available"`
	Note          string               `json:"note,omitempty"`
	Currency      string               `json:"currency,omitempty"`
	Days          int                  `json:"days,omitempty"`
	HardwareTotal float64              `json:"hardware_total,omitempty"`
	RevenueTotal  float64              `json:"revenue_total,omitempty"`
	MarginTotal   float64              `json:"margin_total,omitempty"`
	TopLossMakers []adminCostLossMaker `json:"top_loss_makers,omitempty"`
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
	if !isGod(claims) {
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
	dynamics, err := h.overviewDynamics(ctx, days)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to aggregate dynamics")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users":         users,
		"projects":      projects,
		"builds":        builds,
		"domains":       domains,
		"money":         h.overviewMoney(ctx),
		"not_ready":     notReady,
		"dynamics":      dynamics,
		"dynamics_days": days,
	})
}

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
func (h *Handler) overviewNotReadyApps(ctx context.Context) ([]overviewNotReadyApp, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT rs.name, p.display_name, rs.phase, COALESCE(u.email, '')
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
		if err := rows.Scan(&a.Name, &a.ProjectName, &a.Phase, &a.OwnerEmail); err != nil {
			return nil, err
		}
		out = append(out, a)
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
