package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/dadagent"
	gh "github.com/dada-tuda/console/backend/internal/github"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/models"
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

	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "TriggerAutofix",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	reject := func(status int, reason string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "not_a_member")
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "membership_check_failed")
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		reject(http.StatusForbidden, "read_only_role")
		respondForbidden(c)
		return
	}

	var req autofixRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		reject(http.StatusBadRequest, "malformed_body")
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	errText := req.Error
	suppliedError := errText != ""
	if errText == "" {
		errText, err = h.latestFailedBuildSummary(c.Request.Context(), envID, appName)
		if err != nil {
			reject(http.StatusBadRequest, "no_error_context")
			respondError(c, http.StatusBadRequest, "no error supplied and no failed build found for this app")
			return
		}
	}

	row, err := h.launchAutofix(c.Request.Context(), autofixLaunch{
		ProjectID: projectID, EnvID: envID, AppName: appName,
		ErrText: errText, ActorID: claims.UserID,
	})
	if err != nil {
		status := http.StatusInternalServerError
		var af *autofixError
		if errors.As(err, &af) {
			status = af.status
		}
		audit(auditOutcomeFailure, map[string]any{
			"reason": "launch_failed", "status": status, "detail": err.Error(),
		})
		respondAutofixError(c, err)
		return
	}

	audit(auditOutcomeSuccess, map[string]any{
		"cloud_task_id":  row.ID,
		"error_supplied": suppliedError,
	})

	c.JSON(http.StatusAccepted, gin.H{"cloud_task": row})
}

type autofixLaunch struct {
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	AppName   string
	ErrText   string
	ActorID   uuid.UUID
}

// autofixError carries the HTTP status a launch failure should surface, so the
// engine below stays transport-agnostic while its callers -- the app page's
// explicit action and the support-ticket triage endpoint -- still answer with
// the same status codes for the same causes.
type autofixError struct {
	status  int
	message string
}

func (e *autofixError) Error() string { return e.message }

// respondAutofixError maps a launch failure onto the response, defaulting to
// 500 for anything the engine did not classify.
func respondAutofixError(c *gin.Context, err error) {
	var af *autofixError
	if errors.As(err, &af) {
		respondError(c, af.status, af.message)
		return
	}
	respondError(c, http.StatusInternalServerError, "failed to launch auto-fix run")
}

// launchAutofix is the auto-fix engine: it mints a repo-scoped install token,
// gathers runtime log context, starts the DadaAgent run and records the
// cloud_tasks row keyed by the agent's cloud_task_id so the existing webhook
// can drive it to completion.
//
// It takes the failure as plain text and nothing else, which is what lets a
// support ticket drive it. The error context used to be produced only by the
// platform -- a build-failure summary or an LLM crash diagnosis -- so the
// engine only ever fired from an alert. A customer describing the same bug in
// their own words is the same input.
func (h *Handler) launchAutofix(ctx context.Context, in autofixLaunch) (models.CloudTask, error) {
	if h.dadagent == nil {
		return models.CloudTask{}, &autofixError{http.StatusServiceUnavailable, "dadagent integration not configured"}
	}

	repo, instID, gitRepoID, err := h.resolveGitRepo(ctx, in.ProjectID, in.EnvID, in.AppName)
	if err != nil {
		return models.CloudTask{}, &autofixError{http.StatusBadRequest, "no connected git repo for app"}
	}

	token, _, err := gh.MintInstallToken(ctx, h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, instID)
	if err != nil {
		return models.CloudTask{}, &autofixError{http.StatusBadGateway, "failed to mint install token"}
	}

	logs := h.fetchAutofixLogs(ctx, in.ProjectID, in.EnvID, in.AppName)

	res, err := h.dadagent.Autofix(ctx, dadagent.AutofixRequest{
		RepoFullName: repo,
		InstallToken: token,
		Error:        in.ErrText,
		CallbackURL:  h.cfg.CloudTaskCallbackURL,
		Logs:         logs,
	})
	if err != nil {
		log.Printf("autofix: app %s (project %s) launch failed: %v", in.AppName, in.ProjectID, err)
		return models.CloudTask{}, &autofixError{http.StatusBadGateway, "failed to launch auto-fix run"}
	}

	cloudTaskID := ""
	if info, err := h.dadagent.GetRun(ctx, res.RunID); err != nil {
		log.Printf("autofix: run %s: failed to read back cloud_task_id, webhook updates will not match this row: %v", res.RunID, err)
	} else {
		cloudTaskID = info.CloudTaskID
	}

	gitRepoUUID := gitRepoID
	row, err := h.insertCloudTask(ctx, cloudTaskInsert{
		ProjectID: in.ProjectID, EnvironmentID: in.EnvID, AppName: in.AppName,
		GitRepoID: &gitRepoUUID, TaskType: "autofix",
		IntentID: cloudTaskID, WorkflowID: res.RunID,
		ActorID: in.ActorID,
	})
	if err != nil {
		return models.CloudTask{}, &autofixError{http.StatusInternalServerError, "failed to record cloud task"}
	}
	return row, nil
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

// fetchAutofixLogs pulls the last hour of ERROR-level runtime logs for an app
// to hand the auto-fix agent real crash context instead of just a build-fail
// summary. Best-effort only: OpenSearch/Elasticsearch is a known recurring
// outage point, and log context is a nice-to-have, not a blocker -- any
// failure (no infra client configured, no k8s namespaces resolved, search
// error) returns "" so the caller falls back to the existing build-fail
// summary behavior.
func (h *Handler) fetchAutofixLogs(ctx context.Context, projectID, envID uuid.UUID, appName string) string {
	if h.infraLogsearch == nil {
		return ""
	}
	namespaces, err := h.k8sAppNamespaces(ctx, projectID, appName)
	if err != nil {
		log.Printf("autofix: app %s (project %s): resolving k8s namespaces for log context: %v", appName, projectID, err)
		return ""
	}
	if len(namespaces) == 0 {
		return ""
	}
	res, err := h.infraLogsearch.Search(ctx, logsearch.SearchOpts{
		KubeApp:        appName,
		KubeNamespaces: namespaces,
		Level:          "ERROR",
		Since:          time.Now().Add(-time.Hour),
		Size:           50,
	})
	if err != nil {
		log.Printf("autofix: app %s (project %s): log context search failed: %v", appName, projectID, err)
		return ""
	}
	return formatAutofixLogs(res.Entries)
}

// formatAutofixLogs renders log entries newest-first into a plain-text block
// for the agent prompt.
func formatAutofixLogs(entries []logsearch.LogEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Timestamp)
		b.WriteString(" ")
		b.WriteString(e.Message)
		b.WriteString("\n")
	}
	return b.String()
}
