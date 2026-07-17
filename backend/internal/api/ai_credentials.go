package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/crypto"
)

type aiSetCredentialRequest struct {
	ProjectID uuid.UUID `json:"project_id" binding:"required"`
	Provider  string    `json:"provider" binding:"required"`
	APIKey    string    `json:"api_key" binding:"required"`
	APIBase   string    `json:"api_base"`
}

type aiGetCredentialRequest struct {
	ProjectID uuid.UUID `json:"project_id" binding:"required"`
	Provider  string    `json:"provider" binding:"required"`
}

// AISetProviderCredential upserts a project-scoped provider (BYOK) credential,
// encrypting the api_key with the platform GITOPS_ENCRYPTION_KEY. Server-to-server
// only (the AI Gateway control path); never exposed to end-user JWT traffic.
//
// POST /internal/ai/credential/set  (guarded by requireInternalToken)
func (h *Handler) AISetProviderCredential(c *gin.Context) {
	var req aiSetCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.APIKey))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "encrypt credential: "+err.Error())
		return
	}

	var apiBase *string
	if req.APIBase != "" {
		apiBase = &req.APIBase
	}

	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO ai_provider_credentials (project_id, provider, api_base, api_key_encrypted)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, provider) DO UPDATE
		   SET api_base          = EXCLUDED.api_base,
		       api_key_encrypted = EXCLUDED.api_key_encrypted,
		       updated_at        = NOW()
	`, req.ProjectID, req.Provider, apiBase, enc); err != nil {
		respondError(c, http.StatusInternalServerError, "store credential: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// AIGetProviderCredential returns the decrypted project-scoped provider credential
// for the AI Gateway runtime to inject per request. Server-to-server only; the
// plaintext key leaves the backend only over the internal-token-guarded channel.
//
// POST /internal/ai/credential/get  (guarded by requireInternalToken)
func (h *Handler) AIGetProviderCredential(c *gin.Context) {
	var req aiGetCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	var enc []byte
	var apiBase *string
	err := h.pool.QueryRow(c.Request.Context(), `
		SELECT api_key_encrypted, api_base
		  FROM ai_provider_credentials
		 WHERE project_id = $1 AND provider = $2
	`, req.ProjectID, req.Provider).Scan(&enc, &apiBase)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(c, http.StatusNotFound, "no credential for project/provider")
			return
		}
		respondError(c, http.StatusInternalServerError, "load credential: "+err.Error())
		return
	}

	plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, enc)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "decrypt credential: "+err.Error())
		return
	}

	resp := gin.H{"api_key": string(plain)}
	if apiBase != nil {
		resp["api_base"] = *apiBase
	}
	c.JSON(http.StatusOK, resp)
}
