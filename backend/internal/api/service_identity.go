package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// identityTokenPrefix marks a ServiceIdentity token plaintext (ADR-021). It
// stays inside the `sk-dada-` family so every platform credential still looks
// the same to a user, and is distinguishable from `sk-dada-ai-` so the gateway
// plugin can route introspection by prefix alone: both console-minted families
// resolve against the console, anything else against user-service.
const identityTokenPrefix = "sk-dada-id-"

// identityTokenPrefixLen is how many leading plaintext characters are persisted
// as token_prefix: identityTokenPrefix plus 6 hex chars, enough to tell two
// tokens apart in a list without revealing either.
const identityTokenPrefixLen = len(identityTokenPrefix) + 6

// identityDefaultScopes is what a freshly declared app identity carries. AI is
// granted by default because it is the audience every app has today; payment
// scopes are opt-in, since an app that can spend money should say so.
const identityDefaultScopes = "ai:chat ai:embeddings"

// identityUsageThrottleSQL bounds how often a token's last_used_at is
// refreshed. Introspection runs on every gateway cache miss, so an unthrottled
// write would turn a read-mostly table into one UPDATE per minute per active
// token.
const identityUsageThrottleSQL = "5 minutes"

// generateIdentityToken mints a new plaintext identity token plus its derived
// hash and prefix, mirroring generateAIKey exactly. The plaintext is
// identityTokenPrefix followed by 48 hex characters (24 random bytes) and is
// returned to the caller exactly once.
func generateIdentityToken() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = identityTokenPrefix + hex.EncodeToString(buf)
	hash = hashIdentityToken(plaintext)
	prefix = plaintext[:identityTokenPrefixLen]
	return plaintext, hash, prefix, nil
}

// hashIdentityToken returns the hex-encoded sha256 of an identity token
// plaintext -- the only form ever persisted or compared against.
func hashIdentityToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// identityScopes splits a scope string into its whitespace-separated parts.
func identityScopes(scopes string) []string {
	return strings.Fields(scopes)
}

// identityHasScope reports whether a scope set grants the wanted scope. Every
// platform service checks the one scope it needs, which is what lets a second
// audience be added without a second credential table.
func identityHasScope(scopes, want string) bool {
	for _, s := range identityScopes(scopes) {
		if s == want {
			return true
		}
	}
	return false
}

// resolvedIdentity is what a token resolves to: the principal (identity id),
// where that principal currently lives, and what it may do. project_id and
// environment_id are the identity's *current location* and are re-pointed by
// MoveApp; the id is what the token names and never changes.
type resolvedIdentity struct {
	IdentityID  uuid.UUID
	TokenID     uuid.UUID
	AppName     string
	ProjectID   *uuid.UUID
	EnvID       *uuid.UUID
	OrgID       string
	Scopes      string
	PrincipalID *uuid.UUID
}

// resolveIdentityToken looks a plaintext token up by hash and returns the live
// identity behind it. A revoked token, a revoked identity, or a token whose
// identity row is gone all resolve to pgx.ErrNoRows -- never to a default
// project. An identity outlives a project; it does not outlive its app.
func resolveIdentityToken(ctx context.Context, q pgxQuerier, plaintext string) (resolvedIdentity, error) {
	var out resolvedIdentity
	var appName *string
	var orgID *string
	err := q.QueryRow(ctx,
		`SELECT t.id, i.id, i.app_name, i.project_id, i.environment_id, i.scopes, i.created_by, p.org_id
		   FROM service_identity_tokens t
		   JOIN service_identities i ON i.id = t.identity_id
		   LEFT JOIN projects p ON p.id = i.project_id
		  WHERE t.token_hash = $1
		    AND t.revoked_at IS NULL
		    AND i.revoked_at IS NULL`,
		hashIdentityToken(plaintext),
	).Scan(&out.TokenID, &out.IdentityID, &appName, &out.ProjectID, &out.EnvID, &out.Scopes, &out.PrincipalID, &orgID)
	if err != nil {
		return resolvedIdentity{}, err
	}
	if appName != nil {
		out.AppName = *appName
	}
	if orgID != nil {
		out.OrgID = *orgID
	}
	return out, nil
}

// pgxQuerier is the read surface resolveIdentityToken needs, so the resolution
// can run on a pool or inside a transaction.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// touchIdentityToken refreshes last_used_at at most once per
// identityUsageThrottleSQL. Best effort: a failed bookkeeping write must never
// fail the introspection that is holding up a live request.
func (h *Handler) touchIdentityToken(ctx context.Context, tokenID uuid.UUID) {
	_, _ = h.pool.Exec(ctx,
		`UPDATE service_identity_tokens SET last_used_at = now()
		  WHERE id = $1
		    AND (last_used_at IS NULL OR last_used_at < now() - $2::interval)`,
		tokenID, identityUsageThrottleSQL,
	)
}

type identityIntrospectRequest struct {
	Token string `json:"token"`
}

