package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
)

func (h *Handler) hasConfiguredAIKeyCredentialPool(ctx context.Context, gatewayKeyID uuid.UUID) (bool, error) {
	var configured bool
	err := h.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM ai_gateway_key_credentials
			 WHERE (gateway_key_id = $1 OR gateway_key_id IS NULL)
			   AND enabled AND deleted_at IS NULL
		)`, gatewayKeyID).Scan(&configured)
	return configured, err
}

type aiKeyCredentialInput struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	APIBase  string `json:"api_base"`
	Label    string `json:"label"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

func normalizeAIKeyCredentialInput(in aiKeyCredentialInput) (aiKeyCredentialInput, error) {
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.APIBase = strings.TrimSpace(in.APIBase)
	in.Label = strings.TrimSpace(in.Label)
	if in.Provider == "" {
		return in, errors.New("provider is required")
	}
	if in.APIKey == "" {
		return in, errors.New("api_key is required")
	}
	if in.Priority != nil && *in.Priority < 0 {
		return in, errors.New("priority must be non-negative")
	}
	return in, nil
}

type aiKeyCredentialListItem struct {
	ID               uuid.UUID  `json:"id"`
	Provider         string     `json:"provider"`
	Label            string     `json:"label"`
	KeyHint          string     `json:"key_hint"`
	APIBase          string     `json:"api_base,omitempty"`
	Enabled          bool       `json:"enabled"`
	Priority         int        `json:"priority"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Status           string     `json:"status"`
	UnavailableUntil *time.Time `json:"unavailable_until,omitempty"`
}

type aiKeyCredentialUpdateRequest struct {
	APIKey   *string `json:"api_key"`
	APIBase  *string `json:"api_base"`
	Label    *string `json:"label"`
	Enabled  *bool   `json:"enabled"`
	Priority *int    `json:"priority"`
}

type createAIKeyCredentialsRequest struct {
	Credentials []aiKeyCredentialInput `json:"credentials" binding:"required,min=1,max=50"`
}

func validateAIKeyCredentialUpdate(req aiKeyCredentialUpdateRequest) error {
	if req.APIKey == nil && req.APIBase == nil && req.Label == nil && req.Enabled == nil && req.Priority == nil {
		return errors.New("at least one field is required")
	}
	if req.APIKey != nil && strings.TrimSpace(*req.APIKey) == "" {
		return errors.New("api_key must not be empty")
	}
	if req.Priority != nil && *req.Priority < 0 {
		return errors.New("priority must be non-negative")
	}
	return nil
}

// authorizeAIKeyCredentialAccess checks project membership and ensures the key
// belongs to that project. Non-members and cross-project key IDs both get 404,
// so key existence is not disclosed across tenants.
func (h *Handler) authorizeAIKeyCredentialAccess(c *gin.Context, write bool) (uuid.UUID, uuid.UUID, bool) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return uuid.Nil, uuid.Nil, false
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	keyID, err := uuid.Parse(c.Param("keyId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return uuid.Nil, uuid.Nil, false
	}
	if write && !canWrite(role) {
		respondForbidden(c)
		return uuid.Nil, uuid.Nil, false
	}
	var exists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM ai_gateway_keys WHERE id = $1 AND project_id = $2 AND revoked_at IS NULL)`,
		keyID, projectID).Scan(&exists); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check ai key")
		return uuid.Nil, uuid.Nil, false
	}
	if !exists {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, keyID, true
}

