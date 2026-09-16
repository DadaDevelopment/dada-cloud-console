package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	gh "github.com/dada-tuda/console/backend/internal/github"
	"github.com/dada-tuda/console/backend/internal/kagent"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/promptsource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sync statuses of a prompt source. The agent's prompt and skills are read
// from a directory in the client's git repository instead of being pasted into
// saveAgent; the design, the repository format and the reasons for the choices
// in this file are in docs/agent-prompt-source.md.
const (
	promptSourceStatusPending = "pending"
	promptSourceStatusOK      = "ok"
	promptSourceStatusError   = "error"
)

const lockKeyAgentPromptSource int64 = 0x64616461_0012

// agentPromptSourceRow is one agent_prompt_sources row.
type agentPromptSourceRow struct {
	ProjectID      uuid.UUID
	EnvironmentID  uuid.UUID
	AgentName      string
	InstallationID uuid.UUID
	RepoFullName   string
	Ref            string
	Path           string
	ResolvedSHA    string
	SyncedAt       *time.Time
	LastCheckedAt  *time.Time
	LastSyncStatus string
	LastSyncError  string
	Prompt         string
	PromptTitle    string
	PromptVersion  string
	Skills         map[string]string
}

// AgentPromptSourceSkill is one synced skill file as the console shows it.
type AgentPromptSourceSkill struct {
	Name    string `json:"name"`
	Bytes   int    `json:"bytes"`
	Content string `json:"content"`
}

// AgentPromptSource is the prompt source of one agent as the console shows it.
type AgentPromptSource struct {
	AgentName      string                   `json:"agent_name"`
	InstallationID string                   `json:"installation_id"`
	RepoFullName   string                   `json:"repo_full_name"`
	Ref            string                   `json:"ref"`
	Path           string                   `json:"path"`
	ResolvedSHA    string                   `json:"resolved_sha"`
	SyncedAt       *time.Time               `json:"synced_at"`
	LastCheckedAt  *time.Time               `json:"last_checked_at"`
	LastSyncStatus string                   `json:"last_sync_status"`
	LastSyncError  string                   `json:"last_sync_error"`
	Prompt         string                   `json:"prompt"`
	PromptTitle    string                   `json:"prompt_title"`
	PromptVersion  string                   `json:"prompt_version"`
	PromptBytes    int                      `json:"prompt_bytes"`
	Skills         []AgentPromptSourceSkill `json:"skills"`
}

