package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint error (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// gitInstallation mirrors the frontend GitInstallation shape.
type gitInstallation struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	Provider       string    `json:"provider"`
	InstallationID string    `json:"installation_id"` // BIGINT rendered as string
	AccountLogin   string    `json:"account_login"`
	AccountType    string    `json:"account_type"`
	CreatedAt      time.Time `json:"created_at"`
}

// gitRepo mirrors the frontend GitRepo shape.
type gitRepo struct {
	ID                uuid.UUID  `json:"id"`
	ProjectID         uuid.UUID  `json:"project_id"`
	EnvironmentID     uuid.UUID  `json:"environment_id"`
	AppName           string     `json:"app_name"`
	Provider          string     `json:"provider"`
	InstallationID    *uuid.UUID `json:"installation_id,omitempty"`
	RepoFullName      string     `json:"repo_full_name"`
	ProductionBranch  string     `json:"production_branch"`
	RootDir           string     `json:"root_dir"`
	FrameworkOverride *string    `json:"framework_override,omitempty"`
	AutoDeploy        bool       `json:"auto_deploy"`
	Port              int        `json:"port"`
	Replicas          int        `json:"replicas"`
	Profile           string     `json:"profile"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ListGitInstallations returns the GitHub/GitLab App installations bound to a project.
//
// @ID          listGitInstallations
// @Summary     List git provider installations for a project
// @Description Returns the GitHub App (or GitLab) installations bound to the project's org.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with an installations array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/git/installations [get]
func (h *Handler) ListGitInstallations(c *gin.Context) {
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

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT gai.id, gai.project_id, gai.provider, gai.installation_id, gai.account_login, gai.account_type, gai.created_at
		 FROM git_app_installations gai
		 JOIN projects p ON p.org_id = gai.org_id
		 WHERE p.id = $1
		 ORDER BY gai.created_at`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query installations")
		return
	}
	defer rows.Close()

	installations := []gitInstallation{}
	for rows.Next() {
		var inst gitInstallation
		var installID int64
		if err := rows.Scan(&inst.ID, &inst.ProjectID, &inst.Provider, &installID,
			&inst.AccountLogin, &inst.AccountType, &inst.CreatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan installation")
			return
		}
		inst.InstallationID = strconv.FormatInt(installID, 10)
		installations = append(installations, inst)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading installations")
		return
	}

	c.JSON(http.StatusOK, gin.H{"installations": installations})
}

// GetGitInstallURL returns the provider install URL the user must visit to grant access.
//
// @ID          getGitInstallUrl
// @Summary     Get the git provider install URL
// @Description Returns the GitHub App (or GitLab) install URL, with project + CSRF state encoded, that the user visits to grant repository access. Requires write access.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       provider  query    string true "Provider: github | gitlab"
// @Success     200       {object} map[string]string "object with the install url"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/git/install-url [get]
func (h *Handler) GetGitInstallURL(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
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

	provider := c.Query("provider")
	if provider == "" {
		provider = "github"
	}
	if provider != "github" && provider != "gitlab" {
		respondError(c, http.StatusBadRequest, "provider must be github or gitlab")
		return
	}
	if provider == "gitlab" {
		respondError(c, http.StatusServiceUnavailable, "gitlab connect not implemented")
		return
	}

	if h.cfg.GitAppSlug == "" {
		respondError(c, http.StatusServiceUnavailable, "git app slug not configured")
		return
	}
	secret := h.stateSecret()
	if secret == "" {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}

	// Public GitHub App install URL. GitHub redirects the browser back to the
	// App's Setup URL (our /api/v1/git/install/callback) with installation_id +
	// this state. The state is HMAC-signed so the callback can trust the project
	// binding without a server-side nonce table.
	state := signInstallState(secret, projectID)
	u := "https://github.com/apps/" + url.PathEscape(h.cfg.GitAppSlug) +
		"/installations/new?state=" + url.QueryEscape(state)
	c.JSON(http.StatusOK, gin.H{"url": u})
}

// stateSecret picks the HMAC key for signing the install-callback state. The
// connect flow already requires the build-agent, so its token secret is the
// natural choice; fall back to the JWT secret.
func (h *Handler) stateSecret() string {
	if h.cfg.BuildAgentTokenSecret != "" {
		return h.cfg.BuildAgentTokenSecret
	}
	return h.cfg.JWTSecret
}

