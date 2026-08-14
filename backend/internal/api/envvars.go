package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
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
	Value         *string   `json:"value,omitempty"`
	IsSecret      bool      `json:"is_secret"`
	Scope         string    `json:"scope,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

	rejectEnv := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "SetEnvVar",
			ResourceKind:  "EnvVar",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	if key == "" {
		rejectEnv(http.StatusBadRequest, "key_required", "key is required")
		return
	}

	var req setEnvVarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectEnv(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	// 4KiB/var cap (see plan §5).
	if len(req.Value) > 4*1024 {
		rejectEnv(http.StatusBadRequest, "value_too_large", "value exceeds 4KiB limit")
		return
	}

	scope := req.Scope
	if scope == "" {
		scope = "runtime"
	}
	if scope != "build" && scope != "runtime" && scope != "both" {
		rejectEnv(http.StatusBadRequest, "invalid_scope", "scope must be one of: build, runtime, both")
		return
	}

	ev, err := h.upsertEnvVar(c.Request.Context(), envID, appName, key, req.Value, req.IsSecret, scope, claims.UserID.String())
	if err != nil {
		rejectEnv(http.StatusInternalServerError, "save_failed", "failed to save env var")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "SetEnvVar",
		ResourceKind:  "EnvVar",
		ResourceName:  appName,
	})

	resp := gin.H{"env_var": ev}
	if warning := connectionStringWarning(key, req.Value); warning != nil {
		resp["warnings"] = []envVarWarning{*warning}
	}
	if op, queued := h.queueEnvApply(c, claims, projectID, envID, appName); queued {
		resp["operation"] = op
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

	rejectEnv := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "SetEnvVar",
			ResourceKind:  "EnvVar",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
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
		rejectEnv(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if len(req.Vars) == 0 {
		rejectEnv(http.StatusBadRequest, "vars_required", "vars is required")
		return
	}
	if len(req.Vars) > 200 {
		rejectEnv(http.StatusBadRequest, "batch_too_large", "at most 200 variables per request")
		return
	}

	seen := make(map[string]struct{}, len(req.Vars))
	var warnings []envVarWarning
	for i := range req.Vars {
		v := &req.Vars[i]
		if !validEnvKey(v.Key) {
			rejectEnv(http.StatusBadRequest, "invalid_key", fmt.Sprintf("invalid key %q: expected letters, digits and underscore, not starting with a digit", v.Key))
			return
		}
		if _, dup := seen[v.Key]; dup {
			rejectEnv(http.StatusBadRequest, "duplicate_key", fmt.Sprintf("duplicate key %q", v.Key))
			return
		}
		seen[v.Key] = struct{}{}
		if len(v.Value) > 4*1024 {
			rejectEnv(http.StatusBadRequest, "value_too_large", fmt.Sprintf("value for %q exceeds 4KiB limit", v.Key))
			return
		}
		if v.Scope == "" {
			v.Scope = "runtime"
		}
		if v.Scope != "build" && v.Scope != "runtime" && v.Scope != "both" {
			rejectEnv(http.StatusBadRequest, "invalid_scope", "scope must be one of: build, runtime, both")
			return
		}
		if warning := connectionStringWarning(v.Key, v.Value); warning != nil {
			warnings = append(warnings, *warning)
		}
	}

	saved := make([]envVar, 0, len(req.Vars))
	for _, v := range req.Vars {
		ev, err := h.upsertEnvVar(c.Request.Context(), envID, appName, v.Key, v.Value, v.IsSecret, v.Scope, claims.UserID.String())
		if err != nil {
			rejectEnv(http.StatusInternalServerError, "save_failed", "failed to save env var")
			return
		}
		saved = append(saved, ev)
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "SetEnvVar",
		ResourceKind:  "EnvVar",
		ResourceName:  appName,
	})

	resp := gin.H{"env_vars": saved}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
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

// envVarWarning is a machine-readable, non-blocking note attached to a save
// response. It exists to survive: an incident where a user pasted the bare
// host from the database page (pg-router.databases.svc.cluster.local)
// straight into DATABASE_URL, we accepted it silently eight times in a row,
// and the app sat in a crash loop for twelve hours before the user gave up.
// The value is not rejected -- a user is entitled to store whatever string
// they want -- but the frontend renders this warning where it cannot be
// missed before the next deploy.
type envVarWarning struct {
	Key     string `json:"key"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// connectionKeySuffixes and connectionKeyExact identify env var keys that are
