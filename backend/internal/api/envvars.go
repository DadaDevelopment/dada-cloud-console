package api

import (
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// envVar mirrors the frontend EnvVar shape. value is only populated on reveal.
type envVar struct {
	ID            uuid.UUID `json:"id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	AppName       string    `json:"app_name"`
	Key           string    `json:"key"`
	Value         *string   `json:"value,omitempty"` // masked/omitted in list; set only on reveal
	IsSecret      bool      `json:"is_secret"`
	Scope         string    `json:"scope"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ListEnvVars returns the env vars for an app. Secret values are never returned —
// the frontend reveals a single secret on demand via the reveal endpoint.
//
// @ID          listEnvVars
// @Summary     List environment variables for an app
// @Description Returns the environment variables for an app. Non-secret values are returned in plaintext; secret values are masked (omitted). Read-only.
// @Tags        env-var
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with an env_vars array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/env [get]
func (h *Handler) ListEnvVars(c *gin.Context) {
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

	_, err = h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID, claims.Groups)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, environment_id, app_name, key, value_encrypted, is_secret, scope, created_at, updated_at
		 FROM env_vars
		 WHERE environment_id = $1 AND app_name = $2
		 ORDER BY key`,
		envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query env vars")
		return
	}
	defer rows.Close()

	envVars := []envVar{}
	for rows.Next() {
		var ev envVar
		var encrypted []byte
		if err := rows.Scan(&ev.ID, &ev.EnvironmentID, &ev.AppName, &ev.Key,
			&encrypted, &ev.IsSecret, &ev.Scope, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan env var")
			return
		}
		// Never return secret plaintext in the list. Non-secret values are decrypted
		// so the editor can show them inline.
		if !ev.IsSecret {
			if plain, derr := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted); derr == nil {
				v := string(plain)
				ev.Value = &v
			}
		}
		envVars = append(envVars, ev)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading env vars")
		return
	}

	c.JSON(http.StatusOK, gin.H{"env_vars": envVars})
}

type setEnvVarRequest struct {
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
	Scope    string `json:"scope"`
}

// SetEnvVar upserts a single environment variable (value stored encrypted).
//
// @ID          setEnvVar
// @Summary     Set an environment variable
// @Description Creates or updates a single environment variable for an app. The value is always stored AES-GCM encrypted. Requires write access.
// @Tags        env-var
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       envId     path     string           true "Environment UUID"
// @Param       appName   path     string           true "App name"
// @Param       key       path     string           true "Variable key"
// @Param       body      body     setEnvVarRequest true "Variable value"
// @Success     200       {object} map[string]interface{} "object with the saved env var"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/env/{key} [put]
func (h *Handler) SetEnvVar(c *gin.Context) {
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
	key := c.Param("key")

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

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	if key == "" {
		respondError(c, http.StatusBadRequest, "key is required")
		return
	}

	var req setEnvVarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = "runtime"
	}
	if scope != "build" && scope != "runtime" && scope != "both" {
		respondError(c, http.StatusBadRequest, "scope must be one of: build, runtime, both")
		return
	}
	// 4KiB/var cap (see plan §5).
	if len(req.Value) > 4*1024 {
		respondError(c, http.StatusBadRequest, "value exceeds 4KiB limit")
		return
	}

	encrypted, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.Value))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to encrypt value")
		return
	}

	var ev envVar
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (environment_id, app_name, key)
		 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted,
		               is_secret = EXCLUDED.is_secret,
		               scope = EXCLUDED.scope,
		               updated_at = NOW()
		 RETURNING id, environment_id, app_name, key, is_secret, scope, created_at, updated_at`,
		envID, appName, key, encrypted, req.IsSecret, scope, claims.UserID,
	)
	if err := row.Scan(&ev.ID, &ev.EnvironmentID, &ev.AppName, &ev.Key,
		&ev.IsSecret, &ev.Scope, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to save env var")
		return
	}

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'SetEnvVar', 'EnvVar', $3)`,
		claims.UserID, projectID, appName,
	)

	c.JSON(http.StatusOK, gin.H{"env_var": ev})
}

// RevealEnvVar returns the decrypted value of a single env var (write access required).
//
// @ID          revealEnvVar
// @Summary     Reveal a single environment variable
// @Description Returns the decrypted plaintext value of a single environment variable. Requires reveal=true and write access.
// @Tags        env-var
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       key       path     string true "Variable key"
// @Param       reveal    query    bool   true "Must be true to reveal"
// @Success     200       {object} map[string]string "object with the decrypted value"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/env/{key} [get]
func (h *Handler) RevealEnvVar(c *gin.Context) {
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
	key := c.Param("key")

	if c.Query("reveal") != "true" {
		respondError(c, http.StatusBadRequest, "reveal=true is required")
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

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	var encrypted []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT value_encrypted FROM env_vars
		 WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
		envID, appName, key,
	).Scan(&encrypted)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load env var")
		return
	}

	plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to decrypt value")
		return
	}

	c.JSON(http.StatusOK, gin.H{"value": string(plain)})
}

// DeleteEnvVar removes a single environment variable.
//
// @ID          deleteEnvVar
// @Summary     Delete an environment variable
// @Description Removes a single environment variable from an app. Requires write access.
// @Tags        env-var
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       key       path     string true "Variable key"
// @Success     204       {object} nil
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/env/{key} [delete]
func (h *Handler) DeleteEnvVar(c *gin.Context) {
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
	key := c.Param("key")

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

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	tag, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM env_vars WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
		envID, appName, key,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete env var")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}

	c.Status(http.StatusNoContent)
}
