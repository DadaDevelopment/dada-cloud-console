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
// @Failure     409       {object} map[string]string "an autofix run is already in flight for this app"
// @Failure     422       {object} map[string]string "the refusal is a proven platform capacity limit, not app code -- no PR was opened"
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
	code    string
}

func (e *autofixError) Error() string { return e.message }

// respondAutofixError maps a launch failure onto the response, defaulting to
// 500 for anything the engine did not classify. When the failure carries a
// code, it rides alongside "error" so the frontend can pick a specific
// message instead of showing the raw string.
func respondAutofixError(c *gin.Context, err error) {
	var af *autofixError
	if errors.As(err, &af) {
		if af.code != "" {
			c.JSON(af.status, gin.H{"error": af.message, "code": af.code})
			return
		}
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
//
// Two guards run before any network call. Both exist because of the same
// artemmendeleev/fonbet-value incident (2026-08-11 23:21): a double click
// fired two parallel runs against the same repo (no in-flight check existed),
// and separately the platform's own resource ceiling -- not the app's code --
// was the real blocker, which no run could have fixed with a PR.
func (h *Handler) launchAutofix(ctx context.Context, in autofixLaunch) (models.CloudTask, error) {
	if h.dadagent == nil {
		return models.CloudTask{}, &autofixError{status: http.StatusServiceUnavailable, message: "dadagent integration not configured"}
	}

	claim, err := h.claimAutofixRun(ctx, in)
	if err != nil {
		if isUniqueViolation(err) {
			return models.CloudTask{}, &autofixError{
				status:  http.StatusConflict,
				message: "по этому приложению уже идёт автопочинка, дождитесь её завершения",
				code:    "autofix_already_running",
			}
		}
		return models.CloudTask{}, &autofixError{status: http.StatusInternalServerError, message: "failed to record cloud task"}
	}

	claimID, err := uuid.Parse(claim.ID)
	if err != nil {
		return models.CloudTask{}, &autofixError{status: http.StatusInternalServerError, message: "failed to record cloud task"}
	}

	fail := func(af *autofixError) (models.CloudTask, error) {
		h.failCloudTask(ctx, claimID, af.message)
		return models.CloudTask{}, af
	}

	if reason, capped := h.platformCapacityRefusal(ctx, in.ProjectID, in.EnvID, in.AppName); capped {
		log.Printf("autofix: app %s (project %s) refused: platform capacity cause %q, not opening a PR", in.AppName, in.ProjectID, reason)
		return fail(&autofixError{
			status:  http.StatusUnprocessableEntity,
			message: autofixPlatformCapacityVerdict,
			code:    "platform_capacity_limit",
		})
	}

	repo, instID, gitRepoID, err := h.resolveGitRepo(ctx, in.ProjectID, in.EnvID, in.AppName)
	if errors.Is(err, errRepoWithoutInstallation) {
		return fail(&autofixError{
			status:  http.StatusBadRequest,
			message: "репозиторий подключён без доступа GitHub App: переподключите его через GitHub App, чтобы автофикс мог открыть PR",
			code:    "repo_without_installation",
		})
	}
	if err != nil {
		return fail(&autofixError{
			status:  http.StatusBadRequest,
			message: "no connected git repo for app",
			code:    "no_repo",
		})
	}

	token, _, err := gh.MintInstallToken(ctx, h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, instID)
	if err != nil {
		return fail(&autofixError{status: http.StatusBadGateway, message: "failed to mint install token"})
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
		return fail(&autofixError{status: http.StatusBadGateway, message: "failed to launch auto-fix run"})
	}

	cloudTaskID := ""
	if info, err := h.dadagent.GetRun(ctx, res.RunID); err != nil {
		log.Printf("autofix: run %s: failed to read back cloud_task_id, webhook updates will not match this row: %v", res.RunID, err)
	} else {
		cloudTaskID = info.CloudTaskID
	}

	row, err := h.finalizeCloudTask(ctx, claimID, gitRepoID, cloudTaskID, res.RunID)
	if err != nil {
		return models.CloudTask{}, &autofixError{status: http.StatusInternalServerError, message: "failed to record cloud task"}
	}
	return row, nil
}

// claimAutofixRun stakes the (project_id, environment_id, app_name) slot for
// an autofix run before any network call is made: idx_cloud_tasks_autofix_
// inflight (migration 113) allows only one status='running' task_type=autofix
// row per app across every backend replica, so a second concurrent launch
// fails the INSERT with a unique-violation instead of racing a second run
// past the first. GitRepoID/IntentID/WorkflowID are filled in later by
// finalizeCloudTask once they are known; the row is 'running' (the table
// default) from the moment this returns.
func (h *Handler) claimAutofixRun(ctx context.Context, in autofixLaunch) (models.CloudTask, error) {
	return h.insertCloudTask(ctx, cloudTaskInsert{
		ProjectID: in.ProjectID, EnvironmentID: in.EnvID, AppName: in.AppName,
		TaskType: "autofix", ActorID: in.ActorID,
	})
}

// autofixPlatformCapacityReasons is the subset of app-autoscale refusal
// reasons (app_autoscale_watcher.go maybeResize/auditRefusal, metadata.
// refusal) that are proven platform-capacity facts, not app bugs: the
// autoscaler looked at a genuinely starved, genuinely Ready app and hit a
// ceiling it does not control. Every other refusal reason on that same code
// path -- app_not_ready, unsized_app, envelope_unreadable, limitrange_
// unreadable, quota_unreadable, resize_failed -- stays out on purpose: those
// can just as easily mean the app's own code is crash-looping or the platform
// simply could not read its own state, so treating them as "definitely not
// your code" would repeat the mistake platformCrashSignatures in notify.go
// guards against -- a confident wrong verdict in either direction is worse
// than staying silent and letting the run proceed normally.
var autofixPlatformCapacityReasons = map[string]bool{
	"limitrange_capped":   true,
	"at_ceiling":          true,
	"quota_blocked":       true,
	"consumption_blocked": true,
}

// autofixPlatformCapacityWindow bounds how fresh the autoscaler's refusal
// must be to still describe the app's current state. Set to appAutoscaleCooldown
// (app_autoscale_watcher.go) -- the longest gap the watcher's own dedup can
// leave between a refusal recurring and a fresh audit row proving it, so a
// row older than this window could already be stale (quota raised, app since
// resized, starvation gone) and must not be trusted.
const autofixPlatformCapacityWindow = appAutoscaleCooldown

// autofixPlatformCapacityVerdict is shown instead of opening a PR when the
// freshest capacity refusal for this exact app is still inside the window:
// no pull request can raise a ceiling the platform itself enforces, so
// running the agent would only relabel the same failure under a different
// name. Kubernetes/LimitRange/namespace never appear in user-facing text.
const autofixPlatformCapacityVerdict = "причина отказа не в коде приложения: платформе не хватает выделенных приложению ресурсов в рамках текущего тарифа, и пул-реквест это не изменит. Поднимите лимиты ресурсов или тариф приложения и повторите запуск"

// platformCapacityRefusal looks up the freshest app-autoscale refusal audit
// row for this exact app and reports whether it names a reason in
// autofixPlatformCapacityReasons within autofixPlatformCapacityWindow. Returns
// found=false for everything else, including a query failure or no matching
// row at all -- an unclassifiable or absent signal must fall through to a
// normal PR-seeking run rather than guess.
func (h *Handler) platformCapacityRefusal(ctx context.Context, projectID, envID uuid.UUID, appName string) (reason string, found bool) {
	var refusal *string
	err := h.pool.QueryRow(ctx,
		`SELECT metadata->>'refusal' FROM audit_events
		  WHERE project_id = $1 AND environment_id = $2 AND action = $3
		    AND resource_kind = 'App' AND resource_name = $4 AND outcome = $5
		    AND created_at >= NOW() - make_interval(secs => $6)
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, envID, auditActionAutoscaleApp, appName, auditOutcomeFailure,
		autofixPlatformCapacityWindow.Seconds()).
		Scan(&refusal)
	if err != nil || refusal == nil {
		return "", false
	}
	if !autofixPlatformCapacityReasons[*refusal] {
		return "", false
	}
	return *refusal, true
}

// formatBuildFailureSummary renders a short natural-language failure summary
// from build fields -- the auto-fix error context used when the caller does
// not supply one explicitly.
// The cause is the whole point of the summary. Without it the agent is handed
// a commit message and a timestamp and asked why the build broke -- it cannot
// know, and the run burns tokens guessing. fail_reason names the class and
// error_message carries the line the build actually died on.
func formatBuildFailureSummary(branch, commitSHA string, commitMessage *string, finishedAt *time.Time, failReason string, errorMessage *string) string {
	summary := fmt.Sprintf("Build failed on branch %s (commit %s)", branch, shortSHA(commitSHA))
	if commitMessage != nil && *commitMessage != "" {
		summary += ": " + *commitMessage
	}
	if finishedAt != nil {
		summary += " [failed at " + finishedAt.UTC().Format(time.RFC3339) + "]"
	}
	if cause := buildFailureCause(failReason, errorMessage); cause != "" {
		summary += "\n" + cause
	}
	return summary
}

// buildFailureCause renders the persisted failure of a build as the line an
// agent can act on. The build agent stores error_message as
// "<fail_reason>: <detail>", so the code is dropped when the detail already
// repeats it rather than saying the same word twice.
func buildFailureCause(failReason string, errorMessage *string) string {
	msg := ""
	if errorMessage != nil {
		msg = strings.TrimSpace(*errorMessage)
	}
	if failReason == "" && msg == "" {
		return ""
	}
	if msg == "" {
		return "Failure reason: " + failReason
	}
	msg = strings.TrimPrefix(msg, failReason+": ")
	if failReason == "" {
		return "Cause: " + msg
	}
	return "Failure reason: " + failReason + "\nCause: " + msg
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
	var failReason *string
	var errorMessage *string
	err := h.pool.QueryRow(ctx,
		`SELECT branch, commit_sha, commit_message, finished_at, fail_reason, error_message
		   FROM builds
		  WHERE environment_id = $1 AND app_name = $2 AND status = 'failed'
		  ORDER BY created_at DESC LIMIT 1`,
		envID, appName).Scan(&branch, &commitSHA, &commitMessage, &finishedAt, &failReason, &errorMessage)
	if err != nil {
		return "", err
	}
	reason := ""
	if failReason != nil {
		reason = *failReason
	}
	return formatBuildFailureSummary(branch, commitSHA, commitMessage, finishedAt, reason, errorMessage), nil
}

// Tuning knobs for fetchAutofixLogs' three-tier retry. autofixRecentWindow is
// the tight window tried first, both with and without the ERROR level filter.
// autofixFallbackWindow is the wide retry used once both recent-window
// attempts come back empty (crash logged a while ago, log shipper lag, etc).
// autofixLogSearchSize mirrors diagnoseLogSearchSize -- generous enough to
// give the agent real context without flooding its prompt.
const (
	autofixRecentWindow   = time.Hour
	autofixFallbackWindow = 24 * time.Hour
	autofixLogSearchSize  = 50
	autofixErrorLevel     = "ERROR"
)

// fetchAutofixLogs pulls recent runtime logs for an app to hand the auto-fix
// agent real crash context instead of just a build-fail summary. Best-effort
// only: OpenSearch/Elasticsearch is a known recurring outage point, and log
// context is a nice-to-have, not a blocker -- any failure (no infra client
// configured, no k8s namespaces resolved, search error) returns "" so the
// caller falls back to the existing build-fail summary behavior.
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
	entries, err := h.searchAutofixLogs(ctx, namespaces, appName)
	if err != nil {
		log.Printf("autofix: app %s (project %s): %v", appName, projectID, err)
		return ""
	}
	return formatAutofixLogs(entries)
}

// searchAutofixLogs tries, in order: the tight recent window filtered to
// ERROR level; the same window unfiltered (structured-log apps report an
// error field, but a plain stdout stack trace from an uncaught Python/Node
// exception carries no level field at all and would never match a level
// filter -- see the fetchDiagnoseLogs path, which never filters by level, for
// the sibling flow this mirrors); and finally the wide fallback window,
// unfiltered. Each tier only runs once the previous one comes back with zero
// hits, so a structured app with real ERROR entries never sees noisier
// unfiltered results. Split out from fetchAutofixLogs so it is unit-testable
// without a k8sAppNamespaces DB round-trip.
func (h *Handler) searchAutofixLogs(ctx context.Context, namespaces []string, appName string) ([]logsearch.LogEntry, error) {
	entries, err := h.searchAutofixWindow(ctx, namespaces, appName, autofixRecentWindow, autofixErrorLevel)
	if err != nil {
		return nil, fmt.Errorf("log search (recent window, ERROR level) failed: %w", err)
	}
	if len(entries) > 0 {
		return entries, nil
	}
	entries, err = h.searchAutofixWindow(ctx, namespaces, appName, autofixRecentWindow, "")
	if err != nil {
		return nil, fmt.Errorf("log search (recent window, unfiltered) failed: %w", err)
	}
	if len(entries) > 0 {
		return entries, nil
	}
	entries, err = h.searchAutofixWindow(ctx, namespaces, appName, autofixFallbackWindow, "")
	if err != nil {
		return nil, fmt.Errorf("log search (fallback window, unfiltered) failed: %w", err)
	}
	return entries, nil
}

// searchAutofixWindow runs one bounded k8s-scoped log search over the given
// window, optionally filtered to a log level ("" means unfiltered).
func (h *Handler) searchAutofixWindow(ctx context.Context, namespaces []string, appName string, window time.Duration, level string) ([]logsearch.LogEntry, error) {
	res, err := h.infraLogsearch.Search(ctx, logsearch.SearchOpts{
		KubeApp:        appName,
		KubeNamespaces: namespaces,
		Level:          level,
		Since:          time.Now().Add(-window),
		Size:           autofixLogSearchSize,
	})
	if err != nil {
		return nil, err
	}
	return res.Entries, nil
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