// signInstallState returns "<projectID>.<nonce>.<hmacHex>" binding the install
// callback to a project. The nonce defeats replay/guessing; the HMAC defeats
// forgery (only the server can mint a state for an arbitrary project).
func signInstallState(secret string, projectID uuid.UUID) string {
	payload := projectID.String() + "." + randomHex(16)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifyInstallState validates a signed state and returns the bound project id.
func verifyInstallState(secret, state string) (uuid.UUID, bool) {
	parts := strings.Split(state, ".")
	if len(parts) != 3 {
		return uuid.Nil, false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return uuid.Nil, false
	}
	pid, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, false
	}
	return pid, true
}

// GitInstallCallback is the public GitHub App Setup URL. After a user installs
// the App, GitHub redirects the browser here with installation_id + the signed
// state we issued. We verify the state, resolve the installation's account via
// the build-agent (it holds the App key), upsert git_app_installations, then
// redirect the browser back to the import wizard.
//
// Public (no bearer): the caller is an anonymous browser redirect. Trust is the
// HMAC-signed state, not a session.
//
// @ID          gitInstallCallback
// @Summary     GitHub App install callback (Setup URL)
// @Description Public endpoint GitHub redirects to after App install. Verifies the signed state, persists the installation, redirects to the import wizard.
// @Tags        git
// @Param       installation_id query string true "GitHub installation id"
// @Param       state           query string true "Signed install state"
// @Success     302
// @Router      /git/install/callback [get]
func (h *Handler) GitInstallCallback(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}
	secret := h.stateSecret()
	if secret == "" {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}

	state := c.Query("state")
	projectID, ok := verifyInstallState(secret, state)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid install state")
		return
	}

	installIDStr := c.Query("installation_id")
	installID, err := strconv.ParseInt(installIDStr, 10, 64)
	if err != nil {
		respondError(c, http.StatusBadRequest, "missing installation_id")
		return
	}

	// Resolve the org/user behind the installation (build-agent has the App key).
	acct, err := h.buildagent.GetInstallationAccount(c.Request.Context(), installID)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to resolve installation")
		return
	}

	// Upsert: re-installing the same App for the same org is idempotent. Installations
	// are scoped to the org (migration 026), so the conflict target is org-level.
	_, err = h.pool.Exec(c.Request.Context(),
		`INSERT INTO git_app_installations
		   (project_id, org_id, provider, installation_id, account_login, account_type)
		 VALUES ($1, (SELECT org_id FROM projects WHERE id = $1), 'github', $2, $3, $4)
		 ON CONFLICT (org_id, provider, installation_id)
		 DO UPDATE SET account_login = EXCLUDED.account_login,
		               account_type  = EXCLUDED.account_type,
		               updated_at    = NOW()`,
		projectID, installID, acct.AccountLogin, acct.AccountType,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to save installation")
		return
	}

	// Relative redirect → same host as the console (backend is served behind the
	// console domain), no extra config needed.
	c.Redirect(http.StatusFound,
		"/projects/"+projectID.String()+"/git/import?connected=1")
}

// availableInstallation is one App installation the wizard can bind to a project.
type availableInstallation struct {
	InstallationID string `json:"installation_id"` // numeric, as string
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type"`
	Bound          bool   `json:"bound"` // already linked to this project
}

