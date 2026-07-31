package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// envVar mirrors the frontend EnvVar shape. value is only populated on reveal.
//
// PreviewOverride marks a row that lives in preview_env_overrides rather than
// env_vars: it is set on a PARENT environment and, when true, its value wins
// over the parent's own env_vars row for the same key when a preview (PR)
// environment is created from this one. Scope is meaningless for such a row
// (preview_env_overrides has no scope column) and is left empty.
type envVar struct {
	ID              uuid.UUID `json:"id"`
	EnvironmentID   uuid.UUID `json:"environment_id"`
	AppName         string    `json:"app_name"`
	Key             string    `json:"key"`
	Value           *string   `json:"value,omitempty"` // masked/omitted in list; set only on reveal
	IsSecret        bool      `json:"is_secret"`
	Scope           string    `json:"scope,omitempty"`
	PreviewOverride bool      `json:"preview_override"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// queueEnvApply re-deploys an app so an env var change actually reaches its
// pods, and reports whether it queued anything.
//
// Env vars are resolved at RENDER time, not at save time: the gitops-agent
// decrypts env_vars while rendering an operation and writes them into
// values.yaml / a per-app Secret. Saving a variable alone therefore changed
// nothing a user could observe — the row sat in the database and the running
// pods kept the environment they were born with. Observed live: BOT_TOKEN was
// saved through the console, the app kept crashlooping on KeyError: 'BOT_TOKEN',
// and only an unrelated deploy picked the value up. Restart is not a substitute:
// it is compose-only, and re-rendering is exactly what is needed here.
//
// The re-deploy is the app's CURRENT image, so this is a no-op for the workload
// itself and the only observable effect is the new environment. Apps with no
// image yet (a bare app, or an upload whose first build has not finished) are
// skipped: there is nothing to deploy, and their env is picked up by the deploy
// that materializes them.
func (h *Handler) queueEnvApply(c *gin.Context, claims *auth.Claims, projectID, envID uuid.UUID, appName string) (*models.Operation, bool) {
	var image string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(summary_json->>'image', '') FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&image); err != nil || image == "" {
		return nil, false
	}

	payloadBytes, err := json.Marshal(models.DeployImageVersionPayload{AppName: appName, Image: image})
	if err != nil {
		return nil, false
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		return nil, false
	}
	return &op, true
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

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
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

	overrideRows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, environment_id, app_name, key, value_encrypted, is_secret, created_at, updated_at
		 FROM preview_env_overrides
		 WHERE environment_id = $1 AND app_name = $2
		 ORDER BY key`,
		envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query preview env overrides")
		return
	}
	defer overrideRows.Close()

	for overrideRows.Next() {
		var ev envVar
		var encrypted []byte
		if err := overrideRows.Scan(&ev.ID, &ev.EnvironmentID, &ev.AppName, &ev.Key,
			&encrypted, &ev.IsSecret, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan preview env override")
			return
		}
		ev.PreviewOverride = true
		if !ev.IsSecret {
			if plain, derr := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted); derr == nil {
				v := string(plain)
				ev.Value = &v
			}
		}
		envVars = append(envVars, ev)
	}
	if err := overrideRows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading preview env overrides")
		return
	}

	c.JSON(http.StatusOK, gin.H{"env_vars": envVars})
}

type setEnvVarRequest struct {
	Value           string `json:"value"`
	IsSecret        bool   `json:"is_secret"`
	Scope           string `json:"scope"`
	PreviewOverride bool   `json:"preview_override"`
}

