package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// deployTokenPrefix marks a deploy-hook token plaintext (and lets one string
// pattern-match it apart from any other credential type the platform mints).
const deployTokenPrefix = "dadadh_"

// deployTokenPrefixLen is how many leading plaintext characters are persisted
// as token_prefix -- deployTokenPrefix itself plus 6 hex chars, enough for a
// caller to tell hooks apart in a list without revealing the secret.
const deployTokenPrefixLen = len(deployTokenPrefix) + 6

// systemDeployActorID is the fixed system-user id (migration
// 010_system_user.sql) used as operations/audit_events actor_id when a
// deploy-hook's creator has since been deleted (created_by went NULL via
// ON DELETE SET NULL).
var systemDeployActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// generateDeployToken mints a new plaintext deploy-hook token plus its derived
// hash and prefix. The plaintext is deployTokenPrefix followed by 40 hex
// characters (20 random bytes) -- returned to the caller exactly once; only
// hash and prefix are ever persisted.
func generateDeployToken() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 20)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = deployTokenPrefix + hex.EncodeToString(buf)
	hash = hashDeployToken(plaintext)
	prefix = plaintext[:deployTokenPrefixLen]
	return plaintext, hash, prefix, nil
}

// hashDeployToken returns the hex-encoded sha256 of a deploy-hook token
// plaintext -- the only form ever persisted or compared against.
func hashDeployToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// classifyOperationStatus reports whether an operation status is terminal, and
// when terminal, whether it represents success -- as seen by a deploy-hook
// caller polling GetDeployOperation.
//
// A DeployImageVersion operation SUCCEEDS at "Committed": gitops-agent's
// doDeployImageVersion ends by writing the new image manifests to the gitops
// git repo (db.MarkCommitted) and nothing thereafter advances the operation
// row -- Argo/k8s rollout health is reconciled onto resource_snapshots
// (statusreconciler.go), not back onto operations. "Ready" is only reached by
// operation types with a dedicated reconcile-to-Ready path (e.g. db backups,
// db_backups_reconcile.go), so it is accepted as success too but a deploy will
// not normally reach it. "Committed" therefore is the terminal-success state
// the Action's wait poll must stop on, or a green deploy would poll forever.
// Pure so it is unit tested directly.
func classifyOperationStatus(status models.OperationStatus) (terminal, success bool) {
	switch status {
	case models.OperationStatusCommitted, models.OperationStatusReady:
		return true, true
	case models.OperationStatusFailed, models.OperationStatusCancelled:
		return true, false
	default:
		return false, false
	}
}

// extractDeployToken reads the caller's deploy-hook token from the request,
// preferring X-Dada-Deploy-Token and falling back to a standard bearer
// Authorization header. Returns "" when neither is present. Mirrors the
// trim-and-compare idiom in dadaAgentWebhook: a non-"Bearer "-prefixed
// Authorization header is treated as absent, not as a literal token.
func extractDeployToken(c *gin.Context) string {
	if tok := c.GetHeader("X-Dada-Deploy-Token"); tok != "" {
		return tok
	}
	header := c.GetHeader("Authorization")
	raw := strings.TrimPrefix(header, "Bearer ")
	if raw == "" || raw == header {
		return ""
	}
	return raw
}

// resolvedDeployHook is the subset of an app_deploy_hooks row needed to
// authenticate and scope a token-authenticated /api/v1/deploy* request.
type resolvedDeployHook struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	CreatedBy     *uuid.UUID
}

