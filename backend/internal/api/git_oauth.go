package api

import (
	"net/http"
	"net/url"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// oauthStateTTL bounds how long a github_oauth_states row is valid. The row is
// one-time (deleted on callback); the TTL caps a leaked or stale state.
const oauthStateTTL = 10 * time.Minute

// StartGitHubUserAuth issues a GitHub App user-authorization URL for the connect
// wizard. Unlike the install URL, this proves the caller controls a GitHub
// account and lets the callback bind the App installations that account already
// has -- the only path that works once the App is installed but on a different
// org. A random one-time state row binds the eventual callback to this project
// and user. No redirect_uri is sent: GitHub uses the App's configured callback
// URL (/api/v1/git/github/oauth/callback).
//
// @ID          startGitHubUserAuth
// @Summary     Start GitHub App user authorization for a project
// @Description Returns a GitHub OAuth authorize URL. After the user authorizes, the callback binds the installations that user can access to this project. canWrite required. 503 when GitHub OAuth is unconfigured.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]string "object with a url field"
// @Failure     403       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/git/github/authorize [get]
func (h *Handler) StartGitHubUserAuth(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}
	if h.cfg.GithubAppClientID == "" {
		respondError(c, http.StatusServiceUnavailable, "github oauth not configured")
		return
	}
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

	state := randomHex(24)
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO github_oauth_states (state, project_id, user_id) VALUES ($1, $2, $3)`,
		state, projectID, claims.UserID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to start authorization")
		return
	}

	u := "https://github.com/login/oauth/authorize?client_id=" +
		url.QueryEscape(h.cfg.GithubAppClientID) + "&state=" + url.QueryEscape(state)
	if h.cfg.GithubOAuthRedirectURI != "" {
		u += "&redirect_uri=" + url.QueryEscape(h.cfg.GithubOAuthRedirectURI)
	}
	c.JSON(http.StatusOK, gin.H{"url": u})
}

// GitHubOAuthCallback is the public GitHub App user-authorization callback. GitHub
// redirects the browser here with code + our one-time state. It consumes the
// state (one-time), exchanges the code for the user's installations via the
// build-agent (which holds the OAuth secret), upserts each installation into the
// project's org, then redirects back to the wizard. Only installations the
// authorizing user can access are bound, so a user can never attach an
// installation they do not control.
//
// Public (no bearer): an anonymous browser redirect. Trust is the one-time state
// row plus the code that only the authorizing user's browser holds.
//
// @ID          gitHubOAuthCallback
// @Summary     GitHub App user-authorization callback
// @Description Public endpoint GitHub redirects to after user authorization. Consumes the one-time state, exchanges the code, binds the user's installations, redirects to the wizard.
// @Tags        git
// @Param       code  query string true "GitHub OAuth code"
// @Param       state query string true "One-time state"
// @Success     302
// @Router      /git/github/oauth/callback [get]
func (h *Handler) GitHubOAuthCallback(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}
	state := c.Query("state")
	code := c.Query("code")
	if state == "" || code == "" {
		respondError(c, http.StatusBadRequest, "missing code or state")
		return
	}

	var projectID uuid.UUID
	var createdAt time.Time
	err := h.pool.QueryRow(c.Request.Context(),
		`DELETE FROM github_oauth_states WHERE state = $1 RETURNING project_id, created_at`,
		state,
	).Scan(&projectID, &createdAt)
	if err == pgx.ErrNoRows {
		respondError(c, http.StatusBadRequest, "invalid or expired state")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify state")
		return
	}
	if time.Since(createdAt) > oauthStateTTL {
		respondError(c, http.StatusBadRequest, "invalid or expired state")
		return
	}

	res, err := h.buildagent.ExchangeUserCode(c.Request.Context(), code)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to resolve github authorization")
		return
	}

	for _, inst := range res.Installations {
		if _, err := h.pool.Exec(c.Request.Context(),
			`INSERT INTO git_app_installations
			   (project_id, org_id, provider, installation_id, account_login, account_type)
			 VALUES ($1, (SELECT org_id FROM projects WHERE id = $1), 'github', $2, $3, $4)
			 ON CONFLICT (org_id, provider, installation_id)
			 DO UPDATE SET account_login = EXCLUDED.account_login,
			               account_type  = EXCLUDED.account_type,
			               updated_at    = NOW()`,
			projectID, inst.InstallationID, inst.AccountLogin, inst.AccountType,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to save installation")
			return
		}
	}

	c.Redirect(http.StatusFound,
		"/projects/"+projectID.String()+"/git/import?connected=1")
}