// SetEnvVar upserts a single environment variable (value stored encrypted).
//
// When preview_override is true, the variable is written to
// preview_env_overrides instead of env_vars: it stays inert on THIS
// environment (never rendered, never copied verbatim) and only takes effect
// as an override on preview (PR) environments created from this one. scope is
// ignored in that case (preview_env_overrides has no scope column).
//
// @ID          setEnvVar
// @Summary     Set an environment variable
// @Description Creates or updates a single environment variable for an app. The value is always stored AES-GCM encrypted. Requires write access. When preview_override is true, writes a preview-only override instead (see preview_env_overrides).
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

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
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
	// 4KiB/var cap (see plan §5).
	if len(req.Value) > 4*1024 {
		respondError(c, http.StatusBadRequest, "value exceeds 4KiB limit")
		return
	}

	var ev envVar
	if req.PreviewOverride {
		encrypted, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.Value))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to encrypt value")
			return
		}
		row := h.pool.QueryRow(c.Request.Context(),
			`INSERT INTO preview_env_overrides (environment_id, app_name, key, value_encrypted, is_secret, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (environment_id, app_name, key)
			 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted,
			               is_secret = EXCLUDED.is_secret,
			               updated_at = NOW()
			 RETURNING id, environment_id, app_name, key, is_secret, created_at, updated_at`,
			envID, appName, key, encrypted, req.IsSecret, claims.UserID,
		)
		if err := row.Scan(&ev.ID, &ev.EnvironmentID, &ev.AppName, &ev.Key,
			&ev.IsSecret, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to save preview env override")
			return
		}
		ev.PreviewOverride = true
	} else {
		scope := req.Scope
		if scope == "" {
			scope = "runtime"
		}
		if scope != "build" && scope != "runtime" && scope != "both" {
			respondError(c, http.StatusBadRequest, "scope must be one of: build, runtime, both")
			return
		}

		saved, err := h.upsertEnvVar(c.Request.Context(), envID, appName, key, req.Value, req.IsSecret, scope, claims.UserID.String())
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to save env var")
			return
		}
		ev = saved
	}

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'SetEnvVar', 'EnvVar', $3)`,
		claims.UserID, projectID, appName,
	)

	resp := gin.H{"env_var": ev}
	if !req.PreviewOverride {
		if op, queued := h.queueEnvApply(c, claims, projectID, envID, appName); queued {
			resp["operation"] = op
		}
	}
	c.JSON(http.StatusOK, resp)
}

type bulkEnvVarItem struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
	Scope    string `json:"scope"`
}

type bulkSetEnvVarsRequest struct {
	Vars []bulkEnvVarItem `json:"vars"`
}

// BulkSetEnvVars upserts many environment variables in one call.
//
// The single-variable endpoint costs a full round trip and, since env changes
// now queue a re-deploy, one deploy PER VARIABLE. Pasting a .env with eight
// keys through it means eight deploys racing each other. Here every variable is
// written first and exactly one re-deploy is queued at the end.
//
// Partial success is not offered: a half-applied .env is worse than a rejected
// one, because the app comes up with an environment the user never described.
// Validation therefore runs over the whole batch before anything is written.
//
// Preview overrides are deliberately out of scope — bulk entry exists for the
// "here is my .env, run my app" path, and preview_env_overrides is an expert
// feature that belongs on the single-variable form.
//
// @ID          bulkSetEnvVars
// @Summary     Set many environment variables at once
// @Description Creates or updates several environment variables for an app in a single request, then queues one re-deploy so the new environment reaches the running pods. Values are stored AES-GCM encrypted. Requires write access. The batch is all-or-nothing.
// @Tags        env-var
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     bulkSetEnvVarsRequest true "Variables to set"
// @Success     200       {object} map[string]interface{} "object with the saved env vars"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/env/bulk [post]
func (h *Handler) BulkSetEnvVars(c *gin.Context) {
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

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
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

	var req bulkSetEnvVarsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Vars) == 0 {
		respondError(c, http.StatusBadRequest, "vars is required")
		return
	}
	if len(req.Vars) > 200 {
		respondError(c, http.StatusBadRequest, "at most 200 variables per request")
		return
	}

	seen := make(map[string]struct{}, len(req.Vars))
	for i := range req.Vars {
		v := &req.Vars[i]
		if !validEnvKey(v.Key) {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("invalid key %q: expected letters, digits and underscore, not starting with a digit", v.Key))
			return
		}
		if _, dup := seen[v.Key]; dup {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("duplicate key %q", v.Key))
			return
		}
		seen[v.Key] = struct{}{}
		if len(v.Value) > 4*1024 {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("value for %q exceeds 4KiB limit", v.Key))
			return
		}
		if v.Scope == "" {
			v.Scope = "runtime"
		}
		if v.Scope != "build" && v.Scope != "runtime" && v.Scope != "both" {
			respondError(c, http.StatusBadRequest, "scope must be one of: build, runtime, both")
			return
		}
	}

	saved := make([]envVar, 0, len(req.Vars))
	for _, v := range req.Vars {
		ev, err := h.upsertEnvVar(c.Request.Context(), envID, appName, v.Key, v.Value, v.IsSecret, v.Scope, claims.UserID.String())
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to save env var")
			return
		}
		saved = append(saved, ev)
	}

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'SetEnvVar', 'EnvVar', $3)`,
		claims.UserID, projectID, appName,
	)

	resp := gin.H{"env_vars": saved}
	if op, queued := h.queueEnvApply(c, claims, projectID, envID, appName); queued {
		resp["operation"] = op
	}
	c.JSON(http.StatusOK, resp)
}

