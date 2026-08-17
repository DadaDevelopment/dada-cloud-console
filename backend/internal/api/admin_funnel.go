package api

import (
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

// funnelWindows maps the UI's fixed window choices to a Postgres interval
// literal. "all" has no WHERE clause at all (interval math can't express
// "no lower bound").
var funnelWindows = map[string]string{
	"7d":  "7 days",
	"30d": "30 days",
	"90d": "90 days",
}

type adminFunnelCohortCount struct {
	AccountKind string `json:"account_kind"`
	Count       int    `json:"count"`
}

type adminFunnelResponse struct {
	Window        string                   `json:"window"`
	ExcludedKinds []string                 `json:"excluded_kinds"`
	Signups       int                      `json:"signups"`
	AppUp         int                      `json:"app_up"`
	DBUp          int                      `json:"db_up"`
	VMUp          int                      `json:"vm_up"`
	BoxUp         int                      `json:"box_up"`
	S3Up          int                      `json:"s3_up"`
	ModelUp       int                      `json:"model_up"`
	Paid          int                      `json:"paid"`
	PaidNote      string                   `json:"paid_note,omitempty"`
	CohortCounts  []adminFunnelCohortCount `json:"cohort_counts"`
}

// GetAdminFunnel returns product-adoption funnel counts (signup -> App/DB/VM/
// Box/S3/Model up -> paid) for a signup window and account_kind cohort.
//
// Resource "up" is computed per resource kind, not per row, because the
// ground-truth signal differs by kind: App/DB/S3 use resource_snapshots.phase
// = 'Ready' (the gitops reconciler's verdict); AIModel has no working phase
// signal (the worker never stamps Ready for it), so presence is the only
// available proxy; AppServer (VM) and Box are not in resource_snapshots at
// all and use their own tables' "ever became reachable" columns (vm_ip / ssh_host)
// instead of current status, because status cycles back through
// Deleted/Failed after use and a status filter reads as a false zero.
//
// Paid joins payments to users via customer_email, not created_by_sub:
// created_by_sub is empty on every existing production row (P0, undiagnosed
// prior to this endpoint), while customer_email is a required, always-populated
// field at checkout (yookassa.Checkout enforces it via requireReceiptEmail).
//
// @ID          getAdminFunnel
// @Summary     Product adoption funnel
// @Description Signup -> resource-kind adoption -> paid counts for a signup window, excluding chosen account_kind cohorts.
// @Tags        admin
// @Param       window query string false "7d|30d|90d|all" default(30d)
// @Param       exclude_kind query string false "comma-separated account_kind values to hide"
// @Success     200 {object} adminFunnelResponse
// @Router      /admin/funnel [get]
func (h *Handler) GetAdminFunnel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) && !isPlatformAnalyst(claims) {
		respondForbidden(c)
		return
	}

	window := c.DefaultQuery("window", "30d")
	interval, known := funnelWindows[window]
	if !known && window != "all" {
		respondError(c, http.StatusBadRequest, "invalid window, expected 7d|30d|90d|all")
		return
	}

	var excludeKinds []string
	if raw := c.Query("exclude_kind"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				excludeKinds = append(excludeKinds, k)
			}
		}
	}

	sinceClause := "TRUE"
	if interval != "" {
		sinceClause = "u.created_at >= now() - interval '" + interval + "'"
	}

	query := `
		WITH scope AS (
			SELECT u.id, u.email
			FROM users u
			WHERE ` + sinceClause + `
			  AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		proj AS (
			SELECT p.id, p.owner_id FROM projects p JOIN scope s ON s.id = p.owner_id
		)
		SELECT
			(SELECT count(*) FROM scope),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'App' AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind IN ('ServiceDatabase','ServiceDatabaseV2') AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM app_servers a JOIN proj p ON p.id = a.project_id
				WHERE a.status = 'Ready' OR a.vm_ip <> ''),
			(SELECT count(DISTINCT p.owner_id) FROM boxes b JOIN proj p ON p.id = b.project_id
				WHERE b.ssh_host <> ''),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'S3Bucket' AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'AIModel'),
			(SELECT count(DISTINCT s.id) FROM scope s JOIN payments pay ON lower(pay.customer_email) = lower(s.email)
				WHERE pay.status = 'succeeded')
	`

	var excludeArg interface{}
	if len(excludeKinds) > 0 {
		excludeArg = excludeKinds
	}

	resp := adminFunnelResponse{Window: window, ExcludedKinds: excludeKinds}
	err := h.pool.QueryRow(c.Request.Context(), query, excludeArg).Scan(
		&resp.Signups, &resp.AppUp, &resp.DBUp, &resp.VMUp, &resp.BoxUp, &resp.S3Up, &resp.ModelUp, &resp.Paid,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}
	resp.PaidNote = "AIModel counts row presence only, its phase never reaches Ready; VM/Box count ever-reachable, not current status."

	cohortRows, err := h.pool.Query(c.Request.Context(),
		`SELECT u.account_kind, count(*) FROM users u WHERE `+sinceClause+` GROUP BY u.account_kind ORDER BY u.account_kind`)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read funnel cohorts")
		return
	}
	defer cohortRows.Close()
	resp.CohortCounts = make([]adminFunnelCohortCount, 0)
	for cohortRows.Next() {
		var cc adminFunnelCohortCount
		if err := cohortRows.Scan(&cc.AccountKind, &cc.Count); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan funnel cohorts")
			return
		}
		resp.CohortCounts = append(resp.CohortCounts, cc)
	}

	c.JSON(http.StatusOK, resp)
}