func (r agentPromptSourceRow) view() AgentPromptSource {
	skills := make([]AgentPromptSourceSkill, 0, len(r.Skills))
	for name, content := range r.Skills {
		skills = append(skills, AgentPromptSourceSkill{Name: name, Bytes: len(content), Content: content})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return AgentPromptSource{
		AgentName:      r.AgentName,
		InstallationID: r.InstallationID.String(),
		RepoFullName:   r.RepoFullName,
		Ref:            r.Ref,
		Path:           r.Path,
		ResolvedSHA:    r.ResolvedSHA,
		SyncedAt:       r.SyncedAt,
		LastCheckedAt:  r.LastCheckedAt,
		LastSyncStatus: r.LastSyncStatus,
		LastSyncError:  r.LastSyncError,
		Prompt:         r.Prompt,
		PromptTitle:    r.PromptTitle,
		PromptVersion:  r.PromptVersion,
		PromptBytes:    len(r.Prompt),
		Skills:         skills,
	}
}

// setAgentPromptSourceRequest attaches a repository directory to an agent.
type setAgentPromptSourceRequest struct {
	RepoFullName   string `json:"repo_full_name" binding:"required" example:"DadaDevelopment/tg-agent-tools"`
	Ref            string `json:"ref" example:"main"`
	Path           string `json:"path" example:"agents/tg-exchange-support"`
	InstallationID string `json:"installation_id" example:"c0ffee00-0000-4000-8000-000000000000"`
}

// syncAgentPromptSourceRequest is the body of a manual sync.
type syncAgentPromptSourceRequest struct {
	Force bool `json:"force"`
}

// syncAgentPromptSourceResult is what one sync did.
type syncAgentPromptSourceResult struct {
	Changed     bool   `json:"changed"`
	SHA         string `json:"sha"`
	Version     string `json:"version"`
	Files       int    `json:"files"`
	Bytes       int    `json:"bytes"`
	OperationID string `json:"operation_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

const promptSourceColumns = `project_id, environment_id, agent_name, installation_id, repo_full_name, ref, path,
	resolved_sha, synced_at, last_checked_at, last_sync_status, last_sync_error,
	prompt, prompt_title, prompt_version, skills`

func scanPromptSource(row pgx.Row) (agentPromptSourceRow, error) {
	var r agentPromptSourceRow
	err := row.Scan(&r.ProjectID, &r.EnvironmentID, &r.AgentName, &r.InstallationID, &r.RepoFullName, &r.Ref, &r.Path,
		&r.ResolvedSHA, &r.SyncedAt, &r.LastCheckedAt, &r.LastSyncStatus, &r.LastSyncError,
		&r.Prompt, &r.PromptTitle, &r.PromptVersion, &r.Skills)
	return r, err
}

func (h *Handler) loadPromptSource(ctx context.Context, projectID, envID uuid.UUID, name string) (agentPromptSourceRow, bool, error) {
	r, err := scanPromptSource(h.pool.QueryRow(ctx,
		`SELECT `+promptSourceColumns+` FROM agent_prompt_sources
		 WHERE project_id = $1 AND environment_id = $2 AND agent_name = $3`,
		projectID, envID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return agentPromptSourceRow{}, false, nil
	}
	if err != nil {
		return agentPromptSourceRow{}, false, err
	}
	return r, true, nil
}

// promptSourceOverride hands saveAgent the synced prompt when the agent has a
// source, so a hand edit or an MCP call cannot desync the claim from the repo.
func (h *Handler) promptSourceOverride(ctx context.Context, projectID, envID uuid.UUID, name string) (prompt, version string, ok bool) {
	r, found, err := h.loadPromptSource(ctx, projectID, envID, name)
	if err != nil || !found || r.SyncedAt == nil {
		return "", "", false
	}
	return r.Prompt, r.PromptVersion, true
}

// GetAgentPromptSource returns the repository the agent's prompt and skills are synced from.
//
// @ID          getAgentPromptSource
// @Summary     Get an agent's prompt source
// @Description Returns the repository, branch and directory the agent's prompt and skills are read from, the last synced commit, the parsed prompt with its version, the skills with their sizes, and the status of the last sync. 404 when the agent has no source and its prompt is edited by hand.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Agent name"
// @Success     200       {object} AgentPromptSource
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents/{name}/prompt-source [get]
func (h *Handler) GetAgentPromptSource(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	r, found, err := h.loadPromptSource(c.Request.Context(), projectID, envID, c.Param("name"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load prompt source")
		return
	}
	if !found {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, r.view())
}

// SetAgentPromptSource points an agent at a directory in a git repository and runs the first sync.
//
// @ID          setAgentPromptSource
// @Summary     Attach a git prompt source to an agent
// @Description Stores the repository (owner/name), branch (default main) and directory (default agents/<name>) the agent's prompt and skills are read from, then syncs once. The directory must hold core.md with a "# title, middle dot, version" first line and domains/*.md skills of at most 8192 bytes each; see docs/agent-prompt-source.md. The agent must already exist as a console-managed agent: the sync writes only the prompt into the claim and keeps model, tools and env as they are. While a source is attached the prompt cannot be edited by hand. Requires write access.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                      true "Project UUID"
// @Param       envId     path     string                      true "Environment UUID"
// @Param       name      path     string                      true "Agent name"
// @Param       body      body     setAgentPromptSourceRequest true "Repository, branch and directory"
// @Success     200       {object} map[string]interface{} "source and sync result"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents/{name}/prompt-source [put]
func (h *Handler) SetAgentPromptSource(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")
	if err := kagent.ValidateName(name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	if h.cfg.GithubAppID == "" || h.cfg.GithubAppPrivateKey == "" {
		respondError(c, http.StatusServiceUnavailable, "git app not configured")
		return
	}
	var req setAgentPromptSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	repo := strings.TrimSpace(req.RepoFullName)
	if _, ok := repoScopeName(repo); !ok {
		respondError(c, http.StatusBadRequest, "repo_full_name must be owner/name")
		return
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		ref = "main"
	}
	dir := strings.Trim(strings.TrimSpace(req.Path), "/")
	if dir == "" {
		dir = "agents/" + name
	}
	if strings.Contains(dir, "..") {
		respondError(c, http.StatusBadRequest, "path must be a directory inside the repository")
		return
	}

	ctx := c.Request.Context()
	kind, err := h.agentSnapshotKind(ctx, projectID, envID, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to look up agent")
		return
	}
	if kind != managedAgentKind {
		respondErrorCode(c, http.StatusConflict, "agent_not_console_owned",
			"save the agent in the console first: the sync writes only the prompt into an existing agent and would otherwise create one without a model or tools")
		return
	}

	installationID, ok := h.promptSourceInstallation(c, projectID, repo, req.InstallationID)
	if !ok {
		return
	}

	_, err = h.pool.Exec(ctx,
		`INSERT INTO agent_prompt_sources (project_id, environment_id, agent_name, installation_id, repo_full_name, ref, path, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (project_id, environment_id, agent_name) DO UPDATE SET
		   installation_id = EXCLUDED.installation_id, repo_full_name = EXCLUDED.repo_full_name,
		   ref = EXCLUDED.ref, path = EXCLUDED.path, resolved_sha = '',
		   last_sync_status = 'pending', last_sync_error = '', updated_at = NOW()`,
		projectID, envID, name, installationID, repo, ref, dir, claims.UserID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to store prompt source")
		return
	}
	h.recordAudit(ctx, claims.UserID, auditEntry{
		ProjectID: projectID, EnvironmentID: envID, Action: "SetAgentPromptSource",
		ResourceKind: "ManagedAgent", ResourceName: name, Outcome: auditOutcomeSuccess,
		Metadata: map[string]any{"repo": repo, "ref": ref, "path": dir},
	})

	row, _, err := h.loadPromptSource(ctx, projectID, envID, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load prompt source")
		return
	}
	result := h.syncPromptSource(ctx, row, true, claims.UserID)
	row, _, err = h.loadPromptSource(ctx, projectID, envID, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load prompt source")
		return
	}
	c.JSON(http.StatusOK, gin.H{"source": row.view(), "sync": result})
}

// promptSourceInstallation resolves the installation that can read the repo:
// the one the caller named, or the single installation of the repo owner.
func (h *Handler) promptSourceInstallation(c *gin.Context, projectID uuid.UUID, repo, requested string) (uuid.UUID, bool) {
	if requested != "" {
		id, err := uuid.Parse(requested)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid installation_id")
			return uuid.Nil, false
		}
		var n int
		err = h.pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM git_app_installations i JOIN projects p ON p.org_id = i.org_id
			 WHERE i.id = $1 AND p.id = $2 AND i.provider = 'github'`, id, projectID).Scan(&n)
		if err != nil || n == 0 {
			respondError(c, http.StatusNotFound, "installation not found in this project's organization")
			return uuid.Nil, false
		}
		return id, true
	}
	id, ok := h.resolveInstallationByOwner(c.Request.Context(), projectID, repo)
	if !ok {
		respondError(c, http.StatusNotFound, "no git app installation for the repository owner; install the app on that account or pass installation_id")
		return uuid.Nil, false
	}
	return id, true
}

