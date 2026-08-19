package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/buildagent"
	gh "github.com/dada-tuda/console/backend/internal/github"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Agent-facing read-only git endpoints. These let a trusted service caller
// (agent_sync_hub, azp=dada-agent — same gate as the cloud-task webhook in
// webhooks_dadagent.go) discover the GitHub App installation(s), repos, and
// branches already connected to one of its projects, without a user session or
// project-membership row (the service account has none). Never expose these
// under the user JWT group: the auth model here is "trusted service, any
// project", not "this user can see this project".

// agentAuthorize verifies the bearer token is the agent's own client. Returns
// false (and has already written the response) when unauthorized/misconfigured.
func (h *Handler) agentAuthorize(c *gin.Context, verifier tokenVerifier) bool {
	header := c.GetHeader("Authorization")
	raw := stripBearer(header)
	if raw == "" {
		respondUnauthorized(c)
		return false
	}
	if verifier == nil {
		respondError(c, http.StatusServiceUnavailable, "agent auth not configured")
		return false
	}
	claims, err := verifier.Verify(c.Request.Context(), raw)
	if err != nil {
		respondUnauthorized(c)
		return false
	}
	if claims.Azp != "dada-agent" && !hasClient(claims, "dada-agent") {
		respondForbidden(c)
		return false
	}
	return true
}

func stripBearer(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	return ""
}

// AgentListGitInstallations lists the GitHub App installations bound to a
// project's org. GET /api/v1/agent/git/installations?project_id=<uuid>.
//
// @ID          agentListGitInstallations
// @Summary     [service] List git installations for a project
// @Description Trusted-service equivalent of ListGitInstallations, gated by azp=dada-agent instead of project membership.
// @Tags        agent-git
// @Produce     json
// @Security    BearerAuth
// @Param       project_id query    string true "Project UUID"
// @Success     200        {object} map[string]interface{} "object with an installations array"
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Router      /agent/git/installations [get]
func (h *Handler) AgentListGitInstallations(c *gin.Context) {
	if !h.agentAuthorize(c, h.agentVerifier) {
		return
	}
	projectID, err := uuid.Parse(c.Query("project_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid project_id")
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

// AgentListInstallationRepos lists the repos visible to one installation.
// GET /api/v1/agent/git/repos?project_id=<uuid>&installation_id=<uuid>.
//
// @ID          agentListInstallationRepos
// @Summary     [service] List repos visible to a git installation
// @Tags        agent-git
// @Produce     json
// @Security    BearerAuth
// @Param       project_id      query    string true "Project UUID"
// @Param       installation_id query    string true "Installation UUID"
// @Success     200             {object} map[string]interface{} "object with a repos array"
// @Failure     401             {object} map[string]string
// @Failure     403             {object} map[string]string
// @Failure     404             {object} map[string]string
// @Failure     503             {object} map[string]string
// @Router      /agent/git/repos [get]
func (h *Handler) AgentListInstallationRepos(c *gin.Context) {
	if !h.agentAuthorize(c, h.agentVerifier) {
		return
	}
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}

	providerInstallID, ok := h.resolveAgentInstallation(c)
	if !ok {
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

// AgentListInstallationBranches lists the branches of one repo visible to one
// installation. GET /api/v1/agent/git/branches?project_id=<uuid>&installation_id=<uuid>&repo=owner/name.
//
// @ID          agentListInstallationBranches
// @Summary     [service] List branches of a repo visible to a git installation
// @Tags        agent-git
// @Produce     json
// @Security    BearerAuth
// @Param       project_id      query    string true "Project UUID"
// @Param       installation_id query    string true "Installation UUID"
// @Param       repo            query    string true "Repository full name (org/repo)"
// @Success     200             {object} map[string]interface{} "object with a branches array"
// @Failure     401             {object} map[string]string
// @Failure     403             {object} map[string]string
// @Failure     404             {object} map[string]string
// @Failure     503             {object} map[string]string
// @Router      /agent/git/branches [get]
func (h *Handler) AgentListInstallationBranches(c *gin.Context) {
	if !h.agentAuthorize(c, h.agentVerifier) {
		return
	}
	if h.buildagent == nil {
		respondError(c, http.StatusServiceUnavailable, "git integration not configured")
		return
	}
	repo := c.Query("repo")
	if repo == "" {
		respondError(c, http.StatusBadRequest, "missing repo query param")
		return
	}

	providerInstallID, ok := h.resolveAgentInstallation(c)
	if !ok {
		return
	}

	branches, err := h.buildagent.ListBranches(c.Request.Context(), providerInstallID, repo)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to list branches from build-agent")
		return
	}
	if branches == nil {
		branches = []buildagent.RemoteBranch{}
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches})
}

// resolveAgentInstallation resolves the installation UUID query param to its
// numeric provider id, scoped to the requesting project_id's org (mirrors
// ListInstallationRepos's membership check, but by org match instead of a
// user's effective role).
func (h *Handler) resolveAgentInstallation(c *gin.Context) (int64, bool) {
	projectID, err := uuid.Parse(c.Query("project_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid project_id")
		return 0, false
	}
	installationUUID, err := uuid.Parse(c.Query("installation_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid installation_id")
		return 0, false
	}

	var providerInstallID int64
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT i.installation_id FROM git_app_installations i
		  JOIN projects p ON p.org_id = i.org_id
		 WHERE i.id = $1 AND p.id = $2`,
		installationUUID, projectID,
	).Scan(&providerInstallID)
	if err != nil {
		respondNotFound(c)
		return 0, false
	}
	return providerInstallID, true
}

// agentInstallTokenRequest asks for a GitHub credential for one repository of
// one project. installation_id may be omitted when the project's org has a
// single installation — the common case, and the hub should not have to carry an
// id it never chose.
type agentInstallTokenRequest struct {
	ProjectID      string `json:"project_id" binding:"required"`
	InstallationID string `json:"installation_id"`
	Repo           string `json:"repo" binding:"required"`
}

type agentInstallTokenResponse struct {
	Repo      string    `json:"repo"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AgentMintInstallToken issues a short-lived GitHub App installation token for
// one repository of one project. POST /api/v1/agent/git/install-token.
//
// This is the write counterpart of the read-only discovery above, and it exists
// so the hub stops asking a human to paste a token into the launch dialog. The
// token is narrowed to the single requested repository, so it is strictly weaker
// than the installation-wide token the platform's own cloud-task dispatch
// already hands the same caller.
//
// The repository is not checked against a table here: the narrowing is passed to
// GitHub, which refuses (422) anything the installation does not contain. That
// keeps one source of truth for "what this installation reaches" instead of a
// local copy able to drift from it.
//
// @ID          agentMintInstallToken
// @Summary     [service] Mint a repo-scoped git installation token
// @Description Trusted-service endpoint (azp=dada-agent): returns a short-lived GitHub App token scoped to one repository, so a run can be launched without a human-pasted credential.
// @Tags        agent-git
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     agentInstallTokenRequest true "project, repo, optional installation"
// @Success     200     {object} agentInstallTokenResponse
// @Failure     400     {object} map[string]string
// @Failure     401     {object} map[string]string
// @Failure     403     {object} map[string]string
// @Failure     404     {object} map[string]string
// @Failure     502     {object} map[string]string
// @Failure     503     {object} map[string]string
// @Router      /agent/git/install-token [post]
func (h *Handler) AgentMintInstallToken(c *gin.Context) {
	h.agentMintInstallToken(c, h.agentVerifier)
}

// agentMintInstallToken carries the verifier as an argument so the auth gate can
// be exercised without a live Keycloak, the same split the dadagent webhook uses.
func (h *Handler) agentMintInstallToken(c *gin.Context, verifier tokenVerifier) {
	if !h.agentAuthorize(c, verifier) {
		return
	}
	if h.cfg.GithubAppID == "" || h.cfg.GithubAppPrivateKey == "" {
		respondError(c, http.StatusServiceUnavailable, "git app not configured")
		return
	}

	var req agentInstallTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid project_id")
		return
	}
	repoName, ok := repoScopeName(req.Repo)
	if !ok {
		respondError(c, http.StatusBadRequest, "repo must be owner/name")
		return
	}

	providerInstallID, ok := h.agentInstallationFor(c, projectID, req.InstallationID)
	if !ok {
		return
	}

	token, expires, err := gh.MintInstallTokenForRepos(c.Request.Context(),
		h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, providerInstallID, []string{repoName})
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to mint install token")
		return
	}

	h.recordSystemAudit(c.Request.Context(), auditEntry{
		ProjectID:    projectID,
		Action:       "MintAgentInstallToken",
		ResourceKind: "GitInstallToken",
		ResourceName: req.Repo,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"expires_at": expires},
	})

	c.JSON(http.StatusOK, agentInstallTokenResponse{Repo: req.Repo, Token: token, ExpiresAt: expires})
}

