package api

import (
	"encoding/json"
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

// aiCredentialListItem is the secret-free shape returned to the console: which
// providers this project has a key for, when it was last changed, and a hint
// short enough to recognise a key by without being usable.
type aiCredentialListItem struct {
	Provider  string    `json:"provider"`
	KeyHint   string    `json:"key_hint"`
	APIBase   string    `json:"api_base,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// maskAIKey renders a provider key as a recognisable, unusable hint: the first
// four and last four characters. Anything short enough that those would overlap
// is reported as all-stars rather than leaked whole.
func maskAIKey(key string) string {
	if len(key) < 12 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// ListAIProviderCredentials lists which providers a project has a BYOK
// credential for. Any project member may read it -- the response carries no
// usable secret, and hiding which providers are configured from a developer who
// is about to debug a 'no credential for project/provider' error helps nobody.
//
// @ID          listAIProviderCredentials
// @Summary     List a project's AI provider credentials
// @Description Lists the providers this project holds a BYOK credential for. Never returns a usable key -- only a masked hint, the optional api_base override and the last update time.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with credentials array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/credentials [get]
func (h *Handler) ListAIProviderCredentials(c *gin.Context) {
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
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT provider, api_base, api_key_encrypted, updated_at
		   FROM ai_provider_credentials
		  WHERE project_id = $1
		  ORDER BY provider`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query credentials")
		return
	}
	defer rows.Close()

	out := []aiCredentialListItem{}
	for rows.Next() {
		var it aiCredentialListItem
		var apiBase *string
		var enc []byte
		if err := rows.Scan(&it.Provider, &apiBase, &enc, &it.UpdatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan credential")
			return
		}
		if apiBase != nil {
			it.APIBase = *apiBase
		}
		if plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, enc); err == nil {
			it.KeyHint = maskAIKey(string(plain))
		}
		out = append(out, it)
	}

	c.JSON(http.StatusOK, gin.H{"credentials": out})
}

type putAICredentialRequest struct {
	APIKey  string `json:"api_key" binding:"required"`
	APIBase string `json:"api_base"`
}

// PutAIProviderCredential stores or replaces the project's BYOK key for one
// provider. This is the user-facing half of AISetProviderCredential: same table
// and same encryption, reached with a project role instead of the internal
// token.
//
// @ID          putAIProviderCredential
// @Summary     Store a project's AI provider credential
// @Description Stores (or replaces) this project's own provider key, encrypted at rest. The AI Gateway injects it server-side for every request the project's AI keys make against a model of that provider; the key is never returned to the browser afterwards.
// @Tags        ai-gateway
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       provider  path     string                 true "Provider name from the catalog"
// @Param       body      body     putAICredentialRequest true "Provider key and optional api_base override"
// @Success     200       {object} map[string]string
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/credentials/{provider} [put]
func (h *Handler) PutAIProviderCredential(c *gin.Context) {
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
	provider := c.Param("provider")
	if !isKnownAIProvider(provider) {
		respondError(c, http.StatusBadRequest, "unknown provider")
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

	var req putAICredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		respondError(c, http.StatusBadRequest, "api_key is required")
		return
	}

	enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(apiKey))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "encrypt credential: "+err.Error())
		return
	}

	var apiBase *string
	if base := strings.TrimSpace(req.APIBase); base != "" {
		apiBase = &base
	}

	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO ai_provider_credentials (project_id, provider, api_base, api_key_encrypted)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, provider) DO UPDATE
		   SET api_base          = EXCLUDED.api_base,
		       api_key_encrypted = EXCLUDED.api_key_encrypted,
		       updated_at        = NOW()
	`, projectID, provider, apiBase, enc); err != nil {
		respondError(c, http.StatusInternalServerError, "store credential: "+err.Error())
		return
	}

	auditMeta, _ := json.Marshal(gin.H{"provider": provider, "key_hint": maskAIKey(apiKey)})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, 'SetAIProviderCredential', 'AIGateway', $3, $4)`,
		claims.UserID, projectID, provider, auditMeta,
	)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DeleteAIProviderCredential removes a project's key for one provider. Every
// subsequent gateway call for a model of that provider fails closed with
// "no credential for project/provider" rather than falling back to a shared
// platform key.
//
// @ID          deleteAIProviderCredential
// @Summary     Delete a project's AI provider credential
// @Description Removes the stored provider key. Calls to models of that provider then fail closed -- there is no shared platform key to fall back to.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       provider  path string true "Provider name"
// @Success     204       "no content"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/credentials/{provider} [delete]
func (h *Handler) DeleteAIProviderCredential(c *gin.Context) {
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
	provider := c.Param("provider")

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

	tag, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM ai_provider_credentials WHERE project_id = $1 AND provider = $2`,
		projectID, provider,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "delete credential: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, 'DeleteAIProviderCredential', 'AIGateway', $3, '{}'::jsonb)`,
		claims.UserID, projectID, provider,
	)

	c.Status(http.StatusNoContent)
}
