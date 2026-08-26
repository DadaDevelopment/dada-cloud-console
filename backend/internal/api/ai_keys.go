package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// aiKeyPrefix marks an AI Gateway key plaintext. The gateway plugin routes
// introspection by this prefix: `sk-dada-ai-` resolves against the console
// (POST /internal/ai/key/introspect), any other `sk-dada-` key against
// user-service. Keeping the prefix inside the `sk-dada-` family means every
// platform credential still looks the same to a user.
const aiKeyPrefix = "sk-dada-ai-"

// aiKeyPrefixLen is how many leading plaintext characters are persisted as
// token_prefix: aiKeyPrefix plus 6 hex chars, enough to tell two keys apart in
// a list without revealing either.
const aiKeyPrefixLen = len(aiKeyPrefix) + 6

// aiKeyScopes is the scope set every self-service key carries. Both AI call
// types are granted together on purpose: a developer who can chat can also
// embed, and splitting them only produces the "missing scope ai:embeddings"
// failure mode halfway through an integration.
const aiKeyScopes = "ai:chat ai:embeddings"

// aiKeyUsageThrottleSQL bounds how often a key's last_used_at is refreshed.
// The gateway introspects on every cache miss (TTL 60s), so an unthrottled
// write would turn a read-mostly table into one UPDATE per minute per active
// key. Kept as a SQL interval literal because the value is only ever
// interpolated into the throttle predicate below.
const aiKeyUsageThrottleSQL = "5 minutes"

// generateAIKey mints a new plaintext AI Gateway key plus its derived hash and
// prefix. The plaintext is aiKeyPrefix followed by 48 hex characters (24 random
// bytes) and is returned to the caller exactly once.
func generateAIKey() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = aiKeyPrefix + hex.EncodeToString(buf)
	hash = hashAIKey(plaintext)
	prefix = plaintext[:aiKeyPrefixLen]
	return plaintext, hash, prefix, nil
}

// hashAIKey returns the hex-encoded sha256 of an AI Gateway key plaintext --
// the only form ever persisted or compared against.
func hashAIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

type createAIKeyRequest struct {
	Name string `json:"name"`
}

type createAIKeyResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Key         string    `json:"key"`
	TokenPrefix string    `json:"token_prefix"`
	Scopes      string    `json:"scopes"`
	BaseURL     string    `json:"base_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type aiKeyListItem struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      string     `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// CreateAIGatewayKey mints a project-scoped AI Gateway key. This is the whole
// self-service path: the returned key plus base_url is a working OpenAI-SDK
// configuration, which is why the response carries the gateway URL alongside
// the secret.
//
// @ID          createAIGatewayKey
// @Summary     Create an AI Gateway key for a project
// @Description Mints a revocable project-scoped key for the AI Gateway (scopes ai:chat and ai:embeddings). Use it as the api_key of any OpenAI-compatible SDK with base_url set to the returned base_url. The plaintext key is returned ONLY in this response -- store it now, it cannot be retrieved again.
// @Tags        ai-gateway
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string             true  "Project UUID"
// @Param       body      body     createAIKeyRequest false "Optional label for the key"
// @Success     201       {object} createAIKeyResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/keys [post]
func (h *Handler) CreateAIGatewayKey(c *gin.Context) {
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

	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "CreateAIGatewayKey",
			ResourceKind: "AIGateway",
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	var req createAIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		reject(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}

	plaintext, hash, prefix, err := generateAIKey()
	if err != nil {
		reject(http.StatusInternalServerError, "key_generate_failed", "failed to generate key")
		return
	}

	var id uuid.UUID
	var createdAt time.Time
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO ai_gateway_keys (project_id, name, scopes, token_hash, token_prefix, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		projectID, req.Name, aiKeyScopes, hash, prefix, claims.UserID,
	).Scan(&id, &createdAt); err != nil {
		reject(http.StatusInternalServerError, "key_insert_failed", "failed to create ai key")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "CreateAIGatewayKey",
		ResourceKind: "AIGateway",
		ResourceName: prefix,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"name": req.Name, "token_prefix": prefix},
	})

	c.JSON(http.StatusCreated, createAIKeyResponse{
		ID:          id,
		Name:        req.Name,
		Key:         plaintext,
		TokenPrefix: prefix,
		Scopes:      aiKeyScopes,
		BaseURL:     h.aiGatewayBaseURL(),
		CreatedAt:   createdAt,
	})
}