// ListAvailableInstallations lists every App installation the build-agent can
// see, flagging which are already bound to this project. The connect wizard uses
// it to attach an already-installed org/user without a reinstall round-trip
// (the only path that works once the App is installed org-wide).
//
// @ID          listAvailableInstallations
// @Summary     List bindable git App installations for the project's org
// @Description Lists GitHub App installations scoped to the project's org (each org owns its own GitHub accounts). Installations from other orgs are never returned. canWrite required. 503 when the build-agent is unconfigured.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with an installations array"
// @Failure     403       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/git/installations/available [get]
func (h *Handler) ListAvailableInstallations(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
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

	// Return installations that belong to the same org as the project.
	// Each org owns its own GitHub accounts; other orgs' installations are never
	// returned here (prevents cross-tenant leakage like dadaDevelopment being
	// visible to users in unrelated personal orgs).
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT gai.installation_id, gai.account_login, gai.account_type,
		        gai.project_id = $1 AS bound
		 FROM git_app_installations gai
		 JOIN projects p ON p.org_id = gai.org_id
		 WHERE p.id = $1
		 ORDER BY gai.account_login`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query installations")
		return
	}
	defer rows.Close()

	out := []availableInstallation{}
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		var login, acctType string
		var bound bool
		if err := rows.Scan(&id, &login, &acctType, &bound); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan installation")
			return
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, availableInstallation{
			InstallationID: strconv.FormatInt(id, 10),
			AccountLogin:   login,
			AccountType:    acctType,
			Bound:          bound,
		})
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading installations")
		return
	}
	c.JSON(http.StatusOK, gin.H{"installations": out})
}

// bindInstallationRequest is the body of POST …/git/installations.
type bindInstallationRequest struct {
	InstallationID string `json:"installation_id"` // numeric GitHub installation id
}

// BindInstallation attaches an existing App installation to the project. It
// resolves the account via the build-agent, then upserts git_app_installations.
// This is the connect path for an already-installed App (no GitHub redirect).
//
// @ID          bindInstallation
// @Summary     Bind an existing git App installation to a project
// @Description Attaches an already-installed GitHub App installation to the project (resolves the account via the build-agent, upserts git_app_installations). Requires write access.
// @Tags        git
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       body      body     bindInstallationRequest true "Installation id"
// @Success     201       {object} gitInstallation
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/git/installations [post]
func (h *Handler) BindInstallation(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
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

	var req bindInstallationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	installID, err := strconv.ParseInt(req.InstallationID, 10, 64)
	if err != nil {
		respondError(c, http.StatusBadRequest, "installation_id must be numeric")
		return
	}

	acct, err := h.buildagent.GetInstallationAccount(c.Request.Context(), installID)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to resolve installation")
		return
	}

	var inst gitInstallation
	var scanID int64
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO git_app_installations
		   (project_id, org_id, provider, installation_id, account_login, account_type)
		 VALUES ($1, (SELECT org_id FROM projects WHERE id = $1), 'github', $2, $3, $4)
		 ON CONFLICT (org_id, provider, installation_id)
		 DO UPDATE SET account_login = EXCLUDED.account_login,
		               account_type  = EXCLUDED.account_type,
		               project_id    = EXCLUDED.project_id,
		               updated_at    = NOW()
		 RETURNING id, project_id, provider, installation_id, account_login, account_type, created_at`,
		projectID, installID, acct.AccountLogin, acct.AccountType,
	).Scan(&inst.ID, &inst.ProjectID, &inst.Provider, &scanID,
		&inst.AccountLogin, &inst.AccountType, &inst.CreatedAt)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to save installation")
		return
	}
	inst.InstallationID = strconv.FormatInt(scanID, 10)
	c.JSON(http.StatusCreated, inst)
}

// ListInstallationRepos proxies the repository listing for an installation to the build-agent.
//
// @ID          listInstallationRepos
// @Summary     List repositories visible to an installation
// @Description Proxies to the build-agent, which lists the repositories the given installation can access. Requires write access. Returns 503 when the build-agent is not configured.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId      path     string true "Project UUID"
// @Param       installationId path     string true "Installation UUID"
// @Success     200            {object} map[string]interface{} "object with a repos array"
// @Failure     403            {object} map[string]string
// @Failure     404            {object} map[string]string
// @Failure     503            {object} map[string]string
// @Router      /projects/{projectId}/git/installations/{installationId}/repos [get]
func (h *Handler) ListInstallationRepos(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
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
	installationUUID, err := uuid.Parse(c.Param("installationId"))
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

	// Resolve the installation's numeric provider id, scoped to the requesting
	// project's org so existence isn't leaked across tenants. Installations are
	// org-scoped after the migration 026 dedup, so the surviving row's
	// project_id may differ from the request's project; match on org membership.
	var providerInstallID int64
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT i.installation_id FROM git_app_installations i
		  JOIN projects p ON p.org_id = i.org_id
		 WHERE i.id = $1 AND p.id = $2`,
		installationUUID, projectID,
	).Scan(&providerInstallID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve installation")
		return
	}

	repos, err := h.buildagent.ListInstallationRepos(c.Request.Context(), providerInstallID)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to list repositories from build-agent")
		return
	}
	if repos == nil {
		repos = []buildagent.RemoteRepo{}
	}
	c.JSON(http.StatusOK, gin.H{"repos": repos})
}

// DetectFramework proxies framework auto-detection for a remote repo to the build-agent.
//
// @ID          detectFramework
// @Summary     Detect a repository's framework
// @Description Proxies to the build-agent, which inspects the repository tree (including a shallow recursive search under the chosen root dir) and returns a best-effort framework guess. Requires write access. Returns 503 when the build-agent is not configured.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId      path     string true  "Project UUID"
// @Param       installationId path     string true  "Installation UUID"
// @Param       repo           query    string true  "Repository full name (org/repo)"
// @Param       root_dir       query    string false "Subdirectory to inspect (default .)"
// @Success     200            {object} map[string]interface{} "framework detection result"
// @Failure     403            {object} map[string]string
// @Failure     404            {object} map[string]string
// @Failure     503            {object} map[string]string
// @Router      /projects/{projectId}/git/installations/{installationId}/detect [get]
func (h *Handler) DetectFramework(c *gin.Context) {
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
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
	installationUUID, err := uuid.Parse(c.Param("installationId"))
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

	repo := c.Query("repo")
	if repo == "" {
		respondError(c, http.StatusBadRequest, "repo is required")
		return
	}
	rootDir := c.Query("root_dir")
	if rootDir == "" {
		rootDir = "."
	}

	// Scope to the requesting project's org (not project_id): installations are
	// org-scoped after migration 026 dedup, so the surviving row's project_id
	// may differ from the request's project. Match on org membership to keep
	// cross-tenant existence hidden.
	var providerInstallID int64
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT i.installation_id FROM git_app_installations i
		  JOIN projects p ON p.org_id = i.org_id
		 WHERE i.id = $1 AND p.id = $2`,
		installationUUID, projectID,
	).Scan(&providerInstallID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve installation")
		return
	}

	det, err := h.buildagent.DetectFramework(c.Request.Context(), providerInstallID, repo, rootDir)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to detect framework via build-agent")
		return
	}
	c.JSON(http.StatusOK, det)
}