// repoScopeName splits owner/name and returns the name GitHub expects in the
// token's repositories list. Unlike repoShortName it refuses anything that is
// not exactly one slash instead of falling back to the input: a name this
// function guessed wrong would be a token scoped to the wrong repository.
func repoScopeName(full string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(full), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// agentInstallationFor resolves the installation to mint against. An explicit
// id is checked against the project's org exactly like the read endpoints do.
// When it is omitted the project's org must have exactly one installation:
// picking one out of several would silently decide which account's credential
// the run gets.
func (h *Handler) agentInstallationFor(c *gin.Context, projectID uuid.UUID, installationID string) (int64, bool) {
	ctx := c.Request.Context()
	if installationID != "" {
		installationUUID, err := uuid.Parse(installationID)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid installation_id")
			return 0, false
		}
		var providerInstallID int64
		err = h.pool.QueryRow(ctx,
			`SELECT i.installation_id FROM git_app_installations i
			   JOIN projects p ON p.org_id = i.org_id
			  WHERE i.id = $1 AND p.id = $2`,
			installationUUID, projectID,
		).Scan(&providerInstallID)
		if err != nil {
			respondNotFound(c)
			return 0, false
		}
		return providerInstallID, true
	}

	rows, err := h.pool.Query(ctx,
		`SELECT i.installation_id FROM git_app_installations i
		   JOIN projects p ON p.org_id = i.org_id
		  WHERE p.id = $1`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query installations")
		return 0, false
	}
	defer rows.Close()

	var found []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan installation")
			return 0, false
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading installations")
		return 0, false
	}
	switch len(found) {
	case 0:
		respondNotFound(c)
		return 0, false
	case 1:
		return found[0], true
	default:
		respondError(c, http.StatusBadRequest, "project has several git installations: name installation_id")
		return 0, false
	}
}