// ListAIGatewayKeys lists a project's AI Gateway keys, revoked ones included.
// The plaintext key and its hash are never returned after creation.
//
// @ID          listAIGatewayKeys
// @Summary     List a project's AI Gateway keys
// @Description Lists the AI Gateway keys minted for this project (including revoked ones). Never returns the plaintext key or its hash -- only the id, label, token_prefix, scopes and lifecycle timestamps.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with keys array and base_url"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/keys [get]
func (h *Handler) ListAIGatewayKeys(c *gin.Context) {
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
		`SELECT id, name, token_prefix, scopes, created_at, last_used_at, revoked_at
		   FROM ai_gateway_keys
		  WHERE project_id = $1
		  ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query ai keys")
		return
	}
	defer rows.Close()

	out := []aiKeyListItem{}
	for rows.Next() {
		var it aiKeyListItem
		if err := rows.Scan(&it.ID, &it.Name, &it.TokenPrefix, &it.Scopes, &it.CreatedAt, &it.LastUsedAt, &it.RevokedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan ai key")
			return
		}
		out = append(out, it)
	}

	c.JSON(http.StatusOK, gin.H{"keys": out, "base_url": h.aiGatewayBaseURL()})
}

// DeleteAIGatewayKey revokes an AI Gateway key. Revocation is permanent and
// takes effect at the gateway within one introspection TTL (60s).
//
// @ID          deleteAIGatewayKey
// @Summary     Revoke an AI Gateway key
// @Description Permanently revokes the key. The gateway caches introspection for up to 60 seconds, so an in-flight key stops working within a minute.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       keyId     path string true "Key UUID"
// @Success     204       "no content"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/keys/{keyId} [delete]
func (h *Handler) DeleteAIGatewayKey(c *gin.Context) {
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
	keyID, err := uuid.Parse(c.Param("keyId"))
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

	var tokenPrefix string
	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to revoke ai key")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	err = tx.QueryRow(c.Request.Context(),
		`UPDATE ai_gateway_keys SET revoked_at = now()
		  WHERE id = $1 AND project_id = $2 AND revoked_at IS NULL
		  RETURNING token_prefix`,
		keyID, projectID,
	).Scan(&tokenPrefix)
	rejectRevoke := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "RevokeAIGatewayKey",
			ResourceKind: "AIGateway",
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": reason, "status": status, "key_id": keyID},
		})
		respond()
	}
	if err == pgx.ErrNoRows {
		rejectRevoke(http.StatusNotFound, "key_not_found_or_revoked", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		rejectRevoke(http.StatusInternalServerError, "revoke_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to revoke ai key")
		})
		return
	}
	// Revocation is permanent, so remove the encrypted upstream material in the
	// same transaction instead of retaining secrets that can never be used.
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM ai_gateway_key_credentials WHERE gateway_key_id = $1`, keyID); err != nil {
		rejectRevoke(http.StatusInternalServerError, "credential_delete_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to revoke ai key")
		})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		rejectRevoke(http.StatusInternalServerError, "commit_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to revoke ai key")
		})
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "RevokeAIGatewayKey",
		ResourceKind: "AIGateway",
		ResourceName: tokenPrefix,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"key_id": keyID, "token_prefix": tokenPrefix},
	})

	c.Status(http.StatusNoContent)
}

type aiKeyIntrospectRequest struct {
	APIKey string `json:"api_key" binding:"required"`
}

