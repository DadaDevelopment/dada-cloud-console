package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
)

type createAdminAICredentialsRequest struct {
	Provider    string                 `json:"provider"`
	Label       string                 `json:"label"`
	APIBase     string                 `json:"api_base"`
	APIKeys     []string               `json:"api_keys"`
	Credentials []aiKeyCredentialInput `json:"credentials"`
}

func validateAdminAIProvider(provider string) error {
	if !isKnownAIProvider(strings.ToLower(strings.TrimSpace(provider))) {
		return errors.New("unknown provider")
	}
	return nil
}

func requireAIPlatformAdmin(c *gin.Context) (*auth.Claims, bool) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return nil, false
	}
	if !isGod(claims) {
		respondForbidden(c)
		return nil, false
	}
	return claims, true
}

// @ID listAdminAICredentials
// @Summary List global AI upstream credentials (platform-admin only)
// @Tags admin,ai-gateway
// @Security BearerAuth
// @Produce json
// @Router /admin/ai-gateway/credentials [get]
func (h *Handler) ListAdminAICredentials(c *gin.Context) {
	if _, ok := requireAIPlatformAdmin(c); !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, provider, label, api_base, api_key_encrypted, enabled, priority, created_at, updated_at,
		       status, unavailable_until
		FROM ai_gateway_key_credentials WHERE gateway_key_id IS NULL AND deleted_at IS NULL
		ORDER BY priority, created_at, id`)
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
		if err := rows.Scan(&it.ID, &it.Provider, &it.Label, &base, &enc, &it.Enabled, &it.Priority, &it.CreatedAt, &it.UpdatedAt, &it.Status, &it.UnavailableUntil); err != nil {
			respondError(c, 500, "failed to scan credential")
			return
		}
		plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, enc)
		if err != nil {
			respondError(c, 500, "failed to decrypt credential hint")
			return
		}
		it.KeyHint = maskAIKey(string(plain))
		if base != nil {
			it.APIBase = *base
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"credentials": out})
}

// @ID createAdminAICredentials
// @Summary Add credentials to the global AI upstream pool (platform-admin only)
// @Tags admin,ai-gateway
// @Security BearerAuth
// @Accept json
// @Produce json
// @Router /admin/ai-gateway/credentials [post]
func (h *Handler) CreateAdminAICredentials(c *gin.Context) {
	claims, ok := requireAIPlatformAdmin(c)
	if !ok {
		return
	}
	var body createAdminAICredentialsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, 400, "invalid request body")
		return
	}
	inputs := body.Credentials
	for i, key := range body.APIKeys {
		label := strings.TrimSpace(body.Label)
		if len(body.APIKeys) > 1 && label != "" {
			label += " " + fmt.Sprint(i+1)
		}
		inputs = append(inputs, aiKeyCredentialInput{Provider: body.Provider, APIKey: key, APIBase: body.APIBase, Label: label})
	}
	if len(inputs) == 0 || len(inputs) > 50 {
		respondError(c, http.StatusBadRequest, "one to 50 credentials are required")
		return
	}
	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, 500, "store credentials")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	out := make([]aiKeyCredentialListItem, 0, len(inputs))
	discoveries := make([]createdCredentialDiscovery, 0, len(inputs))
	for _, raw := range inputs {
		req, err := normalizeAIKeyCredentialInput(raw)
		if err != nil {
			respondError(c, 400, err.Error())
			return
		}
		if err := validateAdminAIProvider(req.Provider); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
		if req.APIBase != "" {
			if err := validateAIAPIBase(c.Request.Context(), req.APIBase); err != nil {
				respondError(c, http.StatusBadRequest, err.Error())
				return
			}
		}
		enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.APIKey))
		if err != nil {
			respondError(c, 500, "encrypt credential")
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
		if err := tx.QueryRow(c.Request.Context(), `INSERT INTO ai_gateway_key_credentials
			(gateway_key_id, provider, label, api_base, api_key_encrypted, enabled, priority)
			VALUES (NULL,$1,$2,$3,$4,$5,$6) RETURNING id,created_at,updated_at`,
			req.Provider, req.Label, base, enc, enabled, priority).Scan(&id, &createdAt, &updatedAt); err != nil {
			respondError(c, 500, "store credential")
			return
		}
		out = append(out, aiKeyCredentialListItem{ID: id, Provider: req.Provider, Label: req.Label, KeyHint: maskAIKey(req.APIKey), APIBase: req.APIBase, Enabled: enabled, Priority: priority, CreatedAt: createdAt, UpdatedAt: updatedAt})
		out[len(out)-1].Status = "healthy"
		if enabled {
			discoveries = append(discoveries, createdCredentialDiscovery{ID: id, Provider: req.Provider, APIBase: req.APIBase, APIKey: req.APIKey})
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		respondError(c, 500, "store credentials")
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{Action: "CreateGlobalAICredentials", ResourceKind: "AIGatewayCredential", ResourceName: "global", Outcome: auditOutcomeSuccess, Metadata: map[string]any{"count": len(out)}})
	discoverCreatedCredentials(c.Request.Context(), h.pool, discoveries)
	c.JSON(http.StatusCreated, gin.H{"credentials": out})
}

type adminAIModelStat struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider,omitempty"`
	CredentialCount int64     `json:"credential_count"`
	DiscoveredAt    time.Time `json:"discovered_at"`
}