// deployHookFromToken resolves the bearer/X-Dada-Deploy-Token credential on a
// consumption-endpoint request to its live (non-revoked) app_deploy_hooks row.
// On failure it has already written the response (401 for a missing/unknown/
// revoked token, 500 on a DB error) and returns ok=false.
func (h *Handler) deployHookFromToken(c *gin.Context) (resolvedDeployHook, bool) {
	raw := extractDeployToken(c)
	if raw == "" {
		respondUnauthorized(c)
		return resolvedDeployHook{}, false
	}

	var hook resolvedDeployHook
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, project_id, environment_id, app_name, created_by
		 FROM app_deploy_hooks WHERE token_hash = $1 AND revoked_at IS NULL`,
		hashDeployToken(raw),
	).Scan(&hook.ID, &hook.ProjectID, &hook.EnvironmentID, &hook.AppName, &hook.CreatedBy)
	if err == pgx.ErrNoRows {
		respondUnauthorized(c)
		return resolvedDeployHook{}, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve deploy token")
		return resolvedDeployHook{}, false
	}
	return hook, true
}

// scanDeployHook scans a full app_deploy_hooks row into an AppDeployHook.
func scanDeployHook(scanner interface {
	Scan(dest ...any) error
}, hook *models.AppDeployHook) error {
	return scanner.Scan(
		&hook.ID, &hook.ProjectID, &hook.EnvironmentID, &hook.AppName, &hook.Name,
		&hook.TokenHash, &hook.TokenPrefix, &hook.CreatedBy,
		&hook.CreatedAt, &hook.LastUsedAt, &hook.RevokedAt,
	)
}

type createDeployHookRequest struct {
	Name string `json:"name"`
}

// createDeployHookResponse is returned exactly once, at creation time. Token
// is the plaintext deploy-hook credential -- it is never stored and cannot be
// retrieved again after this response.
type createDeployHookResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Token       string    `json:"token"`
	TokenPrefix string    `json:"token_prefix"`
	BaseURL     string    `json:"base_url"`
	DeployURL   string    `json:"deploy_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateDeployHook mints a new revocable bearer token that external CI (e.g. a
// GitHub Actions workflow) can present to POST /api/v1/deploy instead of a
// Keycloak session, to deploy a prebuilt image to this app.
//
// @ID          createDeployHook
// @Summary     Create a deploy-hook token for an app
// @Description Mints a revocable bearer token scoped to one app. External CI presents it to POST /api/v1/deploy (as "Authorization: Bearer <token>" or "X-Dada-Deploy-Token: <token>") to trigger the same deploy as PATCH .../apps/{appName}/image, without a Keycloak session. The plaintext token is returned ONLY in this response -- store it now, it cannot be retrieved again.
// @Tags        deploy-hook
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       envId     path     string                  true "Environment UUID"
// @Param       appName   path     string                  true "App name"
// @Param       body      body     createDeployHookRequest false "Optional label for the hook"
// @Success     201       {object} createDeployHookResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/deploy-hooks [post]
func (h *Handler) CreateDeployHook(c *gin.Context) {
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

	reject := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateDeployHook",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respond()
	}

	var req createDeployHookRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		reject(http.StatusBadRequest, "malformed_body", func() {
			respondError(c, http.StatusBadRequest, err.Error())
		})
		return
	}

	var count int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count)
	if err != nil {
		reject(http.StatusInternalServerError, "app_check_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to check app existence")
		})
		return
	}
	if count == 0 {
		reject(http.StatusNotFound, "app_not_found", func() { respondNotFound(c) })
		return
	}

	plaintext, hash, prefix, err := generateDeployToken()
	if err != nil {
		reject(http.StatusInternalServerError, "token_generate_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to generate token")
		})
		return
	}

	var hook models.AppDeployHook
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO app_deploy_hooks (project_id, environment_id, app_name, name, token_hash, token_prefix, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, project_id, environment_id, app_name, name, token_hash, token_prefix,
		           created_by, created_at, last_used_at, revoked_at`,
		projectID, envID, appName, req.Name, hash, prefix, claims.UserID,
	)
	if err := scanDeployHook(row, &hook); err != nil {
		reject(http.StatusInternalServerError, "hook_insert_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to create deploy hook")
		})
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "CreateDeployHook",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"name": hook.Name, "token_prefix": hook.TokenPrefix},
	})
	h.notifyDeployHook(projectID, "CreateDeployHook", appName, actorLabelFromClaims(claims))

	baseURL := strings.TrimRight(h.cfg.PublicBaseURL, "/")
	c.JSON(http.StatusCreated, createDeployHookResponse{
		ID:          hook.ID,
		Name:        hook.Name,
		Token:       plaintext,
		TokenPrefix: hook.TokenPrefix,
		BaseURL:     baseURL,
		DeployURL:   baseURL + "/api/v1/deploy",
		CreatedAt:   hook.CreatedAt,
	})
}

// deployHookListItem is the safe (secret-free) shape returned by ListDeployHooks.
type deployHookListItem struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// ListDeployHooks lists the deploy-hook tokens created for an app, including
// revoked ones (see RevokedAt). The plaintext token and its hash are never
// returned here or anywhere after creation.
//
// @ID          listDeployHooks
// @Summary     List deploy-hook tokens for an app
// @Description Lists deploy-hook tokens created for this app (including revoked ones). Never returns the plaintext token or its hash -- only the id, label, token_prefix and lifecycle timestamps.
// @Tags        deploy-hook
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with deploy_hooks array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/deploy-hooks [get]
func (h *Handler) ListDeployHooks(c *gin.Context) {
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

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, name, token_prefix, created_at, last_used_at, revoked_at
		 FROM app_deploy_hooks
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3
		 ORDER BY created_at DESC`,
		projectID, envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query deploy hooks")
		return
	}
	defer rows.Close()

	out := []deployHookListItem{}
	for rows.Next() {
		var it deployHookListItem
		if err := rows.Scan(&it.ID, &it.Name, &it.TokenPrefix, &it.CreatedAt, &it.LastUsedAt, &it.RevokedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan deploy hook")
			return
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"deploy_hooks": out})
}