// aiKeyIntrospectResponse mirrors user-service's introspection contract field
// for field, so the gateway plugin can consume either backend without a
// per-source branch beyond choosing the URL.
// identity_id is the one field with no user-service counterpart: it is empty
// for every key that is not an sk-dada-id- token, and the gateway simply
// forwards whatever it gets onto the usage row (ADR-021 phase 4). An absent
// field there means "no identity paid for this", which is the truth for a
// project-scoped key.
// reason is set only alongside valid=false, and only when the rejection is
// something other than "this token does not resolve". A gateway that does not
// know the field still refuses the call, so the enforcement never depends on
// both sides shipping together -- the field only decides whether the operator
// is told why.
type aiKeyIntrospectResponse struct {
	Valid        bool   `json:"valid"`
	GatewayKeyID string `json:"gateway_key_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	OrgID        string `json:"org_id,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
	PrincipalID  string `json:"principal_id,omitempty"`
	IdentityID   string `json:"identity_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// aiIntrospectReasonBudget is the rejection reason for an identity that has
// spent its monthly ceiling. The value is part of the gateway contract: the
// plugin matches on it to turn the refusal into a message an app developer can
// act on instead of "invalid platform api key".
const aiIntrospectReasonBudget = "ai_budget_exceeded"

// AIIntrospectKey resolves a console-minted AI Gateway key to its project.
// Server-to-server only: the gateway plugin posts the raw key in the body over
// the internal-token-guarded channel, exactly as it does against user-service.
//
// An unknown or revoked key answers 200 with valid=false rather than 401 --
// the 401 belongs to the internal-token guard, and conflating the two would
// make an expired gateway secret look like a bad end-user key.
//
// POST /internal/ai/key/introspect (guarded by requireInternalToken)
func (h *Handler) AIIntrospectKey(c *gin.Context) {
	var req aiKeyIntrospectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	token := strings.TrimSpace(req.APIKey)
	if strings.HasPrefix(token, identityTokenPrefix) {
		h.introspectIdentityAsAIKey(c, token)
		return
	}
	if !strings.HasPrefix(token, aiKeyPrefix) {
		c.JSON(http.StatusOK, aiKeyIntrospectResponse{Valid: false})
		return
	}

	var keyID, projectID uuid.UUID
	var scopes string
	var orgID *string
	var createdBy *uuid.UUID
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT k.id, k.project_id, k.scopes, p.org_id, k.created_by
		   FROM ai_gateway_keys k
		   JOIN projects p ON p.id = k.project_id
		  WHERE k.token_hash = $1 AND k.revoked_at IS NULL`,
		hashAIKey(token),
	).Scan(&keyID, &projectID, &scopes, &orgID, &createdBy)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusOK, aiKeyIntrospectResponse{Valid: false})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "introspect ai key: "+err.Error())
		return
	}

	h.touchAIKey(c.Request.Context(), keyID)

	resp := aiKeyIntrospectResponse{
		Valid:        true,
		GatewayKeyID: keyID.String(),
		ProjectID:    projectID.String(),
		Scopes:       scopes,
	}
	if orgID != nil {
		resp.OrgID = *orgID
	}
	if createdBy != nil {
		resp.PrincipalID = createdBy.String()
	}
	c.JSON(http.StatusOK, resp)
}

// touchAIKey refreshes last_used_at at most once per aiKeyUsageThrottleSQL.
// Best effort: a failed bookkeeping write must never fail the introspection
// that is holding up a live inference request.
func (h *Handler) touchAIKey(ctx context.Context, keyID uuid.UUID) {
	_, _ = h.pool.Exec(ctx,
		`UPDATE ai_gateway_keys SET last_used_at = now()
		  WHERE id = $1
		    AND (last_used_at IS NULL OR last_used_at < now() - $2::interval)`,
		keyID, aiKeyUsageThrottleSQL,
	)
}

// aiGatewayBaseURL is the OpenAI-compatible base_url a caller points an SDK at.
func (h *Handler) aiGatewayBaseURL() string {
	return strings.TrimRight(h.cfg.AIGatewayPublicURL, "/")
}
