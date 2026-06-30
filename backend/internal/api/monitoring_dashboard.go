package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Server-side persistence for the per-user observability dashboard (ADR-011).
// The config is an opaque JSONB blob owned by the frontend (DashboardState):
// range, refresh, filters, group-by, aggregation and the panel layout with
// thresholds/annotations. One row per (monitoring_app, user); the frontend keeps
// localStorage as an offline cache and applies optimistic updates.

// maxDashboardConfigBytes bounds a stored dashboard blob. A large layout with
// many panels and thresholds is still well under this; it only guards abuse.
const maxDashboardConfigBytes = 256 * 1024

type saveDashboardRequest struct {
	Config  json.RawMessage `json:"config"`
	Version int             `json:"version"`
}

// GetMonitoringDashboard returns the calling user's saved dashboard config for a
// monitoring resource, or {config:null, version:0} when none is stored yet.
//
// @ID          getMonitoringDashboard
// @Summary     Get the saved dashboard config
// @Description Returns the calling user's persisted dashboard layout/config for a monitoring resource (per-user). Returns config:null when nothing is saved yet.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appId     path     string true "Monitoring resource UUID"
// @Success     200       {object} map[string]interface{} "object with config, version, updated_at"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/dashboard [get]
func (h *Handler) GetMonitoringDashboard(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var cfg []byte
	var version int
	var updated time.Time
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT config, version, updated_at FROM monitoring_dashboards
		 WHERE monitoring_app_id = $1 AND user_id = $2`,
		app.ID, claims.UserID,
	).Scan(&cfg, &version, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"config": nil, "version": 0})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load dashboard")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"config":     json.RawMessage(cfg),
		"version":    version,
		"updated_at": updated,
	})
}

// SaveMonitoringDashboard upserts the calling user's dashboard config for a
// monitoring resource. The config is stored verbatim (opaque to the backend)
// after a shape + size check.
//
// @ID          saveMonitoringDashboard
// @Summary     Save the dashboard config
// @Description Upserts the calling user's dashboard layout/config (per-user) for a monitoring resource. The config is an opaque JSON object owned by the frontend.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string               true "Project UUID"
// @Param       envId     path     string               true "Environment UUID"
// @Param       appId     path     string               true "Monitoring resource UUID"
// @Param       body      body     saveDashboardRequest true "Dashboard config blob + version"
// @Success     200       {object} map[string]interface{} "object with saved, version"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/dashboard [put]
func (h *Handler) SaveMonitoringDashboard(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var req saveDashboardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Config) == 0 {
		respondError(c, http.StatusBadRequest, "config is required")
		return
	}
	if len(req.Config) > maxDashboardConfigBytes {
		respondError(c, http.StatusRequestEntityTooLarge, "dashboard config too large")
		return
	}
	// Must be a JSON object so a row is never poisoned with a scalar/array.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(req.Config, &probe); err != nil {
		respondError(c, http.StatusBadRequest, "config must be a JSON object")
		return
	}
	if req.Version <= 0 {
		req.Version = 1
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO monitoring_dashboards (monitoring_app_id, user_id, config, version)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (monitoring_app_id, user_id)
		 DO UPDATE SET config = EXCLUDED.config, version = EXCLUDED.version, updated_at = NOW()`,
		app.ID, claims.UserID, []byte(req.Config), req.Version,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to save dashboard")
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "version": req.Version})
}