// SyncAgentPromptSource re-reads the repository now.
//
// @ID          syncAgentPromptSource
// @Summary     Sync an agent's prompt and skills from git now
// @Description Resolves the branch head, and when it moved (or force is set) downloads core.md and domains/*.md, validates them, stores them and queues the prompt into the agent claim. Idempotent: an unchanged head only updates the checked-at time. Requires write access.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                       true  "Project UUID"
// @Param       envId     path     string                       true  "Environment UUID"
// @Param       name      path     string                       true  "Agent name"
// @Param       body      body     syncAgentPromptSourceRequest false "Set force to re-queue the claim even when the commit did not change"
// @Success     200       {object} map[string]interface{} "source and sync result"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents/{name}/prompt-source/sync [post]
func (h *Handler) SyncAgentPromptSource(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	var req syncAgentPromptSourceRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	ctx := c.Request.Context()
	row, found, err := h.loadPromptSource(ctx, projectID, envID, c.Param("name"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load prompt source")
		return
	}
	if !found {
		respondNotFound(c)
		return
	}
	result := h.syncPromptSource(ctx, row, req.Force, claims.UserID)
	row, _, err = h.loadPromptSource(ctx, projectID, envID, row.AgentName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load prompt source")
		return
	}
	c.JSON(http.StatusOK, gin.H{"source": row.view(), "sync": result})
}

// DeleteAgentPromptSource detaches the repository from the agent.
//
// @ID          deleteAgentPromptSource
// @Summary     Detach an agent's prompt source
// @Description Stops syncing. The last synced prompt stays in the agent claim and the prompt becomes editable by hand again; skills served from the source are gone on the next turn. Requires write access.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Agent name"
// @Success     204       "detached"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents/{name}/prompt-source [delete]
func (h *Handler) DeleteAgentPromptSource(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")
	tag, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM agent_prompt_sources WHERE project_id = $1 AND environment_id = $2 AND agent_name = $3`,
		projectID, envID, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to detach prompt source")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID: projectID, EnvironmentID: envID, Action: "DeleteAgentPromptSource",
		ResourceKind: "ManagedAgent", ResourceName: name, Outcome: auditOutcomeSuccess,
	})
	c.Status(http.StatusNoContent)
}

// installTokenCache keeps one token per installation until shortly before it
// expires, so the poller does not mint a token per agent per tick.
type installTokenCache struct {
	mu     sync.Mutex
	tokens map[int64]cachedInstallToken
}

type cachedInstallToken struct {
	token   string
	expires time.Time
}

var promptSourceTokens = &installTokenCache{tokens: map[int64]cachedInstallToken{}}

// promptSourceMintToken is swapped by tests; it is gh.MintInstallTokenForRepos in production.
var promptSourceMintToken = gh.MintInstallTokenForRepos

func (h *Handler) promptSourceToken(ctx context.Context, installationID uuid.UUID, repo string) (string, error) {
	var providerInstallID int64
	if err := h.pool.QueryRow(ctx,
		`SELECT installation_id FROM git_app_installations WHERE id = $1`, installationID).Scan(&providerInstallID); err != nil {
		return "", fmt.Errorf("installation no longer exists; reconnect the git app")
	}
	repoName, ok := repoScopeName(repo)
	if !ok {
		return "", fmt.Errorf("repository must be owner/name")
	}
	promptSourceTokens.mu.Lock()
	defer promptSourceTokens.mu.Unlock()
	if cached, ok := promptSourceTokens.tokens[providerInstallID]; ok && time.Until(cached.expires) > 5*time.Minute {
		return cached.token, nil
	}
	token, expires, err := promptSourceMintToken(ctx, h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, providerInstallID, []string{repoName})
	if err != nil {
		return "", fmt.Errorf("git app could not read %s: %w", repo, err)
	}
	promptSourceTokens.tokens[providerInstallID] = cachedInstallToken{token: token, expires: expires}
	return token, nil
}

// syncPromptSource is one sync of one agent: resolve head, skip when nothing
// moved, otherwise fetch, validate, store and queue the prompt into the claim.
// It never returns an error: the outcome lands in the row for the UI and in
// the result for the caller, and a failed sync leaves the last good prompt in
// force.
func (h *Handler) syncPromptSource(ctx context.Context, row agentPromptSourceRow, force bool, actorID uuid.UUID) syncAgentPromptSourceResult {
	fail := func(err error) syncAgentPromptSourceResult {
		msg := err.Error()
		_, dbErr := h.pool.Exec(ctx,
			`UPDATE agent_prompt_sources SET last_checked_at = NOW(), last_sync_status = 'error', last_sync_error = $4, updated_at = NOW()
			 WHERE project_id = $1 AND environment_id = $2 AND agent_name = $3`,
			row.ProjectID, row.EnvironmentID, row.AgentName, msg)
		if dbErr != nil {
			log.Printf("prompt source %s: record error: %v", row.AgentName, dbErr)
		}
		log.Printf("prompt source sync agent=%s repo=%s ref=%s status=error err=%q", row.AgentName, row.RepoFullName, row.Ref, msg)
		h.writeAudit(ctx, promptSourceActor(actorID), auditEntry{
			ProjectID: row.ProjectID, EnvironmentID: row.EnvironmentID, Action: "SyncAgentPromptSource",
			ResourceKind: "ManagedAgent", ResourceName: row.AgentName, Outcome: auditOutcomeFailure,
			Metadata: map[string]any{"repo": row.RepoFullName, "ref": row.Ref, "error": msg},
		})
		return syncAgentPromptSourceResult{SHA: row.ResolvedSHA, Version: row.PromptVersion, Error: msg}
	}

	token, err := h.promptSourceToken(ctx, row.InstallationID, row.RepoFullName)
	if err != nil {
		return fail(err)
	}
	fetcher := promptsource.Fetcher{Token: token}
	sha, err := fetcher.HeadSHA(ctx, row.RepoFullName, row.Ref)
	if err != nil {
		return fail(err)
	}
	if !force && sha == row.ResolvedSHA && row.LastSyncStatus == promptSourceStatusOK {
		_, err = h.pool.Exec(ctx,
			`UPDATE agent_prompt_sources SET last_checked_at = NOW() WHERE project_id = $1 AND environment_id = $2 AND agent_name = $3`,
			row.ProjectID, row.EnvironmentID, row.AgentName)
		if err != nil {
			log.Printf("prompt source %s: record check: %v", row.AgentName, err)
		}
		return syncAgentPromptSourceResult{SHA: sha, Version: row.PromptVersion, Files: 1 + len(row.Skills)}
	}

	files, err := fetcher.Fetch(ctx, row.RepoFullName, sha, row.Path)
	if err != nil {
		return fail(err)
	}
	bundle, err := promptsource.Build(files, sha)
	if err != nil {
		return fail(err)
	}
	skillsJSON, err := json.Marshal(bundle.SkillMap())
	if err != nil {
		return fail(err)
	}

	payload, err := json.Marshal(models.SaveAgentPayload{Name: row.AgentName, Prompt: bundle.Prompt, PromptVersion: bundle.Version})
	if err != nil {
		return fail(err)
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fail(fmt.Errorf("failed to store the sync"))
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx,
		`UPDATE agent_prompt_sources SET resolved_sha = $4, synced_at = NOW(), last_checked_at = NOW(),
		   last_sync_status = 'ok', last_sync_error = '', prompt = $5, prompt_title = $6, prompt_version = $7,
		   skills = $8, updated_at = NOW()
		 WHERE project_id = $1 AND environment_id = $2 AND agent_name = $3`,
		row.ProjectID, row.EnvironmentID, row.AgentName, sha, bundle.Prompt, bundle.Title, bundle.Version, skillsJSON)
	if err != nil {
		return fail(fmt.Errorf("failed to store the sync"))
	}
	var opID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'UpdateAgent', 'ManagedAgent', $4, 'Created', $5) RETURNING id`,
		promptSourceActor(actorID), row.ProjectID, row.EnvironmentID, row.AgentName, payload).Scan(&opID)
	if err != nil {
		return fail(fmt.Errorf("failed to queue the prompt update"))
	}
	if err = tx.Commit(ctx); err != nil {
		return fail(fmt.Errorf("failed to store the sync"))
	}

	fileCount := 1 + len(bundle.Skills)
	log.Printf("prompt source sync agent=%s repo=%s ref=%s sha=%s version=%s files=%d bytes=%d op=%s",
		row.AgentName, row.RepoFullName, row.Ref, sha, bundle.Version, fileCount, bundle.Bytes(), opID)
	h.writeAudit(ctx, promptSourceActor(actorID), auditEntry{
		ProjectID: row.ProjectID, EnvironmentID: row.EnvironmentID, OperationID: opID, Action: "SyncAgentPromptSource",
		ResourceKind: "ManagedAgent", ResourceName: row.AgentName, Outcome: auditOutcomeSuccess,
		Metadata: map[string]any{"repo": row.RepoFullName, "ref": row.Ref, "sha": sha, "prompt_version": bundle.Version,
			"files": fileCount, "bytes": bundle.Bytes()},
	})
	return syncAgentPromptSourceResult{Changed: true, SHA: sha, Version: bundle.Version, Files: fileCount, Bytes: bundle.Bytes(), OperationID: opID.String()}
}