// ListGitRepos returns the git-linked repos in an environment.
//
// @ID          listGitRepos
// @Summary     List git-linked repos in an environment
// @Description Returns the git repositories linked to apps in the given environment. Any project member. Never returns stored tokens or webhook secrets.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with a repos array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/repos [get]
func (h *Handler) ListGitRepos(c *gin.Context) {
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

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, app_name, installation_id, provider,
		        repo_full_name, production_branch, root_dir, framework_override,
		        auto_deploy, port, replicas, profile, created_at, updated_at
		 FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2
		 ORDER BY app_name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query repos")
		return
	}
	defer rows.Close()

	repos := []gitRepo{}
	for rows.Next() {
		var r gitRepo
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.EnvironmentID, &r.AppName,
			&r.InstallationID, &r.Provider, &r.RepoFullName, &r.ProductionBranch,
			&r.RootDir, &r.FrameworkOverride, &r.AutoDeploy,
			&r.Port, &r.Replicas, &r.Profile, &r.CreatedAt, &r.UpdatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan repo")
			return
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading repos")
		return
	}

	c.JSON(http.StatusOK, gin.H{"repos": repos})
}

type connectGitRepoRequest struct {
	InstallationID    string `json:"installation_id"`
	RepoFullName      string `json:"repo_full_name"`
	AppName           string `json:"app_name"`
	ProductionBranch  string `json:"production_branch"`
	RootDir           string `json:"root_dir"`
	FrameworkOverride string `json:"framework_override"`
	AutoDeploy        bool   `json:"auto_deploy"`
	// Intended app spec, applied by the first successful build when it creates the
	// app. Defaulted server-side when omitted (8080 / 2 / small).
	Port     int    `json:"port"`
	Replicas int    `json:"replicas"`
	Profile  string `json:"profile"`
	// GitLab only: a personal/project access token to store encrypted. Ignored for GitHub.
	Token string `json:"token"`
	// Provider defaults to github when an installation_id is supplied.
	Provider string `json:"provider"`
	CloneURL string `json:"clone_url"`
}

