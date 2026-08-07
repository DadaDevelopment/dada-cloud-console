package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/dadagent"
	gh "github.com/dada-tuda/console/backend/internal/github"
)

type createCloudTaskRequest struct {
	TaskType string            `json:"task_type"`
	Params   map[string]string `json:"params"`
}

// CreateCloudTask mints repo + agent params, submits + executes a DadaAgent intent.
//
// @ID          createCloudTask
// @Summary     Fire a DadaAgent cloud task for an app
// @Description Mints a GitHub install token + agent params, submits and executes a DadaAgent intent. Write role required.
// @Tags        cloud-tasks
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       appName   path     string                 true "App name"
// @Param       body      body     createCloudTaskRequest true "Task type to run"
// @Success     202       {object} map[string]interface{} "object with the created cloud_task"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/cloud-tasks [post]
func (h *Handler) CreateCloudTask(c *gin.Context) {
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

	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateCloudTask",
			ResourceKind:  "CloudTask",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	reject := func(status int, reason string, extra map[string]any) {
		meta := map[string]any{"reason": reason, "status": status}
		for k, v := range extra {
			meta[k] = v
		}
		audit(auditOutcomeFailure, meta)
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "not_a_member", nil)
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "membership_check_failed", nil)
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		reject(http.StatusForbidden, "read_only_role", nil)
		respondForbidden(c)
		return
	}
	if h.dadagent == nil {
		reject(http.StatusServiceUnavailable, "dadagent_unavailable", nil)
		respondError(c, http.StatusServiceUnavailable, "dadagent integration not configured")
		return
	}

	var req createCloudTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		reject(http.StatusBadRequest, "malformed_body", nil)
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	entry, ok := cloudtask.Lookup(req.TaskType)
	if !ok {
		reject(http.StatusBadRequest, "unknown_task_type", map[string]any{"task_type": req.TaskType})
		respondError(c, http.StatusBadRequest, "unknown task_type")
		return
	}

	repo, instID, gitRepoID, err := h.resolveGitRepo(c.Request.Context(), projectID, envID, appName)
	if err != nil {
		reject(http.StatusBadRequest, "no_connected_repo", map[string]any{"task_type": entry.TaskType})
		respondError(c, http.StatusBadRequest, "no connected git repo for app")
		return
	}

	token, _, err := gh.MintInstallToken(c.Request.Context(), h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, instID)
	if err != nil {
		reject(http.StatusBadGateway, "install_token_failed", map[string]any{"task_type": entry.TaskType, "repo": repo})
		respondError(c, http.StatusBadGateway, "failed to mint install token")
		return
	}
	cfg := cloudtask.ResolverCfg{
		ProjectType:   "front",
		PublicBaseURL: h.cfg.PublicBaseURL,
	}
	if entry.NeedsCounter {
		counterID, err := h.counters.Resolve(c.Request.Context(), appName)
		if err != nil {
			reject(http.StatusFailedDependency, "counter_unresolved", map[string]any{"task_type": entry.TaskType})
			respondError(c, http.StatusFailedDependency, err.Error())
			return
		}
		cfg.CounterID = counterID
	}
	params, err := entry.ResolveParams(cfg)
	if err != nil {
		reject(http.StatusFailedDependency, "params_unresolved", map[string]any{"task_type": entry.TaskType})
		respondError(c, http.StatusFailedDependency, err.Error())
		return
	}
	params = cloudtask.MergeClientParams(entry, params, req.Params)

	intentID := uuid.NewString()
	gitRepoUUID := gitRepoID
	row, err := h.insertCloudTask(c.Request.Context(), cloudTaskInsert{
		ProjectID: projectID, EnvironmentID: envID, AppName: appName,
		GitRepoID: &gitRepoUUID, TaskType: entry.TaskType, IntentID: intentID,
		ActorID: claims.UserID,
	})
	if err != nil {
		reject(http.StatusInternalServerError, "cloud_task_insert_failed", map[string]any{"task_type": entry.TaskType})
		respondError(c, http.StatusInternalServerError, "failed to record cloud task")
		return
	}

	in := dadagent.IntentRequest{
		IntentID:          intentID,
		Summary:           entry.Summary + " (" + appName + ")",
		TaskType:          "docs",
		CoreLoopImpact:    "Cloud-fired task to improve " + appName + " growth instrumentation.",
		PrimaryPillar:     "SPD",
		VisiblePrimitives: []string{"intents"},
		KPIHypothesis:     []string{"orchestration_success_rate"},
		CloudPayload: map[string]any{
			"cloud_task_id": row.ID,
			"skill_id":      entry.SkillID,
			"repo":          map[string]any{"provider": "github", "full_name": repo, "install_token": token},
			"params":        params,
			"callback":      map[string]any{"url": h.cfg.CloudTaskCallbackURL},
		},
	}

	go h.runCloudTaskIntent(intentID, in)

	audit(auditOutcomeSuccess, map[string]any{
		"cloud_task_id": row.ID,
		"task_type":     entry.TaskType,
		"intent_id":     intentID,
		"repo":          repo,
	})

	c.JSON(http.StatusAccepted, gin.H{"cloud_task": row})
}

