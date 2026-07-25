package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/llmchat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const agentTokenSourceConsoleChat = "console_chat"

const agentChatSlowTestTrigger = "__slowtest__"
const agentChatSlowTestDuration = 75 * time.Second
const agentChatSlowTestHeartbeat = 10 * time.Second
const agentChatHistoryLimit = 20
const agentChatToolResultMaxLen = 2000
const agentChatPendingActionTTL = 5 * time.Minute

const agentChatConfirmDeclineMessage = "The user chose to decline this action in the confirmation dialog. This is the user's own decision, not a permissions problem and not a tool failure. Do not retry the action, do not speculate about access rights or protection. Briefly acknowledge that the action was cancelled at the user's request and ask what they would like to do instead."

type agentChatRequest struct {
	Message   string `json:"message"`
	ProjectID string `json:"projectId"`
	EnvID     string `json:"envId"`
	AppName   string `json:"appName"`
}

func writeSSEEvent(c *gin.Context, flusher http.Flusher, event string, data string) {
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// writeSSEToken emits an assistant text delta as a JSON-encoded string so that
// newlines, leading/trailing spaces and control characters survive the SSE
// line framing intact. Raw deltas corrupted the wire: an embedded '\n' split
// the data frame (the tail line lost its "data:" prefix and was dropped) and
// significant whitespace was eaten by the client-side trim, producing scrambled
// and glued output. The client JSON-parses the payload back to the exact delta.
func writeSSEToken(c *gin.Context, flusher http.Flusher, text string) {
	b, err := json.Marshal(text)
	if err != nil {
		return
	}
	writeSSEEvent(c, flusher, "token", string(b))
}

func agentChatOrgID(claims *auth.Claims) string {
	if claims == nil {
		return ""
	}
	for org := range claims.OrgRoles() {
		return org
	}
	return ""
}

func parseOptionalUUID(s string) *uuid.UUID {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

// extractUUIDArg pulls a UUID-shaped field out of a tool call's raw JSON
// arguments. Used to resolve a confirmation card against what the tool call
// will ACTUALLY target (e.g. the envId createDatabase's own args carry),
// rather than the console's current project/env selection at the time the
// turn started -- those two can legitimately differ (the agent may resolve
// its own envId per the system prompt's "choose for me" instruction), and the
// card must never show a target other than the one that's really about to
// execute.
func extractUUIDArg(argsJSON, key string) *uuid.UUID {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return nil
	}
	raw, ok := m[key].(string)
	if !ok || raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// agentChatResolveNames best-effort resolves project/environment display names for a
// confirmation summary; empty strings on any lookup failure just make the summary
// slightly less specific, never an error.
func (h *Handler) agentChatResolveNames(ctx context.Context, projectID, envID *uuid.UUID) (projectName, envName string) {
	if projectID != nil {
		_ = h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, *projectID).Scan(&projectName)
	}
	if envID != nil {
		_ = h.pool.QueryRow(ctx, `SELECT name FROM environments WHERE id = $1`, *envID).Scan(&envName)
	}
	return projectName, envName
}

func truncateForTranscript(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... [truncated]"
}

func (h *Handler) agentChatDailyMessageCount(ctx context.Context, userSub string) (int64, error) {
	var count int64
	err := h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_chat_messages
		 WHERE user_sub = $1 AND role = 'user' AND created_at >= date_trunc('day', now())`,
		userSub,
	).Scan(&count)
	return count, err
}

func (h *Handler) agentChatInsertMessage(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, role, content string, toolName *string) {
	var orgArg, toolArg any
	if orgID != "" {
		orgArg = orgID
	}
	if toolName != nil {
		toolArg = *toolName
	}
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO agent_chat_messages (user_sub, org_id, project_id, env_id, role, content, tool_name)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		userSub, orgArg, projectID, envID, role, content, toolArg,
	); err != nil {
		log.Printf("agent-chat: failed to persist %s message: %v", role, err)
	}
}

// recordAgentTokenUsage appends one agent_token_usage ledger row for a
// completed (or partial, on error/confirm interrupt) turn. The provider USD
// cost is frozen here from the in-repo model price table; FX and markup stay
// out of the ledger and are applied at invoice time. It no-ops when the turn
// reported no tokens (e.g. gateway not configured or a pre-flight failure) and
// never blocks the response on a ledger write error.
func (h *Handler) recordAgentTokenUsage(ctx context.Context, source, orgID, userSub string, projectID, envID *uuid.UUID, usage agentchat.Usage) {
	if usage.TotalTokens <= 0 {
		return
	}
	model := usage.Model
	if model == "" {
		model = h.cfg.AgentChatModel
	}
	costUSD := pricing.AgentTokenCostUSD(model, usage.PromptTokens, usage.CompletionTokens)

	var orgArg, userArg any
	if orgID != "" {
		orgArg = orgID
	}
	if userSub != "" {
		userArg = userSub
	}
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO agent_token_usage
			(source, org_id, project_id, env_id, user_sub, model,
			 prompt_tokens, completion_tokens, total_tokens, cost_usd)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		source, orgArg, projectID, envID, userArg, model,
		usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, costUSD,
	); err != nil {
		log.Printf("agent-chat: failed to record token usage: %v", err)
	}
}

func (h *Handler) agentChatHistory(ctx context.Context, userSub string, projectID, envID *uuid.UUID) []llmchat.Message {
	rows, err := h.pool.Query(ctx,
		`SELECT role, content FROM (
			SELECT role, content, created_at FROM agent_chat_messages
			WHERE user_sub = $1
			  AND project_id IS NOT DISTINCT FROM $2
			  AND env_id IS NOT DISTINCT FROM $3
			  AND role IN ('user', 'assistant')
			  AND content <> ''
			ORDER BY created_at DESC
			LIMIT $4
		 ) recent ORDER BY created_at ASC`,
		userSub, projectID, envID, agentChatHistoryLimit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []llmchat.Message
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			continue
		}
		out = append(out, llmchat.Message{Role: role, Content: content})
	}
	return out
}

type agentChatConfirmRequest struct {
	ActionID string `json:"action_id"`
	Decision string `json:"decision"`
}

type agentChatPendingRow struct {
	id               uuid.UUID
	userSub          string
	orgID            string
	projectID        *uuid.UUID
	envID            *uuid.UUID
	toolName         string
	argsJSON         string
	toolCallID       string
	messagesSnapshot []llmchat.Message
	toolCallCount    int
	writeCallCount   int
	status           string
	expiresAt        time.Time
}

func (h *Handler) agentChatInsertPendingAction(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, pending *agentchat.PendingWrite) (uuid.UUID, error) {
	snapshot, err := json.Marshal(pending.Messages)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal messages snapshot: %w", err)
	}
	args := strings.TrimSpace(pending.ArgsJSON)
	if args == "" {
		args = "{}"
	}

	var orgArg any
	if orgID != "" {
		orgArg = orgID
	}

	var actionID uuid.UUID
	err = h.pool.QueryRow(ctx,
		`INSERT INTO agent_chat_pending_actions
			(user_sub, org_id, project_id, env_id, tool_name, args_json, tool_call_id,
			 messages_snapshot, tool_call_count, write_call_count, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', $11)
		 RETURNING id`,
		userSub, orgArg, projectID, envID, pending.ToolName, args, pending.ToolCallID,
		snapshot, pending.ToolCallCount, pending.WriteCallCount, time.Now().Add(agentChatPendingActionTTL),
	).Scan(&actionID)
	if err != nil {
		return uuid.Nil, err
	}
	return actionID, nil
}

func (h *Handler) agentChatLoadPendingAction(ctx context.Context, actionID uuid.UUID) (*agentChatPendingRow, error) {
	var row agentChatPendingRow
	var orgID *string
	var snapshotRaw []byte
	err := h.pool.QueryRow(ctx,
		`SELECT id, user_sub, org_id, project_id, env_id, tool_name, args_json, tool_call_id,
		        messages_snapshot, tool_call_count, write_call_count, status, expires_at
		   FROM agent_chat_pending_actions WHERE id = $1`,
		actionID,
	).Scan(&row.id, &row.userSub, &orgID, &row.projectID, &row.envID, &row.toolName, &row.argsJSON,
		&row.toolCallID, &snapshotRaw, &row.toolCallCount, &row.writeCallCount, &row.status, &row.expiresAt)
	if err != nil {
		return nil, err
	}
	if orgID != nil {
		row.orgID = *orgID
	}
	if err := json.Unmarshal(snapshotRaw, &row.messagesSnapshot); err != nil {
		return nil, fmt.Errorf("unmarshal messages snapshot: %w", err)
	}
	return &row, nil
}

func (h *Handler) agentChatConsumePendingAction(ctx context.Context, actionID uuid.UUID, newStatus string) (bool, error) {
	tag, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_pending_actions SET status = $1 WHERE id = $2 AND status = 'pending'`,
		newStatus, actionID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// agentChatConfirmSummary renders the human-readable line shown on a confirmation
// card. projectName/envName are only used by the createDatabase case (the other
// write tools are app-scoped, where appName alone is unambiguous); callers pass
// empty strings for every other tool.
func agentChatConfirmSummary(toolName, argsJSON, projectName, envName string) string {
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}
	appName, _ := args["appName"].(string)
	key, _ := args["key"].(string)

	switch toolName {
	case "createDatabase":
		name, _ := args["name"].(string)
		database, _ := args["database"].(string)
		appRef, _ := args["app_ref"].(string)
		backupEnabled, _ := args["backup_enabled"].(bool)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Create a managed PostgreSQL database %q (database=%q)", name, database))
		if projectName != "" || envName != "" {
			sb.WriteString(fmt.Sprintf(" in project %q, environment %q", projectName, envName))
		}
		if appRef != "" {
			sb.WriteString(fmt.Sprintf(", bound to app %q", appRef))
		}
		if backupEnabled {
			sb.WriteString(", with backups enabled")
		} else {
			sb.WriteString(", without backups")
		}
		sb.WriteString(".")
		return sb.String()
	case "restartApp":
		return fmt.Sprintf("Restart app %s", appName)
	case "rollbackApp":
		return fmt.Sprintf("Roll back app %s to its previous version", appName)
	case "rollbackDeployment":
		return "Roll back the deployment"
	case "promoteDeployment":
		return "Promote the deployment"
	case "triggerBuild":
		return fmt.Sprintf("Trigger a new build for app %s", appName)
	case "cancelBuild":
		return "Cancel the build"
	case "deployTrigger":
		return fmt.Sprintf("Trigger a deploy for app %s", appName)
	case "retryOperation":
		return "Retry the operation"
	case "setEnvVar":
		return fmt.Sprintf("Set env var %s on app %s", key, appName)
	case "deleteEnvVar":
		return fmt.Sprintf("Delete env var %s on app %s", key, appName)
	case "updateAppImage":
		return fmt.Sprintf("Update the image for app %s", appName)
	case "updateAppProfile":
		return fmt.Sprintf("Update the resource profile for app %s", appName)
	case "updateAppStorage":
		return fmt.Sprintf("Update storage for app %s", appName)
	default:
		return fmt.Sprintf("Run %s", toolName)
	}
}

func agentChatSystemPrompt(req agentChatRequest) string {
	var sb strings.Builder
	sb.WriteString("You are the Dada Cloud console assistant, embedded in a side panel of the console UI. ")
	sb.WriteString("Answer in the same language the user writes in (Russian or English). ")
	sb.WriteString("Use the available tools to look up real project/app/deployment state before making any factual claim about the user's resources; never invent state you have not looked up. ")
	sb.WriteString("Be concise and concrete. If you cannot resolve the user's problem, offer to file a support ticket with the create_support_ticket tool. ")
	sb.WriteString("Current console context: ")
	if req.ProjectID != "" {
		sb.WriteString("projectId=" + req.ProjectID + " ")
	}
	if req.EnvID != "" {
		sb.WriteString("envId=" + req.EnvID + " ")
	}
	if req.AppName != "" {
		sb.WriteString("appName=" + req.AppName + " ")
	}
	if req.ProjectID == "" && req.EnvID == "" && req.AppName == "" {
		sb.WriteString("none (the user has not selected a project yet).")
	}
	sb.WriteString(" You can order a managed PostgreSQL database with the createDatabase tool, but it requires a specific projectId and envId (environment), which are NOT things you may invent. ")
	sb.WriteString("If envId is not already given above, ask the user which project/environment before calling createDatabase. ")
	sb.WriteString("If the user says to choose for them, first call getProject (or listProjects) to see the real environments that exist, pick a sensible one (prefer an environment named prod if several exist and the user gave no other hint), and explicitly state which environment you picked before calling the tool -- never guess an envId you have not looked up. ")
	sb.WriteString("createDatabase always pauses for the user's explicit confirmation in the UI before it actually runs, so propose it as soon as you have resolved name/database/env; you do not need the user to also confirm in chat first.")
	return sb.String()
}

// @ID          agentChat
// @Summary     Stream a chat turn with the console agent
// @Description Streams Server-Sent Events for a single chat turn. Runs a server-side ReAct loop against the ADR-015 LLM gateway, grounding answers with a curated read-only subset of the console's own API (plus create_support_ticket) executed under the caller's own bearer. Emits token events (assistant text deltas), tool_call events (tool name only), an error event on a friendly failure (gateway not configured, daily cap reached, upstream error), and a final done event. Sending the literal message "__slowtest__" instead streams a 75s heartbeat run to prove the endpoint survives the ingress proxy-read-timeout.
// @Tags        agent
// @Accept      json
// @Produce     text/event-stream
// @Security    BearerAuth
// @Param       body body     agentChatRequest true "Chat turn"
// @Success     200  {string} string "text/event-stream"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Router      /agent/chat [post]
func (h *Handler) AgentChat(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var req agentChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		respondError(c, http.StatusBadRequest, "message must not be empty")
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		respondError(c, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher.Flush()

	ctx := c.Request.Context()

	if message == agentChatSlowTestTrigger {
		ticks := int(agentChatSlowTestDuration / agentChatSlowTestHeartbeat)
		for i := 1; i <= ticks; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(agentChatSlowTestHeartbeat):
			}
			writeSSEEvent(c, flusher, "heartbeat", fmt.Sprintf(`{"tick":%d}`, i))
		}
		writeSSEEvent(c, flusher, "done", `{"ok":true}`)
		return
	}

	userSub := claims.UserID.String()
	orgID := agentChatOrgID(claims)
	projectID := parseOptionalUUID(req.ProjectID)
	envID := parseOptionalUUID(req.EnvID)

	if h.agentChatLLM == nil || !h.agentChatLLM.Configured() || h.agentChatTools == nil {
		writeSSEEvent(c, flusher, "error", `{"code":"not_configured","message":"agent chat is not configured yet on this environment"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	dailyCap := h.cfg.AgentChatDailyMsgCap
	if dailyCap > 0 {
		count, err := h.agentChatDailyMessageCount(ctx, userSub)
		if err == nil && count >= dailyCap {
			writeSSEEvent(c, flusher, "error", `{"code":"daily_cap","message":"you have reached today's chat message limit; try again tomorrow"}`)
			writeSSEEvent(c, flusher, "done", `{"ok":false}`)
			return
		}
	}

	h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "user", message, nil)

	history := h.agentChatHistory(ctx, userSub, projectID, envID)
	systemPrompt := agentChatSystemPrompt(req)
	bearer := c.GetHeader("Authorization")

	emit := agentchat.Emitter{
		Token: func(text string) {
			writeSSEToken(c, flusher, text)
		},
		ToolCall: func(name string) {
			writeSSEEvent(c, flusher, "tool_call", fmt.Sprintf(`{"name":%q}`, name))
		},
	}

	assistantText, toolLog, pending, usage, err := agentchat.RunTurn(ctx, h.agentChatLLM, h.agentChatTools, bearer, systemPrompt, history, message, emit)
	h.recordAgentTokenUsage(ctx, agentTokenSourceConsoleChat, orgID, userSub, projectID, envID, usage)
	if err != nil {
		writeSSEEvent(c, flusher, "error", fmt.Sprintf(`{"code":"upstream","message":%q}`, "agent could not complete this turn, please try again"))
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	for _, t := range toolLog {
		toolName := t.Name
		h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "tool", truncateForTranscript(t.Result, agentChatToolResultMaxLen), &toolName)
	}

	if pending != nil {
		h.agentChatEmitConfirmRequest(c, flusher, ctx, userSub, orgID, projectID, envID, pending)
		return
	}

	if assistantText != "" {
		h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "assistant", assistantText, nil)
	}

	writeSSEEvent(c, flusher, "done", `{"ok":true}`)
}

func (h *Handler) agentChatEmitConfirmRequest(c *gin.Context, flusher http.Flusher, ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, pending *agentchat.PendingWrite) {
	actionID, err := h.agentChatInsertPendingAction(ctx, userSub, orgID, projectID, envID, pending)
	if err != nil {
		log.Printf("agent-chat: failed to persist pending action: %v", err)
		writeSSEEvent(c, flusher, "error", `{"code":"upstream","message":"agent could not prepare this action for confirmation, please try again"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	// The tool call's own args are the ground truth for what will actually
	// execute -- resolve the project/env names from THOSE, not from the
	// console context this turn started with, for tools where that can differ
	// (currently only createDatabase; other write tools are app-scoped).
	var projectName, envName string
	if pending.ToolName == "createDatabase" {
		targetProjectID := extractUUIDArg(pending.ArgsJSON, "projectId")
		targetEnvID := extractUUIDArg(pending.ArgsJSON, "envId")
		projectName, envName = h.agentChatResolveNames(ctx, targetProjectID, targetEnvID)
	}
	summary := agentChatConfirmSummary(pending.ToolName, pending.ArgsJSON, projectName, envName)
	payload, _ := json.Marshal(map[string]any{
		"action_id": actionID,
		"tool_name": pending.ToolName,
		"args":      json.RawMessage(nonEmptyJSON(pending.ArgsJSON)),
		"summary":   summary,
	})
	h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "confirm_request", summary, &pending.ToolName)

	writeSSEEvent(c, flusher, "confirm_request", string(payload))
	writeSSEEvent(c, flusher, "done", `{"ok":true,"awaiting_confirm":true}`)
}

func nonEmptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// @ID          agentChatConfirm
// @Summary     Approve or reject a pending agent write action
// @Description Resumes a chat turn that interrupted on a write-tool call (see the confirm_request SSE event from POST /agent/chat). On approve, executes the tool for real under this request's own bearer (RBAC re-checked by the self-proxy), appends the result, and continues the ReAct loop to a final answer — which may itself interrupt again on a further write call. On reject, appends a decline notice and continues without executing anything. Streams Server-Sent Events like /agent/chat; an error event with a code (not_found/forbidden/expired/conflict) is emitted instead of an HTTP error status once the stream has started.
// @Tags        agent
// @Accept      json
// @Produce     text/event-stream
// @Security    BearerAuth
// @Param       body body     agentChatConfirmRequest true "Confirm decision"
// @Success     200  {string} string "text/event-stream"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Router      /agent/chat/confirm [post]
func (h *Handler) AgentChatConfirm(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var req agentChatConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	actionID, err := uuid.Parse(strings.TrimSpace(req.ActionID))
	if err != nil {
		respondError(c, http.StatusBadRequest, "action_id must be a valid UUID")
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if decision != "approve" && decision != "reject" {
		respondError(c, http.StatusBadRequest, "decision must be \"approve\" or \"reject\"")
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		respondError(c, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher.Flush()

	ctx := c.Request.Context()
	userSub := claims.UserID.String()

	row, err := h.agentChatLoadPendingAction(ctx, actionID)
	if err != nil {
		writeSSEEvent(c, flusher, "error", `{"code":"not_found","message":"this action was not found"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}
	if row.userSub != userSub {
		writeSSEEvent(c, flusher, "error", `{"code":"forbidden","message":"this action does not belong to you"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}
	if row.status != "pending" {
		writeSSEEvent(c, flusher, "error", `{"code":"conflict","message":"this action was already confirmed or rejected"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}
	if time.Now().After(row.expiresAt) {
		_, _ = h.agentChatConsumePendingAction(ctx, actionID, "expired")
		writeSSEEvent(c, flusher, "error", `{"code":"expired","message":"this action has expired, please ask again"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	newStatus := "rejected"
	if decision == "approve" {
		newStatus = "approved"
	}
	consumed, err := h.agentChatConsumePendingAction(ctx, actionID, newStatus)
	if err != nil {
		writeSSEEvent(c, flusher, "error", `{"code":"upstream","message":"could not record your decision, please try again"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}
	if !consumed {
		writeSSEEvent(c, flusher, "error", `{"code":"conflict","message":"this action was already confirmed or rejected"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "confirm_result", decision, &row.toolName)

	messages := append([]llmchat.Message{}, row.messagesSnapshot...)
	toolCallCount := row.toolCallCount
	writeCallCount := row.writeCallCount

	bearer := c.GetHeader("Authorization")

	if decision == "approve" {
		text, _ := h.agentChatTools.Execute(ctx, bearer, row.toolName, row.argsJSON)
		toolName := row.toolName
		h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "tool", truncateForTranscript(text, agentChatToolResultMaxLen), &toolName)
		messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: row.toolCallID, Content: text})
		toolCallCount++
		writeCallCount++
	} else {
		messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: row.toolCallID, Content: agentChatConfirmDeclineMessage})
	}

	emit := agentchat.Emitter{
		Token: func(text string) {
			writeSSEToken(c, flusher, text)
		},
		ToolCall: func(name string) {
			writeSSEEvent(c, flusher, "tool_call", fmt.Sprintf(`{"name":%q}`, name))
		},
	}

	assistantText, toolLog, nextPending, usage, err := agentchat.ResumeTurn(ctx, h.agentChatLLM, h.agentChatTools, bearer, messages, toolCallCount, writeCallCount, emit)
	h.recordAgentTokenUsage(ctx, agentTokenSourceConsoleChat, row.orgID, userSub, row.projectID, row.envID, usage)
	if err != nil {
		writeSSEEvent(c, flusher, "error", `{"code":"upstream","message":"agent could not complete this turn, please try again"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	for _, t := range toolLog {
		toolName := t.Name
		h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "tool", truncateForTranscript(t.Result, agentChatToolResultMaxLen), &toolName)
	}

	if nextPending != nil {
		h.agentChatEmitConfirmRequest(c, flusher, ctx, userSub, row.orgID, row.projectID, row.envID, nextPending)
		return
	}

	if assistantText != "" {
		h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "assistant", assistantText, nil)
	}

	writeSSEEvent(c, flusher, "done", `{"ok":true}`)
}