// ConnectGitRepo links a repository to an app in an environment.
//
// @ID          connectGitRepo
// @Summary     Link a git repository to an app
// @Description Links a GitHub/GitLab repository to an app in an environment so pushes trigger builds. Stores any GitLab token encrypted and generates a per-repo webhook secret. Requires write access.
// @Tags        git
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       body      body     connectGitRepoRequest true "Repo link specification"
// @Success     201       {object} map[string]interface{} "object with a repos array"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/repos [post]
func (h *Handler) ConnectGitRepo(c *gin.Context) {
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

	var req connectGitRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.RepoFullName == "" {
		respondError(c, http.StatusBadRequest, "repo_full_name is required")
		return
	}
	if req.AppName == "" {
		respondError(c, http.StatusBadRequest, "app_name is required")
		return
	}
	if err := validateKubeName(req.AppName); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	provider := req.Provider
	if provider == "" {
		provider = "github"
	}
	if provider != "github" && provider != "gitlab" {
		respondError(c, http.StatusBadRequest, "provider must be github or gitlab")
		return
	}
	if req.ProductionBranch == "" {
		req.ProductionBranch = "main"
	}
	if req.RootDir == "" {
		req.RootDir = "."
	}
	// Intended app spec (applied when the first build creates the app).
	if req.Port == 0 {
		req.Port = 8080
	}
	if req.Replicas == 0 {
		req.Replicas = 1
	}
	if req.Profile == "" {
		req.Profile = "small"
	}
	if req.Port < 1 || req.Port > 65535 {
		respondError(c, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}
	if req.Replicas < 1 || req.Replicas > 10 {
		respondError(c, http.StatusBadRequest, "replicas must be between 1 and 10")
		return
	}
	if req.Profile != "small" && req.Profile != "medium" && req.Profile != "large" {
		respondError(c, http.StatusBadRequest, "profile must be one of: small, medium, large")
		return
	}
	cloneURL := req.CloneURL
	if cloneURL == "" {
		cloneURL = "https://github.com/" + req.RepoFullName + ".git"
	}

	// Resolve the installation to a row visible to this project's org. Accept
	// EITHER the installation's id (UUID) OR its numeric GitHub installation id —
	// listGitInstallations surfaces both fields, and callers reasonably pass
	// whichever they see. Scoped by org (how installations are shared/listed), not
	// by the row's project_id, so an org-shared installation resolves correctly.
	var installationID *uuid.UUID
	if req.InstallationID != "" {
		var resolved uuid.UUID
		var qerr error
		if instUUID, perr := uuid.Parse(req.InstallationID); perr == nil {
			qerr = h.pool.QueryRow(c.Request.Context(),
				`SELECT gai.id FROM git_app_installations gai
				 JOIN projects p ON p.org_id = gai.org_id
				 WHERE gai.id = $1 AND p.id = $2`,
				instUUID, projectID,
			).Scan(&resolved)
		} else if numeric, nerr := strconv.ParseInt(req.InstallationID, 10, 64); nerr == nil {
			qerr = h.pool.QueryRow(c.Request.Context(),
				`SELECT gai.id FROM git_app_installations gai
				 JOIN projects p ON p.org_id = gai.org_id
				 WHERE gai.installation_id = $1 AND p.id = $2`,
				numeric, projectID,
			).Scan(&resolved)
		} else {
			respondError(c, http.StatusBadRequest, "installation_id must be the installation id (UUID) or its numeric GitHub installation id")
			return
		}
		if qerr == pgx.ErrNoRows {
			respondNotFound(c)
			return
		}
		if qerr != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify installation")
			return
		}
		installationID = &resolved
	}

	// GitLab token (optional) — store encrypted.
	var tokenEncrypted []byte
	if req.Token != "" {
		tokenEncrypted, err = crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(req.Token))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to encrypt token")
			return
		}
	}

	var frameworkOverride *string
	if req.FrameworkOverride != "" {
		frameworkOverride = &req.FrameworkOverride
	}

	webhookSecret := randomHex(32)

	var r gitRepo
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, installation_id, provider,
		    repo_full_name, clone_url, token_encrypted, webhook_secret,
		    production_branch, root_dir, framework_override, auto_deploy,
		    port, replicas, profile)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 RETURNING id, project_id, environment_id, app_name, installation_id, provider,
		           repo_full_name, production_branch, root_dir, framework_override,
		           auto_deploy, port, replicas, profile, created_at, updated_at`,
		projectID, envID, req.AppName, installationID, provider,
		req.RepoFullName, cloneURL, tokenEncrypted, webhookSecret,
		req.ProductionBranch, req.RootDir, frameworkOverride, req.AutoDeploy,
		req.Port, req.Replicas, req.Profile,
	)
	if err := row.Scan(&r.ID, &r.ProjectID, &r.EnvironmentID, &r.AppName,
		&r.InstallationID, &r.Provider, &r.RepoFullName, &r.ProductionBranch,
		&r.RootDir, &r.FrameworkOverride, &r.AutoDeploy,
		&r.Port, &r.Replicas, &r.Profile, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			respondError(c, http.StatusConflict, "this app already has a linked repository in this environment")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to link repository")
		return
	}

	// Best-effort audit.
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'ConnectGitRepo', 'GitRepo', $3)`,
		claims.UserID, projectID, req.AppName,
	)

	c.JSON(http.StatusCreated, gin.H{"repos": []gitRepo{r}})
}

// DisconnectGitRepo unlinks a repository from an app.
//
// @ID          disconnectGitRepo
// @Summary     Unlink a git repository
// @Description Removes the git link for an app. Builds/deployments already created are retained. Requires write access.
// @Tags        git
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       repoId    path     string true "Git repo UUID"
// @Success     204       {object} nil
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/repos/{repoId} [delete]
func (h *Handler) DisconnectGitRepo(c *gin.Context) {
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
	repoID, err := uuid.Parse(c.Param("repoId"))
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

	tag, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM git_repos WHERE id = $1 AND project_id = $2 AND environment_id = $3`,
		repoID, projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to unlink repository")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// randomHex returns n random bytes hex-encoded (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