// validEnvKey reports whether key is a POSIX-shell-safe environment variable
// name. The single-variable form enforces the same shape in the browser via a
// pattern attribute; bulk entry accepts a pasted blob, so the check has to live
// on the server too.
func validEnvKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for i, r := range key {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
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
// @Param       preview_override query bool  false "Reveal the preview-only override instead of the base env var"
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

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
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

	table := "env_vars"
	if c.Query("preview_override") == "true" {
		table = "preview_env_overrides"
	}

	var encrypted []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT value_encrypted FROM `+table+`
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

	c.JSON(http.StatusOK, gin.H{"value": string(plain), "preview_override": table == "preview_env_overrides"})
}

// DeleteEnvVar removes a single environment variable. When preview_override=true
// it removes the preview-only override instead (see preview_env_overrides).
//
// @ID          deleteEnvVar
// @Summary     Delete an environment variable
// @Description Removes a single environment variable from an app. Requires write access. Pass preview_override=true to delete a preview-only override instead.
// @Tags        env-var
// @Produce     json
// @Security    BearerAuth
// @Param       projectId       path     string true  "Project UUID"
// @Param       envId           path     string true  "Environment UUID"
// @Param       appName         path     string true  "App name"
// @Param       key             path     string true  "Variable key"
// @Param       preview_override query    bool   false "Delete the preview-only override instead of the base env var"
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

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
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

	table := "env_vars"
	if c.Query("preview_override") == "true" {
		table = "preview_env_overrides"
	}

	tag, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM `+table+` WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
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

	if table == "env_vars" {
		_, _ = h.queueEnvApply(c, claims, projectID, envID, appName)
	}

	c.Status(http.StatusNoContent)
}

// upsertEnvVar encrypts value and upserts one env_vars row, shared by the
// SetEnvVar HTTP handler and any server-side writer (e.g. the payments
// OAuth callback injecting YOOKASSA_OAUTH_TOKEN/YOOKASSA_ACCOUNT_ID). It does
// NOT trigger a re-render -- the value lands on the app's next deploy, same
// as SetEnvVar.
func (h *Handler) upsertEnvVar(ctx context.Context, envID uuid.UUID, appName, key, value string, secret bool, scope, createdBy string) (envVar, error) {
	encrypted, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(value))
	if err != nil {
		return envVar{}, fmt.Errorf("encrypt value: %w", err)
	}

	var ev envVar
	row := h.pool.QueryRow(ctx,
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (environment_id, app_name, key)
		 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted,
		               is_secret = EXCLUDED.is_secret,
		               scope = EXCLUDED.scope,
		               updated_at = NOW()
		 RETURNING id, environment_id, app_name, key, is_secret, scope, created_at, updated_at`,
		envID, appName, key, encrypted, secret, scope, createdBy,
	)
	if err := row.Scan(&ev.ID, &ev.EnvironmentID, &ev.AppName, &ev.Key,
		&ev.IsSecret, &ev.Scope, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		return envVar{}, fmt.Errorf("save env var: %w", err)
	}
	return ev, nil
}