type identityIntrospectResponse struct {
	Valid       bool   `json:"valid"`
	IdentityID  string `json:"identity_id,omitempty"`
	AppName     string `json:"app_name,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	EnvID       string `json:"env_id,omitempty"`
	OrgID       string `json:"org_id,omitempty"`
	Scopes      string `json:"scopes,omitempty"`
	PrincipalID string `json:"principal_id,omitempty"`
}

// IntrospectServiceIdentity is the one endpoint every platform service calls to
// turn a bearer token into a principal (ADR-021). It never 401s on a bad token
// -- it answers 200 with valid=false, so the 401 belongs to the internal-token
// guard and an expired service secret is not mistaken for a bad caller token.
//
// POST /internal/identity/introspect (guarded by requireInternalToken)
func (h *Handler) IntrospectServiceIdentity(c *gin.Context) {
	var req identityIntrospectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	token := strings.TrimSpace(req.Token)
	if !strings.HasPrefix(token, identityTokenPrefix) {
		c.JSON(http.StatusOK, identityIntrospectResponse{Valid: false})
		return
	}

	ident, err := resolveIdentityToken(c.Request.Context(), h.pool, token)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusOK, identityIntrospectResponse{Valid: false})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "introspect identity: "+err.Error())
		return
	}

	h.touchIdentityToken(c.Request.Context(), ident.TokenID)
	c.JSON(http.StatusOK, identityToIntrospectResponse(ident))
}

// identityToIntrospectResponse maps a resolved identity onto the wire shape,
// leaving the location fields empty for an identity that has none (a service
// outside the cluster, ADR-021 "not every consumer is an app").
func identityToIntrospectResponse(ident resolvedIdentity) identityIntrospectResponse {
	resp := identityIntrospectResponse{
		Valid:      true,
		IdentityID: ident.IdentityID.String(),
		AppName:    ident.AppName,
		OrgID:      ident.OrgID,
		Scopes:     ident.Scopes,
	}
	if ident.ProjectID != nil {
		resp.ProjectID = ident.ProjectID.String()
	}
	if ident.EnvID != nil {
		resp.EnvID = ident.EnvID.String()
	}
	if ident.PrincipalID != nil {
		resp.PrincipalID = ident.PrincipalID.String()
	}
	return resp
}

// introspectIdentityAsAIKey answers an AI Gateway introspection with a
// ServiceIdentity token, in the shape the gateway plugin already parses. This
// is what lets an app move off its pasted project-scoped key without the
// gateway learning a second response format (ADR-021).
//
// An identity with no current project resolves to invalid here: the gateway
// selects the BYOK ai_provider_credentials row by project, so a project-less
// identity (a service outside the cluster) has no AI answer to give -- and
// answering with a default project is exactly the isolation break this design
// exists to prevent.
func (h *Handler) introspectIdentityAsAIKey(c *gin.Context, token string) {
	ident, err := resolveIdentityToken(c.Request.Context(), h.pool, token)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusOK, aiKeyIntrospectResponse{Valid: false})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "introspect identity: "+err.Error())
		return
	}
	if ident.ProjectID == nil {
		c.JSON(http.StatusOK, aiKeyIntrospectResponse{Valid: false})
		return
	}

	h.touchIdentityToken(c.Request.Context(), ident.TokenID)

	resp := aiKeyIntrospectResponse{
		Valid:      true,
		ProjectID:  ident.ProjectID.String(),
		OrgID:      ident.OrgID,
		Scopes:     ident.Scopes,
		IdentityID: ident.IdentityID.String(),
	}
	if ident.PrincipalID != nil {
		resp.PrincipalID = ident.PrincipalID.String()
	}
	c.JSON(http.StatusOK, resp)
}

// createIdentityRequest is the optional body of the mint/rotate route. An
// empty body keeps identityDefaultScopes on a new identity and leaves an
// existing identity's scopes alone, so rotation never silently widens or
// narrows what a live token may do.
type createIdentityRequest struct {
	Scopes []string `json:"scopes"`
}

// identityGrantableScopes is every scope the mint route will hand out. An
// unknown scope is rejected rather than stored: a typo that ends up in the
// scopes column would fail closed at the audience, far from its cause, and a
// scope no service checks is a permission nobody can revoke meaningfully.
var identityGrantableScopes = map[string]bool{
	"ai:chat":       true,
	"ai:embeddings": true,
	payScopeCharge:  true,
}

// normalizeIdentityScopes validates requested scopes and renders them in the
// stored form: whitespace-separated, deduplicated, order preserved.
func normalizeIdentityScopes(requested []string) (string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(requested))
	for _, raw := range requested {
		for _, s := range strings.Fields(raw) {
			if !identityGrantableScopes[s] {
				return "", fmt.Errorf("unknown scope %q", s)
			}
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return "", errors.New("scopes must not be empty")
	}
	return strings.Join(out, " "), nil
}

type createIdentityResponse struct {
	IdentityID  uuid.UUID `json:"identity_id"`
	AppName     string    `json:"app_name"`
	Token       string    `json:"token"`
	TokenPrefix string    `json:"token_prefix"`
	Scopes      string    `json:"scopes"`
	CreatedAt   time.Time `json:"created_at"`
}

type identityView struct {
	IdentityID  uuid.UUID  `json:"identity_id"`
	AppName     string     `json:"app_name"`
	Scopes      string     `json:"scopes"`
	TokenPrefix string     `json:"token_prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// CreateAppServiceIdentity declares (or rotates) an app's platform identity and
// returns a freshly minted token. Rotation reuses the identity row on purpose:
// the principal, and everything attributed to it, must survive a token change.
//
// @ID          createAppServiceIdentity
// @Summary     Declare or rotate an app's platform service identity
// @Description Mints a revocable token for the app's ServiceIdentity -- one credential accepted by every platform service, bounded by the identity's scopes. Rotating replaces the token and keeps the identity. An optional scopes array sets what the identity may do (ai:chat, ai:embeddings, pay:charge); omitting it keeps the current scopes. The plaintext token is returned ONLY in this response.
// @Tags        service-identity
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       body      body     createIdentityRequest false "Requested scopes"
// @Success     201       {object} createIdentityResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/identity [post]
func (h *Handler) CreateAppServiceIdentity(c *gin.Context) {
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

	var req createIdentityRequest
	if c.Request.Body != nil && c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	requestedScopes := ""
	if len(req.Scopes) > 0 {
		requestedScopes, err = normalizeIdentityScopes(req.Scopes)
		if err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
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

	var appCount int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		  WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&appCount); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if appCount == 0 {
		respondNotFound(c)
		return
	}

	plaintext, hash, prefix, err := generateIdentityToken()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to generate token")
		return
	}

	ctx := c.Request.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var identityID uuid.UUID
	var scopes string
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, scopes, created_at FROM service_identities
		  WHERE app_name = $1 AND environment_id = $2 AND revoked_at IS NULL`,
		appName, envID,
	).Scan(&identityID, &scopes, &createdAt)
	if err == pgx.ErrNoRows {
		scopes = identityDefaultScopes
		if requestedScopes != "" {
			scopes = requestedScopes
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO service_identities (app_name, project_id, environment_id, display_name, scopes, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id, created_at`,
			appName, projectID, envID, appName, scopes, claims.UserID,
		).Scan(&identityID, &createdAt); err != nil {
			respondError(c, http.StatusInternalServerError, "create identity: "+err.Error())
			return
		}
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "load identity: "+err.Error())
		return
	} else if requestedScopes != "" && requestedScopes != scopes {
		if _, err := tx.Exec(ctx,
			`UPDATE service_identities SET scopes = $1 WHERE id = $2`,
			requestedScopes, identityID,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "update scopes: "+err.Error())
			return
		}
		scopes = requestedScopes
	}

	if _, err := tx.Exec(ctx,
		`UPDATE service_identity_tokens SET revoked_at = now()
		  WHERE identity_id = $1 AND revoked_at IS NULL`,
		identityID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "revoke previous token: "+err.Error())
		return
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO service_identity_tokens (identity_id, token_hash, token_prefix, created_by)
		 VALUES ($1, $2, $3, $4)`,
		identityID, hash, prefix, claims.UserID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "mint token: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		respondError(c, http.StatusInternalServerError, "commit identity: "+err.Error())
		return
	}

	h.recordAudit(ctx, claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "CreateAppServiceIdentity",
		ResourceKind:  "ServiceIdentity",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"identity_id": identityID, "token_prefix": prefix},
	})

	c.JSON(http.StatusCreated, createIdentityResponse{
		IdentityID:  identityID,
		AppName:     appName,
		Token:       plaintext,
		TokenPrefix: prefix,
		Scopes:      scopes,
		CreatedAt:   createdAt,
	})
}

// GetAppServiceIdentity returns the app's identity without its token. Absence
// is a 404: an app that has never declared an identity is not an error, but it
// is also not an identity.
//
// @ID          getAppServiceIdentity
// @Summary     Show an app's platform service identity
// @Description Returns the app's ServiceIdentity and its live token prefix. Never returns the token itself.
// @Tags        service-identity
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} identityView
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/identity [get]
func (h *Handler) GetAppServiceIdentity(c *gin.Context) {
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

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err != nil {
		if err == pgx.ErrNoRows {
			respondNotFound(c)
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var out identityView
	var tokenPrefix *string
	var lastUsed *time.Time
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT i.id, i.app_name, i.scopes, i.created_at, t.token_prefix, t.last_used_at
		   FROM service_identities i
		   LEFT JOIN service_identity_tokens t
		     ON t.identity_id = i.id AND t.revoked_at IS NULL
		  WHERE i.app_name = $1 AND i.environment_id = $2 AND i.revoked_at IS NULL
		  ORDER BY t.created_at DESC NULLS LAST
		  LIMIT 1`,
		appName, envID,
	).Scan(&out.IdentityID, &out.AppName, &out.Scopes, &out.CreatedAt, &tokenPrefix, &lastUsed)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "load identity: "+err.Error())
		return
	}
	if tokenPrefix != nil {
		out.TokenPrefix = *tokenPrefix
	}
	out.LastUsedAt = lastUsed
	c.JSON(http.StatusOK, out)
}