// DeleteDeployHook revokes a deploy-hook token. Revocation is permanent: the
// token immediately stops authenticating (deployHookFromToken filters on
// revoked_at IS NULL); the row is kept for audit history rather than deleted.
//
// @ID          deleteDeployHook
// @Summary     Revoke a deploy-hook token
// @Description Revokes a deploy-hook token. The token stops working immediately; already-queued deploys it triggered are unaffected.
// @Tags        deploy-hook
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       envId     path string true "Environment UUID"
// @Param       appName   path string true "App name"
// @Param       hookId    path string true "Deploy-hook UUID"
// @Success     204       "no content"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/deploy-hooks/{hookId} [delete]
func (h *Handler) DeleteDeployHook(c *gin.Context) {
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
	hookID, err := uuid.Parse(c.Param("hookId"))
	if err != nil {
		respondNotFound(c)
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

	rejectRevoke := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "RevokeDeployHook",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status, "hook_id": hookID},
		})
		respond()
	}

	var tokenPrefix string
	err = h.pool.QueryRow(c.Request.Context(),
		`UPDATE app_deploy_hooks SET revoked_at = now()
		 WHERE id = $1 AND project_id = $2 AND environment_id = $3 AND app_name = $4 AND revoked_at IS NULL
		 RETURNING token_prefix`,
		hookID, projectID, envID, appName,
	).Scan(&tokenPrefix)
	if err == pgx.ErrNoRows {
		rejectRevoke(http.StatusNotFound, "hook_not_found_or_revoked", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		rejectRevoke(http.StatusInternalServerError, "revoke_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to revoke deploy hook")
		})
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "RevokeDeployHook",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"hook_id": hookID, "token_prefix": tokenPrefix},
	})
	h.notifyDeployHook(projectID, "RevokeDeployHook", appName, actorLabelFromClaims(claims))

	c.Status(http.StatusNoContent)
}

type deployTriggerRequest struct {
	Image string `json:"image"`
}

type deployTriggerResponse struct {
	OperationID uuid.UUID `json:"operation_id"`
	Message     string    `json:"message"`
}