func (h *Handler) ListAIKeyCredentials(c *gin.Context) {
	_, keyID, ok := h.authorizeAIKeyCredentialAccess(c, false)
	if !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, provider, label, api_base, api_key_encrypted, enabled, priority, created_at, updated_at
		  FROM ai_gateway_key_credentials WHERE gateway_key_id = $1
		 ORDER BY priority, created_at, id`, keyID)
	if err != nil {
		respondError(c, 500, "failed to query credentials")
		return
	}
	defer rows.Close()
	out := []aiKeyCredentialListItem{}
	for rows.Next() {
		var it aiKeyCredentialListItem
		var base *string
		var enc []byte
		if err := rows.Scan(&it.ID, &it.Provider, &it.Label, &base, &enc, &it.Enabled, &it.Priority, &it.CreatedAt, &it.UpdatedAt); err != nil {
			respondError(c, 500, "failed to scan credential")
			return
		}
		if base != nil {
			it.APIBase = *base
		}
		plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, enc)
		if err != nil {
			respondError(c, 500, "failed to decrypt credential hint")
			return
		}
		it.KeyHint = maskAIKey(string(plain))
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"credentials": out})
}

func (h *Handler) CreateAIKeyCredential(c *gin.Context) {
	projectID, keyID, ok := h.authorizeAIKeyCredentialAccess(c, true)
	if !ok {
		return
	}
	var body createAIKeyCredentialsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, 400, "invalid request body")
		return
	}
	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, 500, "store credentials")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	out := make([]aiKeyCredentialListItem, 0, len(body.Credentials))
	discoveries := make([]createdCredentialDiscovery, 0, len(body.Credentials))
	for _, raw := range body.Credentials {
		req, err := normalizeAIKeyCredentialInput(raw)
		if err != nil {
			respondError(c, 400, err.Error())
			return
		}
		enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.APIKey))
		if err != nil {
			respondError(c, 500, "encrypt credential: "+err.Error())
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		priority := 100
		if req.Priority != nil {
			priority = *req.Priority
		}
		var base *string
		if req.APIBase != "" {
			base = &req.APIBase
		}
		var id uuid.UUID
		var createdAt, updatedAt time.Time
		err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO ai_gateway_key_credentials (gateway_key_id, provider, label, api_base, api_key_encrypted, enabled, priority)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at, updated_at`,
			keyID, req.Provider, req.Label, base, enc, enabled, priority).Scan(&id, &createdAt, &updatedAt)
		if err != nil {
			respondError(c, 500, "store credential: "+err.Error())
			return
		}
		out = append(out, aiKeyCredentialListItem{ID: id, Provider: req.Provider, Label: req.Label, KeyHint: maskAIKey(req.APIKey), APIBase: req.APIBase, Enabled: enabled, Priority: priority, CreatedAt: createdAt, UpdatedAt: updatedAt})
		if enabled {
			discoveries = append(discoveries, createdCredentialDiscovery{ID: id, Provider: req.Provider, APIBase: req.APIBase, APIKey: req.APIKey})
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		respondError(c, 500, "store credentials")
		return
	}
	claims, _ := auth.GetClaims(c)
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{ProjectID: projectID, Action: "CreateAIKeyCredentials", ResourceKind: "AIGatewayCredential", ResourceName: keyID.String(), Outcome: auditOutcomeSuccess, Metadata: map[string]any{"gateway_key_id": keyID, "count": len(out)}})
	// Discovery is best effort: storing a valid key must succeed even when its
	// upstream is temporarily unavailable. The bounded probes finish before the
	// response so the immediately rendered UI can show discovered models.
	discoverCreatedCredentials(c.Request.Context(), h.pool, discoveries)
	c.JSON(http.StatusCreated, gin.H{"credentials": out})
}

