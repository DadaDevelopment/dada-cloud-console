package api

import (
	"net/http"
	"strconv"

	"github.com/dada-tuda/console/backend/internal/buildagent"
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