// conventionally a full connection string (DSN/URL), as opposed to a bare
// hostname or credential fragment.
var connectionKeySuffixes = []string{"_URL", "_DSN", "_CONNECTION_STRING"}

var connectionKeyExact = map[string]bool{
	"DATABASE_URL":      true,
	"DATABASE_DSN":      true,
	"REDIS_URL":         true,
	"MONGO_URL":         true,
	"MONGODB_URL":       true,
	"POSTGRES_URL":      true,
	"POSTGRESQL_URL":    true,
	"MYSQL_URL":         true,
	"AMQP_URL":          true,
	"RABBITMQ_URL":      true,
	"CONNECTION_STRING": true,
}

// hasSchemePrefix matches a leading `scheme://`. It deliberately requires the
// double slash: Go's net/url happily parses "host:5432" as scheme="host",
// opaque="5432" (no slashes needed for an opaque URL), which would call a
// bare host-with-port a valid connection string. Requiring "://" is what
// actually distinguishes "postgresql://host:5432/db" from "host:5432/db" or
// a plain "host".
var hasSchemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// looksLikeConnectionKey reports whether key is a name that conventionally
// holds a full connection string rather than a bare value.
func looksLikeConnectionKey(key string) bool {
	upper := strings.ToUpper(key)
	if connectionKeyExact[upper] {
		return true
	}
	for _, suffix := range connectionKeySuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// connectionStringWarning flags a value that looks like a bare host/DSN
// fragment saved under a connection-string-shaped key. It returns nil for
// empty values (nothing to judge yet), keys unrelated to connections, and
// values that already carry a `scheme://` prefix.
func connectionStringWarning(key, value string) *envVarWarning {
	if value == "" || !looksLikeConnectionKey(key) {
		return nil
	}
	if hasSchemePrefix.MatchString(value) {
		return nil
	}
	return &envVarWarning{
		Key:  key,
		Code: "value_is_not_a_connection_string",
		Message: fmt.Sprintf(
			"%q looks like a bare host or fragment, not a full connection string. "+
				"A connection string usually looks like postgresql://user:password@host:5432/database "+
				"-- copy the full string from the database page instead of a single field.",
			key,
		),
	}
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

	audit := func(outcome string, meta map[string]any) {
		meta["app"] = appName
		meta["key"] = key
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        auditActionRevealEnvVar,
			ResourceKind:  "EnvVar",
			ResourceName:  key,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	if c.Query("reveal") != "true" {
		rejectErr(http.StatusBadRequest, "reveal_flag_missing", "reveal=true is required")
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		audit(auditOutcomeFailure, map[string]any{"reason": "not_a_member", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "membership_check_failed", "failed to check project membership")
		return
	}
	if !canWrite(role) {
		audit(auditOutcomeFailure, map[string]any{"reason": "read_only_role", "status": http.StatusForbidden})
		respondForbidden(c)
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		rejectErr(http.StatusInternalServerError, "env_check_failed", "failed to verify environment")
		return
	} else if !ok {
		audit(auditOutcomeFailure, map[string]any{"reason": "env_not_in_project", "status": http.StatusNotFound})
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
		audit(auditOutcomeFailure, map[string]any{"reason": "var_not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "load_failed", "failed to load env var")
		return
	}

	plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "decrypt_failed", "failed to decrypt value")
		return
	}

	audit(auditOutcomeSuccess, map[string]any{})

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
// @Param       projectId       path     string true  "Project UUID"
// @Param       envId           path     string true  "Environment UUID"
// @Param       appName         path     string true  "App name"
// @Param       key             path     string true  "Variable key"
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

	rejectEnv := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DeleteEnvVar",
			ResourceKind:  "EnvVar",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
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
		rejectEnv(http.StatusInternalServerError, "delete_failed", "failed to delete env var")
		return
	}
	if tag.RowsAffected() == 0 {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DeleteEnvVar",
			ResourceKind:  "EnvVar",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": "not_found", "status": http.StatusNotFound, "key": key},
		})
		respondNotFound(c)
		return
	}

	_, _ = h.queueEnvApply(c, claims, projectID, envID, appName)

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "DeleteEnvVar",
		ResourceKind:  "EnvVar",
		ResourceName:  appName,
		Metadata:      map[string]any{"key": key},
	})

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
