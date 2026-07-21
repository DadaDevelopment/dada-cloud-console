package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/dadagent"
	gh "github.com/dada-tuda/console/backend/internal/github"
)

type autofixRequest struct {
	Error string `json:"error"`
}

// TriggerAutofix fires a DadaAgent auto-fix run for an app's linked repo: mints
// a repo-scoped install token, launches the run, and persists a cloud_tasks row
// (task_type=autofix) keyed by the agent's run-assigned cloud_task_id so the
// existing DadaAgent webhook can drive it to completion. User-initiated only
// (an explicit "Auto-fix with AI" action) -- nothing calls this automatically.
// Write role required.
//
// @ID          triggerAutofix
// @Summary     Auto-fix a reported issue with AI
// @Description Launches a DadaAgent auto-fix run against the app's linked repo: root-causes the supplied error (or the app's latest failed build when omitted), opens a PR, and reports back via the existing DadaAgent webhook. Write role required.
// @Tags        cloud-tasks
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string         true  "Project UUID"
// @Param       envId     path     string         true  "Environment UUID"
// @Param       appName   path     string         true  "App name"
// @Param       body      body     autofixRequest false "Error/failure context to fix; falls back to the latest failed build when omitted"
// @Success     202       {object} map[string]interface{} "object with the created cloud_task"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/autofix [post]
func (h *Handler) TriggerAutofix(c *gin.Context) {
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
	if h.dadagent == nil {
		respondError(c, http.StatusServiceUnavailable, "dadagent integration not configured")
		return
	}

	var req autofixRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	errText := req.Error
	if errText == "" {
		errText, err = h.latestFailedBuildSummary(c.Request.Context(), envID, appName)
		if err != nil {
			respondError(c, http.StatusBadRequest, "no error supplied and no failed build found for this app")
			return
		}
	}

	repo, instID, gitRepoID, err := h.resolveGitRepo(c.Request.Context(), projectID, envID, appName)
	if err != nil {
		respondError(c, http.StatusBadRequest, "no connected git repo for app")
		return
	}

	token, _, err := gh.MintInstallToken(c.Request.Context(), h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, instID)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to mint install token")
		return
	}

	res, err := h.dadagent.Autofix(c.Request.Context(), dadagent.AutofixRequest{
		RepoFullName: repo,
		InstallToken: token,
		Error:        errText,
		CallbackURL:  h.cfg.CloudTaskCallbackURL,
	})
	if err != nil {
		log.Printf("autofix: app %s (project %s) launch failed: %v", appName, projectID, err)
		respondError(c, http.StatusBadGateway, "failed to launch auto-fix run")
		return
	}

	cloudTaskID := ""
	if info, err := h.dadagent.GetRun(c.Request.Context(), res.RunID); err != nil {
		log.Printf("autofix: run %s: failed to read back cloud_task_id, webhook updates will not match this row: %v", res.RunID, err)
	} else {
		cloudTaskID = info.CloudTaskID
	}

	gitRepoUUID := gitRepoID
	row, err := h.insertCloudTask(c.Request.Context(), cloudTaskInsert{
		ProjectID: projectID, EnvironmentID: envID, AppName: appName,
		GitRepoID: &gitRepoUUID, TaskType: "autofix",
		IntentID: cloudTaskID, WorkflowID: res.RunID,
		ActorID: claims.UserID,
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record cloud task")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"cloud_task": row})
}

// formatBuildFailureSummary renders a short natural-language failure summary
// from build fields -- the auto-fix error context used when the caller does
// not supply one explicitly.
func formatBuildFailureSummary(branch, commitSHA string, commitMessage *string, finishedAt *time.Time) string {
	summary := fmt.Sprintf("Build failed on branch %s (commit %s)", branch, shortSHA(commitSHA))
	if commitMessage != nil && *commitMessage != "" {
		summary += ": " + *commitMessage
	}
	if finishedAt != nil {
		summary += " [failed at " + finishedAt.UTC().Format(time.RFC3339) + "]"
	}
	return summary
}

// shortSHA trims a commit sha to a readable prefix.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// latestFailedBuildSummary looks up the app's most recent failed build and
// summarizes it as auto-fix error context.
func (h *Handler) latestFailedBuildSummary(ctx context.Context, envID uuid.UUID, appName string) (string, error) {
	var branch, commitSHA string
	var commitMessage *string
	var finishedAt *time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT branch, commit_sha, commit_message, finished_at
		   FROM builds
		  WHERE environment_id = $1 AND app_name = $2 AND status = 'failed'
		  ORDER BY created_at DESC LIMIT 1`,
		envID, appName).Scan(&branch, &commitSHA, &commitMessage, &finishedAt)
	if err != nil {
		return "", err
	}
	return formatBuildFailureSummary(branch, commitSHA, commitMessage, finishedAt), nil
}
