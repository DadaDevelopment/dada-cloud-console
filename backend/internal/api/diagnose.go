package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/llmchat"
	"github.com/dada-tuda/console/backend/internal/logsearch"
)

// Tuning knobs for the "razobratsya" flow. diagnoseRecentWindow is the first
// log window tried: recent enough that a crash-loop's most recent restart is
// almost always inside it. diagnoseFallbackWindow is the wider retry when the
// recent window comes back empty (app already rescheduled since crashing,
// shipped logs lag, etc). diagnoseLogSearchSize mirrors fetchAutofixLogs'
// bound. diagnoseMaxLogChars caps the log payload handed to the model to a
// sane token budget; diagnoseExcerptLines is how many of the (already
// truncated, most-recent) lines are echoed back to the user as evidence.
// diagnoseTimeout bounds the whole handler -- log search plus one LLM call --
// so a slow gateway can never hang the request indefinitely.
const (
	diagnoseRecentWindow   = time.Hour
	diagnoseFallbackWindow = 24 * time.Hour
	diagnoseLogSearchSize  = 200
	diagnoseMaxLogChars    = 15000
	diagnoseExcerptLines   = 40
	diagnoseTimeout        = 45 * time.Second
)

// diagnoseSystemPrompt instructs the model to ground its answer strictly in
// the supplied log excerpt and stay within a short, actionable shape.
const diagnoseSystemPrompt = `Ты инженер поддержки PaaS-платформы. Тебе дан фрагмент логов контейнера пользовательского приложения (иногда вместе с уже известной платформе причиной сбоя). Отвечай по-русски, коротким markdown не более чем в 6 предложений, по схеме: (1) что сломалось; (2) вероятная причина -- обязательно подкрепи её ПРЯМОЙ ЦИТАТОЙ из лога; (3) конкретный следующий шаг для пользователя. Никогда не придумывай факты, которых нет в логах. Если логов недостаточно для уверенного вывода -- так и скажи прямо, не выдумывая причину.`

// diagnoseResponse is the "razobratsya" contract: one click from a crash
// alert to a grounded diagnosis of the user's own app, produced from that
// app's own logs by the AI gateway. AutofixUnavailableReason explains a
// false CanAutofix: "no_repo" (no git_repos row for this app) or
// "repo_without_installation" (the repo is connected but has no usable
// github app installation, e.g. a no-OAuth template deploy); it is omitted
// when CanAutofix is true.
type diagnoseResponse struct {
	Reason                   string   `json:"reason"`
	Diagnosis                string   `json:"diagnosis"`
	LogExcerpt               []string `json:"log_excerpt"`
	CanAutofix               bool     `json:"can_autofix"`
	GeneratedAt              string   `json:"generated_at"`
	AutofixUnavailableReason string   `json:"autofix_unavailable_reason,omitempty"`
}

// DiagnoseApp turns a crash alert into a grounded, log-backed diagnosis: it
// pulls the app's latest health-alert reason (if any), the last ~200 lines
// of its own runtime logs, and asks the AI gateway for a short russian
// explanation quoting the evidence it used. Never a dead end: with zero logs
// it still answers 200 with an honest "no logs" diagnosis instead of a bare
// error. Write role required (same tract as autofix).
//
// @ID          diagnoseApp
// @Summary     Diagnose a crashing app from its own logs
// @Description One click from a crash alert to a grounded diagnosis: pulls the app's latest health-alert reason and recent runtime logs, and asks the AI gateway for a short russian explanation quoting the log evidence it used. Write role required.
// @Tags        cloud-tasks
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} diagnoseResponse
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/diagnose [post]
func (h *Handler) DiagnoseApp(c *gin.Context) {
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
	if !h.agentChatLLM.Configured() {
		respondError(c, http.StatusBadGateway, "diagnosis gateway not configured")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), diagnoseTimeout)
	defer cancel()

	ns := h.environmentNamespace(ctx, envID)
	reason := h.latestHealthAlertReason(ctx, ns, appName)
	entries := h.fetchDiagnoseLogs(ctx, ns, appName)

	canAutofix := false
	autofixUnavailableReason := ""
	_, _, _, gitErr := h.resolveGitRepo(ctx, projectID, envID, appName)
	switch {
	case gitErr == nil:
		canAutofix = true
	case errors.Is(gitErr, errRepoWithoutInstallation):
		autofixUnavailableReason = "repo_without_installation"
	default:
		autofixUnavailableReason = "no_repo"
	}

	diagnosis, excerpt, err := h.diagnoseCore(ctx, appName, reason, entries, claims.UserID.String())
	if err != nil {
		log.Printf("diagnose: app %s (project %s): gateway call failed: %v", appName, projectID, err)
		respondError(c, http.StatusBadGateway, "diagnosis failed: "+err.Error())
		return
	}

	h.recordAuditAsync(claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "app.diagnose",
		ResourceKind:  "app",
		ResourceName:  appName,
		Metadata: map[string]any{
			"reason":                     reason,
			"can_autofix":                canAutofix,
			"had_logs":                   len(entries) > 0,
			"autofix_unavailable_reason": autofixUnavailableReason,
		},
	})

	c.JSON(http.StatusOK, diagnoseResponse{
		Reason:                   reason,
		Diagnosis:                diagnosis,
		LogExcerpt:               excerpt,
		CanAutofix:               canAutofix,
		GeneratedAt:              time.Now().UTC().Format(time.RFC3339),
		AutofixUnavailableReason: autofixUnavailableReason,
	})
}