// runCloudTaskIntent submits then executes a DadaAgent intent off the HTTP
// request path. The cloud_tasks row already exists in 'running'; the agent's
// callback webhook (/api/v1/webhooks/dadagent) drives the row to its terminal
// state. Detached context: the request is already answered (202) and the agent's
// readiness gate can outlive any request deadline. Only a hard submit/execute
// failure (no callback will ever arrive) flips the row to 'failed'.
func (h *Handler) runCloudTaskIntent(intentID string, in dadagent.IntentRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := h.dadagent.SubmitIntent(ctx, in)
	if err != nil {
		log.Printf("cloud-task: intent %s submit failed: %v", intentID, err)
		_, _ = h.updateCloudTaskByIntent(ctx, intentID, "failed", "", nil, err.Error())
		return
	}
	_ = h.setCloudTaskWorkflow(ctx, intentID, res.WorkflowID)
	if err := h.dadagent.ExecuteIntent(ctx, intentID); err != nil {
		log.Printf("cloud-task: intent %s execute failed: %v", intentID, err)
		_, _ = h.updateCloudTaskByIntent(ctx, intentID, "failed", "", nil, err.Error())
		return
	}
}

// ListCloudTasks returns the cloud tasks for one app, newest first.
//
// @ID          listCloudTasks
// @Summary     List cloud tasks for an app
// @Description Returns the DadaAgent cloud tasks fired for an app, newest first.
// @Tags        cloud-tasks
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with a cloud_tasks array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/cloud-tasks [get]
func (h *Handler) ListCloudTasks(c *gin.Context) {
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
		respondError(c, http.StatusInternalServerError, "membership check failed")
		return
	}
	tasks, err := h.listCloudTasks(c.Request.Context(), projectID, c.Param("appName"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "list failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"cloud_tasks": tasks})
}

// GetCloudTask returns one cloud task by id.
//
// @ID          getCloudTask
// @Summary     Get a cloud task by id
// @Description Returns a single DadaAgent cloud task.
// @Tags        cloud-tasks
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       taskId    path     string true "Cloud task UUID"
// @Success     200       {object} map[string]interface{} "object with the cloud_task"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/cloud-tasks/{taskId} [get]
func (h *Handler) GetCloudTask(c *gin.Context) {
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
	}
	id, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	t, err := h.getCloudTask(c.Request.Context(), id)
	if err != nil {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cloud_task": t})
}

// ProxyCloudTaskArtifact streams an artifact from the agent (source of truth).
//
// @ID          proxyCloudTaskArtifact
// @Summary     Download a cloud task artifact
// @Description Streams an artifact file produced by the agent for a cloud task.
// @Tags        cloud-tasks
// @Produce     application/octet-stream
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       taskId    path     string true "Cloud task UUID"
// @Param       fileId    path     string true "Agent file id"
// @Success     200       {file}   binary
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/cloud-tasks/{taskId}/artifacts/{fileId} [get]
func (h *Handler) ProxyCloudTaskArtifact(c *gin.Context) {
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
	}
	if h.dadagent == nil {
		respondError(c, http.StatusServiceUnavailable, "agent not configured")
		return
	}
	body, ctype, err := h.dadagent.GetFile(c.Request.Context(), c.Param("fileId"))
	if err != nil {
		respondError(c, http.StatusBadGateway, "artifact fetch failed")
		return
	}
	defer body.Close()
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", ctype)
	_, _ = io.Copy(c.Writer, body)
}