// @ID listAdminAIModelStats
// @Summary List models available in the global AI upstream pool
// @Tags admin,ai-gateway
// @Security BearerAuth
// @Produce json
// @Router /admin/ai-gateway/models/stats [get]
func (h *Handler) ListAdminAIModelStats(c *gin.Context) {
	if _, ok := requireAIPlatformAdmin(c); !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `SELECT m.model_id,
		CASE WHEN count(DISTINCT c.provider)=1 THEN min(c.provider) ELSE '' END,
		count(DISTINCT c.id), max(m.discovered_at)
		FROM ai_gateway_key_credential_models m JOIN ai_gateway_key_credentials c ON c.id=m.credential_id
		WHERE c.gateway_key_id IS NULL AND c.enabled AND c.deleted_at IS NULL
		  AND (c.unavailable_until IS NULL OR c.unavailable_until <= now())
		GROUP BY m.model_id ORDER BY m.model_id`)
	if err != nil {
		respondError(c, 500, "failed to query model stats")
		return
	}
	defer rows.Close()
	out := []adminAIModelStat{}
	for rows.Next() {
		var it adminAIModelStat
		if err := rows.Scan(&it.ID, &it.Provider, &it.CredentialCount, &it.DiscoveredAt); err != nil {
			respondError(c, 500, "failed to scan model stats")
			return
		}
		out = append(out, it)
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

// @ID updateAdminAICredential
// @Summary Update a global AI upstream credential (platform-admin only)
// @Tags admin,ai-gateway
// @Security BearerAuth
// @Accept json
// @Produce json
// @Router /admin/ai-gateway/credentials/{credentialId} [patch]
func (h *Handler) UpdateAdminAICredential(c *gin.Context) {
	claims, ok := requireAIPlatformAdmin(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("credentialId"))
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
	if req.APIBase != nil && strings.TrimSpace(*req.APIBase) != "" {
		if err := validateAIAPIBase(c.Request.Context(), strings.TrimSpace(*req.APIBase)); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	var enc []byte
	if req.APIKey != nil {
		enc, err = crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(strings.TrimSpace(*req.APIKey)))
		if err != nil {
			respondError(c, 500, "encrypt credential")
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
	var provider string
	var currentBase *string
	var currentEnc []byte
	var currentEnabled bool
	err = h.pool.QueryRow(c.Request.Context(), `UPDATE ai_gateway_key_credentials SET
		api_key_encrypted=CASE WHEN $2::bytea IS NULL THEN api_key_encrypted ELSE $2 END,
		api_base=CASE WHEN $3::boolean THEN NULLIF($4,'') ELSE api_base END,
		label=COALESCE($5,label),enabled=COALESCE($6,enabled),priority=COALESCE($7,priority),updated_at=now()
		WHERE id=$1 AND gateway_key_id IS NULL AND deleted_at IS NULL
		RETURNING provider,api_base,api_key_encrypted,enabled`, id, enc, req.APIBase != nil, base, label, req.Enabled, req.Priority).
		Scan(&provider, &currentBase, &currentEnc, &currentEnabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondNotFound(c)
			return
		}
		respondError(c, 500, "update credential")
		return
	}
	shouldRediscover := currentEnabled && (req.APIKey != nil || req.APIBase != nil || (req.Enabled != nil && *req.Enabled))
	if shouldRediscover {
		plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, currentEnc)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "refresh credential discovery")
			return
		}
		apiBase := ""
		if currentBase != nil {
			apiBase = *currentBase
		}
		refreshCredentialDiscovery(c.Request.Context(), h.pool, createdCredentialDiscovery{ID: id, Provider: provider, APIBase: apiBase, APIKey: string(plain)})
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{Action: "UpdateGlobalAICredential", ResourceKind: "AIGatewayCredential", ResourceName: id.String(), Outcome: auditOutcomeSuccess})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// @ID deleteAdminAICredential
// @Summary Delete a global AI upstream credential (platform-admin only)
// @Tags admin,ai-gateway
// @Security BearerAuth
// @Router /admin/ai-gateway/credentials/{credentialId} [delete]
func (h *Handler) DeleteAdminAICredential(c *gin.Context) {
	claims, ok := requireAIPlatformAdmin(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("credentialId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(), `UPDATE ai_gateway_key_credentials
		SET enabled=false,deleted_at=now(),updated_at=now()
		WHERE id=$1 AND gateway_key_id IS NULL AND deleted_at IS NULL`, id)
	if err != nil {
		respondError(c, 500, "delete credential")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{Action: "DeleteGlobalAICredential", ResourceKind: "AIGatewayCredential", ResourceName: id.String(), Outcome: auditOutcomeSuccess})
	c.Status(http.StatusNoContent)
}