// DeployTrigger deploys a new image version to the app bound to the presented
// deploy-hook token. It is the token-authenticated sibling of UpdateAppImage
// (PATCH .../apps/{appName}/image): same DeployImageVersion operation and the
// same gitops-agent rollout path, just reached over a different credential so
// external CI can call it without a Keycloak session.
//
// @ID          deployTrigger
// @Summary     Trigger a deploy with a deploy-hook token
// @Description Authenticates with a deploy-hook token instead of a Keycloak session: pass it as "Authorization: Bearer <token>" or "X-Dada-Deploy-Token: <token>". The token is scoped to one app (minted via POST .../deploy-hooks) and enqueues the same DeployImageVersion operation PATCH .../apps/{appName}/image does. Asynchronous: returns 202 with the operation id; poll GET /api/v1/deploy/operations/{operationId} (same token) until terminal.
// @Tags        deploy-hook
// @Accept      json
// @Produce     json
// @Param       body body     deployTriggerRequest true "New image reference"
// @Success     202  {object} deployTriggerResponse
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Router      /deploy [post]
func (h *Handler) DeployTrigger(c *gin.Context) {
	hook, ok := h.deployHookFromToken(c)
	if !ok {
		return
	}

	var req deployTriggerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Image == "" {
		respondError(c, http.StatusBadRequest, "image is required")
		return
	}
	if err := ValidateImage(req.Image); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	var count int
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		hook.ProjectID, hook.EnvironmentID, hook.AppName,
	).Scan(&count)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	actorID := systemDeployActorID
	if hook.CreatedBy != nil {
		actorID = *hook.CreatedBy
	}

	payload := models.DeployImageVersionPayload{AppName: hook.AppName, Image: req.Image}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		actorID, hook.ProjectID, hook.EnvironmentID, hook.AppName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), actorID, auditEntry{
		ProjectID:     hook.ProjectID,
		EnvironmentID: hook.EnvironmentID,
		OperationID:   op.ID,
		Action:        "DeployImageVersion",
		ResourceKind:  "App",
		ResourceName:  hook.AppName,
		Metadata:      payload,
	})
	h.notifyDeployHook(hook.ProjectID, "DeployImageVersion", hook.AppName, "CI (deploy-hook)")

	_, _ = h.pool.Exec(c.Request.Context(),
		`UPDATE app_deploy_hooks SET last_used_at = now() WHERE id = $1`, hook.ID,
	)

	c.JSON(http.StatusAccepted, deployTriggerResponse{OperationID: op.ID, Message: "Deploy queued"})
}

// deployOperationStatusResponse is the token-authenticated poll response for
// GetDeployOperation -- deliberately narrower than the full Operation object
// GetOperation (JWT-authenticated) returns, since the caller here is a CI
// script rather than the console UI.
type deployOperationStatusResponse struct {
	Status       string `json:"status"`
	Terminal     bool   `json:"terminal"`
	OK           bool   `json:"ok"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// GetDeployOperation polls the state of an operation enqueued via DeployTrigger,
// authenticated by the same deploy-hook token. The operation must belong to the
// token's own project/environment/app -- an id from a different app's operation
// is rejected as 404 (not 403) so a token cannot be used to enumerate other
// apps' operation ids.
//
// @ID          getDeployOperation
// @Summary     Poll a deploy triggered with a deploy-hook token
// @Description Authenticates with the same deploy-hook token as POST /api/v1/deploy. Returns a compact status view; poll until terminal=true. The operation must belong to the token's own app -- any other operation id is rejected as 404.
// @Tags        deploy-hook
// @Produce     json
// @Param       operationId path     string true "Operation UUID"
// @Success     200         {object} deployOperationStatusResponse
// @Failure     401         {object} map[string]string
// @Failure     404         {object} map[string]string
// @Router      /deploy/operations/{operationId} [get]
func (h *Handler) GetDeployOperation(c *gin.Context) {
	hook, ok := h.deployHookFromToken(c)
	if !ok {
		return
	}
	operationID, err := uuid.Parse(c.Param("operationId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		        status, payload, validation_result, git_commit, git_path, argo_application,
		        error_code, error_message, created_at, updated_at
		 FROM operations
		 WHERE id = $1 AND project_id = $2 AND environment_id = $3 AND resource_name = $4
		       AND resource_kind = 'App' AND action = 'DeployImageVersion'`,
		operationID, hook.ProjectID, hook.EnvironmentID, hook.AppName,
	)
	if err := scanOperation(row, &op); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch operation")
		return
	}

	terminal, success := classifyOperationStatus(op.Status)
	c.JSON(http.StatusOK, deployOperationStatusResponse{
		Status:       string(op.Status),
		Terminal:     terminal,
		OK:           success,
		ErrorCode:    op.ErrorCode,
		ErrorMessage: op.ErrorMessage,
	})
}
