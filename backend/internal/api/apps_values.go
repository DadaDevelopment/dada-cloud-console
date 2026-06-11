package api

import (
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/wstoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetValuesToken issues a short-lived delegate token that the frontend uses to
// open a WebSocket connection directly to the gitops-agent values editor.
//
// POST /api/v1/projects/:projectId/environments/:envId/apps/:appName/values-token
//
// Returns { token, ws_url } on success.
// Returns 503 when the values editor is not configured (missing env vars).
//
// @ID          createValuesToken
// @Summary     Issue a short-lived token for the app values editor
// @Description Issues a short-lived delegate token plus WebSocket URL that the frontend uses to open a live editing session against the gitops-agent values editor for an app's config file (values.yaml, compose.yaml or .env). Requires write access. Returns 503 when the values editor is not configured.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       file      query    string false "Config file to edit: values.yaml, compose.yaml or .env (default values.yaml)"
// @Success     200       {object} map[string]interface{} "object with token and ws_url"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/values-token [post]
func (h *Handler) GetValuesToken(c *gin.Context) {
	if h.cfg.GitopsValuesTokenSecret == "" || h.cfg.GitopsAgentWSURL == "" {
		respondError(c, http.StatusServiceUnavailable, "values editor not configured")
		return
	}

	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	// Which file the editor session targets. Defaults to values.yaml (Helm apps);
	// compose apps use compose.yaml / .env.
	file := c.Query("file")
	if file == "" {
		file = "values.yaml"
	}
	switch file {
	case "values.yaml", "compose.yaml", ".env":
		// allowed
	default:
		respondError(c, http.StatusBadRequest, "file must be one of: values.yaml, compose.yaml, .env")
		return
	}

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID, claims.Groups)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	// Resolve project slug and env slug — the token carries slugs, not UUIDs,
	// because the gitops-agent path helpers use slugs.
	var projectSlug, envSlug string
	err = h.pool.QueryRow(c.Request.Context(), `
		SELECT p.name, e.name
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.id = $1 AND e.id = $2
	`, projectID, envID).Scan(&projectSlug, &envSlug)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve project/environment")
		return
	}

	token, err := wstoken.Sign(h.cfg.GitopsValuesTokenSecret, wstoken.Claims{
		Project: projectSlug,
		Env:     envSlug,
		App:     appName,
		File:    file,
		Exp:     time.Now().Add(90 * time.Second).Unix(),
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to sign token")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":  token,
		"ws_url": h.cfg.GitopsAgentWSURL,
	})
}