func promptSourceActor(actorID uuid.UUID) uuid.UUID {
	if actorID == uuid.Nil {
		return systemDeployActorID
	}
	return actorID
}

// RunAgentPromptSourceTick syncs every attached source once. The poller in
// cmd/server calls it on AGENT_PROMPT_SOURCE_POLL_INTERVAL_SECS; one replica
// at a time holds the advisory lock.
func (h *Handler) RunAgentPromptSourceTick(ctx context.Context) {
	if h.cfg.GithubAppID == "" || h.cfg.GithubAppPrivateKey == "" {
		return
	}
	runWithAdvisoryLock(ctx, h.pool, lockKeyAgentPromptSource, "agent-prompt-source", func(ctx context.Context) {
		rows, err := h.pool.Query(ctx, `SELECT `+promptSourceColumns+` FROM agent_prompt_sources ORDER BY agent_name`)
		if err != nil {
			log.Printf("prompt source poll: list: %v", err)
			return
		}
		var sources []agentPromptSourceRow
		for rows.Next() {
			r, err := scanPromptSource(rows)
			if err != nil {
				rows.Close()
				log.Printf("prompt source poll: scan: %v", err)
				return
			}
			sources = append(sources, r)
		}
		rows.Close()
		for _, r := range sources {
			if ctx.Err() != nil {
				return
			}
			h.syncPromptSource(ctx, r, false, uuid.Nil)
		}
	})
}