func (h *Handler) UpdateAIKeyCredential(c *gin.Context) {
	projectID, keyID, ok := h.authorizeAIKeyCredentialAccess(c, true)
	if !ok {
		return
	}
	credentialID, err := uuid.Parse(c.Param("credentialId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	var req aiKeyCredentialUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, 400, "invalid request body")
		return
	}
	if err := validateAIKeyCredentialUpdate(req); err != nil {
		respondError(c, 400, err.Error())
		return
	}
	var enc []byte
	if req.APIKey != nil {
		trimmed := strings.TrimSpace(*req.APIKey)
		enc, err = crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(trimmed))
		if err != nil {
			respondError(c, 500, "encrypt credential: "+err.Error())
			return
		}
	}
	var label, base *string
	if req.Label != nil {
		v := strings.TrimSpace(*req.Label)
		label = &v
	}
	if req.APIBase != nil {
		v := strings.TrimSpace(*req.APIBase)
		base = &v
	}
	tag, err := h.pool.Exec(c.Request.Context(), `
		UPDATE ai_gateway_key_credentials
		   SET api_key_encrypted = CASE WHEN $3::bytea IS NULL THEN api_key_encrypted ELSE $3 END,
		       api_base = CASE WHEN $4::boolean THEN NULLIF($5, '') ELSE api_base END,
		       label = COALESCE($6, label), enabled = COALESCE($7, enabled), priority = COALESCE($8, priority), updated_at = now()
		 WHERE id = $1 AND gateway_key_id = $2`, credentialID, keyID, enc, req.APIBase != nil, base, label, req.Enabled, req.Priority)
	if err != nil {
		respondError(c, 500, "update credential: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	claims, _ := auth.GetClaims(c)
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{ProjectID: projectID, Action: "UpdateAIKeyCredential", ResourceKind: "AIGatewayCredential", ResourceName: credentialID.String(), Outcome: auditOutcomeSuccess, Metadata: map[string]any{"gateway_key_id": keyID}})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) DeleteAIKeyCredential(c *gin.Context) {
	projectID, keyID, ok := h.authorizeAIKeyCredentialAccess(c, true)
	if !ok {
		return
	}
	credentialID, err := uuid.Parse(c.Param("credentialId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(), `DELETE FROM ai_gateway_key_credentials WHERE id = $1 AND gateway_key_id = $2`, credentialID, keyID)
	if err != nil {
		respondError(c, 500, "delete credential: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	claims, _ := auth.GetClaims(c)
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{ProjectID: projectID, Action: "DeleteAIKeyCredential", ResourceKind: "AIGatewayCredential", ResourceName: credentialID.String(), Outcome: auditOutcomeSuccess, Metadata: map[string]any{"gateway_key_id": keyID}})
	c.Status(http.StatusNoContent)
}

type aiCredentialCandidatesRequest struct {
	GatewayKeyID uuid.UUID `json:"gateway_key_id" binding:"required"`
	Provider     string    `json:"provider" binding:"required"`
	Model        string    `json:"model"`
}

type aiCredentialCandidate struct {
	CredentialID uuid.UUID `json:"credential_id"`
	Provider     string    `json:"provider"`
	Label        string    `json:"label,omitempty"`
	APIKey       string    `json:"api_key"`
	APIBase      string    `json:"api_base,omitempty"`
	Priority     int       `json:"priority"`
}

func (h *Handler) AIGetCredentialCandidates(c *gin.Context) {
	var req aiCredentialCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, 400, "invalid request body")
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	model := strings.TrimSpace(req.Model)
	configured, err := h.hasConfiguredAIKeyCredentialPool(c.Request.Context(), req.GatewayKeyID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "check credential pool")
		return
	}
	if !configured {
		// A 404 deliberately means "the new pool contract is not configured".
		// The gateway may then use the pre-pool project/global credential path.
		respondNotFound(c)
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT c.id, c.provider, c.label, c.api_base, c.api_key_encrypted, c.priority
		  FROM ai_gateway_key_credentials c
		 WHERE EXISTS (SELECT 1 FROM ai_gateway_keys k WHERE k.id = $1 AND k.revoked_at IS NULL)
		   AND (c.gateway_key_id = $1 OR c.gateway_key_id IS NULL)
		   AND c.provider = $2 AND c.enabled AND c.deleted_at IS NULL
		   AND (c.unavailable_until IS NULL OR c.unavailable_until <= now())
		   AND ($3 = '' OR EXISTS (SELECT 1 FROM ai_gateway_key_credential_models m WHERE m.credential_id = c.id AND m.model_id = $3))
		 ORDER BY (c.gateway_key_id IS NULL), c.priority, c.created_at, c.id`, req.GatewayKeyID, req.Provider, model)
	if err != nil {
		respondError(c, 500, "load credential candidates: "+err.Error())
		return
	}
	defer rows.Close()
	out := []aiCredentialCandidate{}
	for rows.Next() {
		var it aiCredentialCandidate
		var base *string
		var enc []byte
		if err := rows.Scan(&it.CredentialID, &it.Provider, &it.Label, &base, &enc, &it.Priority); err != nil {
			respondError(c, 500, "scan credential candidate")
			return
		}
		plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, enc)
		if err != nil {
			respondError(c, 500, "decrypt credential candidate")
			return
		}
		it.APIKey = string(plain)
		if base != nil {
			it.APIBase = *base
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"candidates": out})
}

type aiCredentialModelsReportRequest struct {
	CredentialID uuid.UUID `json:"credential_id" binding:"required"`
	Models       []string  `json:"models" binding:"required"`
}

// AIReportCredentialModels atomically replaces one credential's secret-free
// discovery snapshot. The gateway calls it only after a successful upstream
// /models probe; failures leave the previous bounded-stale snapshot intact.
func (h *Handler) AIReportCredentialModels(c *gin.Context) {
	var req aiCredentialModelsReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	models, err := normalizeDiscoveredModels(req.Models)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := replaceCredentialModels(c.Request.Context(), h.pool, req.CredentialID, models); err != nil {
		if err.Error() == "credential not found" {
			respondNotFound(c)
			return
		}
		respondError(c, http.StatusInternalServerError, "store model discovery")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "model_count": len(models)})
}

type aiKeyModelListItem struct {
	ID            string      `json:"id"`
	Provider      string      `json:"provider,omitempty"`
	CredentialIDs []uuid.UUID `json:"credential_ids"`
	DiscoveredAt  time.Time   `json:"discovered_at"`
}

type aiKeyPublicModelListItem struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`
}

// @ID listAIKeyModels
// @Summary List discovered models for one AI Gateway key
// @Tags ai-gateway
// @Security BearerAuth
// @Produce json
// @Router /projects/{projectId}/ai/keys/{keyId}/models [get]
func (h *Handler) ListAIKeyModels(c *gin.Context) {
	_, keyID, ok := h.authorizeAIKeyCredentialAccess(c, false)
	if !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT m.model_id,
		       CASE WHEN count(DISTINCT c.provider) = 1 THEN min(c.provider) ELSE '' END,
		       array_agg(DISTINCT c.id ORDER BY c.id), max(m.discovered_at)
		  FROM ai_gateway_key_credential_models m
		  JOIN ai_gateway_key_credentials c ON c.id = m.credential_id
		 WHERE (c.gateway_key_id = $1 OR c.gateway_key_id IS NULL) AND c.enabled AND c.deleted_at IS NULL
		   AND (c.unavailable_until IS NULL OR c.unavailable_until <= now())
		 GROUP BY m.model_id ORDER BY m.model_id`, keyID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query discovered models")
		return
	}
	defer rows.Close()
	out := []aiKeyPublicModelListItem{}
	for rows.Next() {
		var it aiKeyPublicModelListItem
		var internalCredentialIDs []uuid.UUID
		if err := rows.Scan(&it.ID, &it.Provider, &internalCredentialIDs, &it.DiscoveredAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan discovered model")
			return
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

type aiKeyModelsRequest struct {
	GatewayKeyID uuid.UUID `json:"gateway_key_id" binding:"required"`
}

// AIGetKeyModels is the internal-token counterpart of ListAIKeyModels used by
// the data plane. It intentionally has no end-user JWT or project parameter.
func (h *Handler) AIGetKeyModels(c *gin.Context) {
	var req aiKeyModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	configured, err := h.hasConfiguredAIKeyCredentialPool(c.Request.Context(), req.GatewayKeyID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "check credential pool")
		return
	}
	if !configured {
		respondNotFound(c)
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT m.model_id,
		       CASE WHEN count(DISTINCT c.provider) = 1 THEN min(c.provider) ELSE '' END,
		       array_agg(DISTINCT c.id ORDER BY c.id), max(m.discovered_at)
		  FROM ai_gateway_key_credential_models m
		  JOIN ai_gateway_key_credentials c ON c.id = m.credential_id
		 WHERE (c.gateway_key_id = $1 OR c.gateway_key_id IS NULL) AND c.enabled AND c.deleted_at IS NULL
		   AND (c.unavailable_until IS NULL OR c.unavailable_until <= now())
		   AND EXISTS (SELECT 1 FROM ai_gateway_keys active_key WHERE active_key.id = $1 AND active_key.revoked_at IS NULL)
		 GROUP BY m.model_id ORDER BY m.model_id`, req.GatewayKeyID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query discovered models")
		return
	}
	defer rows.Close()
	out := []aiKeyModelListItem{}
	for rows.Next() {
		var it aiKeyModelListItem
		if err := rows.Scan(&it.ID, &it.Provider, &it.CredentialIDs, &it.DiscoveredAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan discovered model")
			return
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}