// latestHealthAlertReason reads the freshest app_health_alerts.reason for
// this namespace+app (empty string if none, including for a VM/non-k8s
// environment where namespace is ""). Diagnosis proceeds either way: the
// logs themselves may show a failure even without a fresh cooldown row.
func (h *Handler) latestHealthAlertReason(ctx context.Context, namespace, appName string) string {
	if namespace == "" || h.pool == nil {
		return ""
	}
	var reason string
	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(reason, '') FROM app_health_alerts
		  WHERE namespace = $1 AND app_name = $2
		    AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $3)
		  ORDER BY COALESCE(last_seen_at, last_sent_at) DESC
		  LIMIT 1`,
		namespace, appName, appHealthAlertFreshWindow.Seconds()).Scan(&reason)
	if err != nil {
		return ""
	}
	return reason
}

// fetchDiagnoseLogs pulls the app's recent runtime logs, trying the tight
// diagnoseRecentWindow first and falling back to diagnoseFallbackWindow when
// that comes back empty (app crashed a while ago, logs lag, etc). Any search
// failure or missing log-search config returns nil so the caller takes the
// honest "no logs" path rather than failing the request.
func (h *Handler) fetchDiagnoseLogs(ctx context.Context, namespace, appName string) []logsearch.LogEntry {
	if h.infraLogsearch == nil || namespace == "" {
		return nil
	}
	entries, err := h.searchDiagnoseWindow(ctx, namespace, appName, diagnoseRecentWindow)
	if err != nil {
		log.Printf("diagnose: app %s: log search (recent window) failed: %v", appName, err)
		return nil
	}
	if len(entries) > 0 {
		return entries
	}
	entries, err = h.searchDiagnoseWindow(ctx, namespace, appName, diagnoseFallbackWindow)
	if err != nil {
		log.Printf("diagnose: app %s: log search (fallback window) failed: %v", appName, err)
		return nil
	}
	return entries
}

// searchDiagnoseWindow runs one bounded k8s-scoped log search over the given
// window.
func (h *Handler) searchDiagnoseWindow(ctx context.Context, namespace, appName string, window time.Duration) ([]logsearch.LogEntry, error) {
	res, err := h.infraLogsearch.Search(ctx, logsearch.SearchOpts{
		KubeApp:        appName,
		KubeNamespaces: []string{namespace},
		Since:          time.Now().Add(-window),
		Size:           diagnoseLogSearchSize,
	})
	if err != nil {
		return nil, err
	}
	return res.Entries, nil
}

// diagnoseCore turns raw log entries into the final diagnosis text plus the
// excerpt echoed back to the user, making one LLM call through h.agentChatLLM
// when there are logs to ground it in. With zero entries it never touches
// the gateway: it answers directly and honestly that there is nothing to
// diagnose from. Kept free of gin/http/db so it is unit-testable with a
// stubbed llmchat client and canned log entries.
func (h *Handler) diagnoseCore(ctx context.Context, appName, reason string, entries []logsearch.LogEntry, endUser string) (diagnosis string, excerpt []string, err error) {
	lines := buildLogLines(entries)
	if len(lines) == 0 {
		return noLogsDiagnosis(reason), []string{}, nil
	}
	collapsed := collapseRepeatedBlocks(lines)
	truncated := truncateLogLines(collapsed, diagnoseMaxLogChars)
	excerpt = lastLines(truncated, diagnoseExcerptLines)
	messages := buildDiagnoseMessages(appName, reason, strings.Join(truncated, "\n"))
	result, err := h.agentChatLLM.StreamChatCompletion(ctx, messages, nil, endUser, nil)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(result.Content), excerpt, nil
}

// buildLogLines renders the search result (newest-first, as Search returns
// it) into chronological (oldest-first) plain-text lines, which reads far
// more naturally to both the model and the user than newest-first.
func buildLogLines(entries []logsearch.LogEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[len(entries)-1-i] = strings.TrimSpace(e.Timestamp + " " + e.Message)
	}
	return lines
}

// crashloopRepeatMarkerFormat renders the marker line left in place of a
// collapsed repeating block. It carries no timestamp of its own: it stands
// for a span of time, not a single moment.
const crashloopRepeatMarkerFormat = "... предыдущие %d строк повторились ещё %d раз (крашлуп)"

// collapseRepeatedBlocks folds a crash-loop's identical restart cycles into a
// single copy plus a marker line, so neither the model's token budget nor the
// user's excerpt is spent re-reading the same traceback fifty times. Lines
// are compared by message text only (the part after the first space), since
// the timestamp differs on every restart and would otherwise defeat any
// comparison. At each position it searches block periods from 1 up to half
// the remaining lines, keeps the period whose (period * repeat-count) covers
// the most lines, and requires at least two full repeats before collapsing --
// anything less is just an ordinary non-repeating line, which is copied
// through untouched. A trailing partial repeat (fewer lines than a full
// period) is never counted as a repeat, so it survives in the output instead
// of being silently dropped.
func collapseRepeatedBlocks(lines []string) []string {
	n := len(lines)
	if n == 0 {
		return lines
	}
	messages := make([]string, n)
	for i, l := range lines {
		messages[i] = logLineMessage(l)
	}

	out := make([]string, 0, n)
	for i := 0; i < n; {
		bestPeriod, bestReps := 0, 1
		maxPeriod := (n - i) / 2
		for p := 1; p <= maxPeriod; p++ {
			reps := 1
			for i+(reps+1)*p <= n && blockRepeatsAt(messages, i, p, reps) {
				reps++
			}
			if reps >= 2 && p*reps > bestPeriod*bestReps {
				bestPeriod, bestReps = p, reps
			}
		}
		if bestReps >= 2 {
			out = append(out, lines[i:i+bestPeriod]...)
			out = append(out, fmt.Sprintf(crashloopRepeatMarkerFormat, bestPeriod, bestReps-1))
			i += bestPeriod * bestReps
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return out
}

// blockRepeatsAt reports whether the p-line block starting at i also occurs,
// unchanged, at occurrence number reps counted from i (i.e. at
// i+reps*p..i+reps*p+p).
func blockRepeatsAt(messages []string, i, p, reps int) bool {
	base := i + reps*p
	for k := 0; k < p; k++ {
		if messages[base+k] != messages[i+k] {
			return false
		}
	}
	return true
}

// logLineMessage strips the leading timestamp off a "<ts> <message>" line
// built by buildLogLines, returning just the message part that
// collapseRepeatedBlocks compares on.
func logLineMessage(line string) string {
	if idx := strings.IndexByte(line, ' '); idx >= 0 {
		return line[idx+1:]
	}
	return ""
}

// truncateLogLines keeps the most recent lines within a maxChars budget,
// dropping the oldest ones first: the tail of a crash log is almost always
// the useful part, and the sent text must stay within the model's token
// budget. The single most recent line is always kept even if it alone
// exceeds the budget -- some evidence beats none.
func truncateLogLines(lines []string, maxChars int) []string {
	if len(lines) == 0 {
		return lines
	}
	total := 0
	start := len(lines) - 1
	for i := len(lines) - 1; i >= 0; i-- {
		total += len(lines[i]) + 1
		if total > maxChars && i != len(lines)-1 {
			start = i + 1
			break
		}
		start = i
	}
	return lines[start:]
}

// lastLines returns the last n lines, or all of them if there are fewer.
func lastLines(lines []string, n int) []string {
	if len(lines) <= n {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	out := make([]string, n)
	copy(out, lines[len(lines)-n:])
	return out
}

// buildDiagnoseMessages assembles the one-shot chat turn sent to the AI
// gateway: a fixed russian system prompt plus the app name, any known
// health-alert reason, and the (already truncated) log text.
func buildDiagnoseMessages(appName, reason, logText string) []llmchat.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "Приложение: %s\n", appName)
	if reason != "" {
		fmt.Fprintf(&b, "Причина сбоя, обнаруженная платформой: %s\n", reason)
	}
	b.WriteString("\nЛоги контейнера (старые сверху, новые снизу):\n")
	b.WriteString(logText)
	return []llmchat.Message{
		{Role: "system", Content: diagnoseSystemPrompt},
		{Role: "user", Content: b.String()},
	}
}

// noLogsDiagnosis is the honest answer when there is nothing to ground a
// diagnosis in: the app never started, or its logs already expired. It never
// invents a cause: if a health-alert reason is known it is surfaced as-is,
// clearly labeled as platform-detected rather than log-grounded.
func noLogsDiagnosis(reason string) string {
	if reason != "" {
		return fmt.Sprintf("Логов не нашлось -- приложение либо не запускалось, либо логи уже истекли. Платформа зафиксировала причину сбоя: %s, но подтвердить её цитатой из лога не получится. Проверьте последний деплой и переменные окружения вручную.", reason)
	}
	return "Логов не нашлось: приложение либо ни разу не запускалось, либо логи уже истекли. Судить о причине сбоя не на чем -- проверьте последний билд/деплой и переменные окружения вручную."
}
