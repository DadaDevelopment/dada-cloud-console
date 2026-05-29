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

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
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
