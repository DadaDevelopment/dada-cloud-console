package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sse"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/llmchat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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

// writeSSEEvent frames one Server-Sent Event via the spec-compliant gin sse
// encoder rather than a hand-rolled Fprintf. This matters for assistant text
// deltas: sse.Encode rewrites every embedded newline into an additional
// "data:" continuation line, so a multi-line delta survives the wire intact.
// The prior "data: %s" form emitted the newline raw, which split the frame and
// let the client drop the tail line. The encoder also adds no cosmetic leading
// space, so the client must not strip one (see streamSSE in
// frontend/components/agent-chat-panel.tsx) or it would eat significant leading
// spaces in a delta.
func writeSSEEvent(c *gin.Context, flusher http.Flusher, event string, data string) {
	_ = sse.Encode(c.Writer, sse.Event{Event: event, Data: data})
	flusher.Flush()
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

// agentChatContextClearedAt returns the most recent "clear context" timestamp
// for this user/project/env scope, or the zero time if the user has never
// cleared it (in which case a `created_at > zeroTime` filter is always true,
// i.e. a no-op). Reads from agent_chat_context_resets, which is append-only --
// clearing never deletes the underlying agent_chat_messages rows, so the daily
// message cap (agentChatDailyMessageCount) and the audit trail stay accurate
// regardless of how many times a conversation gets cleared.
func (h *Handler) agentChatContextClearedAt(ctx context.Context, userSub string, projectID, envID *uuid.UUID) time.Time {
	var clearedAt time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT cleared_at FROM agent_chat_context_resets
		 WHERE user_sub = $1 AND project_id IS NOT DISTINCT FROM $2 AND env_id IS NOT DISTINCT FROM $3
		 ORDER BY cleared_at DESC LIMIT 1`,
		userSub, projectID, envID,
	).Scan(&clearedAt)
	if err != nil {
		return time.Time{}
	}
	return clearedAt
}

func (h *Handler) agentChatHistory(ctx context.Context, userSub string, projectID, envID *uuid.UUID) []llmchat.Message {
	clearedAt := h.agentChatContextClearedAt(ctx, userSub, projectID, envID)
	rows, err := h.pool.Query(ctx,
		`SELECT role, content FROM (
			SELECT role, content, created_at FROM agent_chat_messages
			WHERE user_sub = $1
			  AND project_id IS NOT DISTINCT FROM $2
			  AND env_id IS NOT DISTINCT FROM $3
			  AND role IN ('user', 'assistant')
			  AND content <> ''
			  AND created_at > $5
			ORDER BY created_at DESC
			LIMIT $4
		 ) recent ORDER BY created_at ASC`,
		userSub, projectID, envID, agentChatHistoryLimit, clearedAt,
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
	priceRub         *float64
}

func (h *Handler) agentChatInsertPendingAction(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, pending *agentchat.PendingWrite, priceRub *float64) (uuid.UUID, error) {
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
			 messages_snapshot, tool_call_count, write_call_count, status, expires_at, price_rub)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', $11, $12)
		 RETURNING id`,
		userSub, orgArg, projectID, envID, pending.ToolName, args, pending.ToolCallID,
		snapshot, pending.ToolCallCount, pending.WriteCallCount, time.Now().Add(agentChatPendingActionTTL), priceRub,
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
		        messages_snapshot, tool_call_count, write_call_count, status, expires_at, price_rub
		   FROM agent_chat_pending_actions WHERE id = $1`,
		actionID,
	).Scan(&row.id, &row.userSub, &orgID, &row.projectID, &row.envID, &row.toolName, &row.argsJSON,
		&row.toolCallID, &snapshotRaw, &row.toolCallCount, &row.writeCallCount, &row.status, &row.expiresAt, &row.priceRub)
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

// agentChatRecordAuditEvent logs the human's approve/reject decision on an
// agent-proposed write action into the platform's regular audit_events table,
// distinct from the tool's own audit row (which only fires on a successful
// approve+execute, same as a manual form submit). This one fires for BOTH
// approve and reject, and independent of whether the underlying tool call
// ultimately succeeds -- it records the human decision, not the technical
// outcome -- so "the agent proposed X, the user said no" is visible to admins
// too, not just in agent_chat_messages. Best-effort: never blocks the
// confirm/decline response on a logging failure.
func (h *Handler) agentChatRecordAuditEvent(ctx context.Context, actorID uuid.UUID, row *agentChatPendingRow, decision string) {
	action := "AgentChatActionDeclined"
	if decision == "approve" {
		action = "AgentChatActionApproved"
	}

	args := map[string]any{}
	_ = json.Unmarshal([]byte(nonEmptyJSON(row.argsJSON)), &args)
	metadata, err := json.Marshal(map[string]any{
		"tool_name": row.toolName,
		"action_id": row.id,
		"args":      args,
		"price_rub": row.priceRub,
	})
	if err != nil {
		log.Printf("agent-chat: failed to marshal audit metadata for action %s: %v", row.id, err)
		return
	}

	if _, err := h.pool.Exec(ctx,
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		actorID, row.projectID, action, row.toolName, row.toolName, metadata,
	); err != nil {
		log.Printf("agent-chat: failed to record audit event for action %s: %v", row.id, err)
	}
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
	case "createEndpoint":
		fqdn, _ := args["fqdn"].(string)
		authEnabled, _ := args["auth_enabled"].(bool)
		swaggerEnabled, _ := args["swagger_enabled"].(bool)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Register public endpoint %q for app %q", fqdn, appName))
		if projectName != "" || envName != "" {
			sb.WriteString(fmt.Sprintf(" in project %q, environment %q", projectName, envName))
		}
		if authEnabled {
			sb.WriteString(", with auth enabled")
		} else {
			sb.WriteString(", publicly accessible (no auth)")
		}
		if swaggerEnabled {
			sb.WriteString(", Swagger docs published")
		}
		sb.WriteString(".")
		return sb.String()
	case "createS3Bucket":
		name, _ := args["name"].(string)
		bucketName, _ := args["bucket_name"].(string)
		region, _ := args["region"].(string)
		public, _ := args["public"].(bool)
		appRef, _ := args["app_ref"].(string)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Create an S3 storage bucket %q (bucket=%q", name, bucketName))
		if region != "" {
			sb.WriteString(fmt.Sprintf(", region=%q", region))
		}
		sb.WriteString(")")
		if projectName != "" || envName != "" {
			sb.WriteString(fmt.Sprintf(" in project %q, environment %q", projectName, envName))
		}
		if appRef != "" {
			sb.WriteString(fmt.Sprintf(", bound to app %q", appRef))
		}
		if public {
			sb.WriteString(", publicly readable")
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
	sb.WriteString(" You can order a managed PostgreSQL database (createDatabase), a public endpoint for an app (createEndpoint), or an S3 storage bucket (createS3Bucket). All three require a specific projectId and envId (environment), and createEndpoint also requires a real appName -- these are NOT things you may invent. ")
	sb.WriteString("If envId (or, for createEndpoint, appName) is not already given above, ask the user before calling any of these tools. ")
	sb.WriteString("If the user says to choose for them, first call getProject/listProjects (and listApps for an appName) to see what actually exists, pick a sensible one (prefer an environment named prod if several exist and the user gave no other hint), and explicitly state what you picked before calling the tool -- never guess an envId or appName you have not looked up. ")
	sb.WriteString("Every one of these three tools always pauses for the user's explicit confirmation in the UI before it actually runs, so propose the call as soon as you have resolved its required fields; you do not need the user to also confirm in chat first. ")
	sb.WriteString("createAppServer (a real, billed virtual machine) and createApp are not available to you yet -- if the user asks for a VM or to deploy a new app, tell them that's not supported from chat yet and point them at the console UI. ")
	sb.WriteString("Naming rules for every resource name you pick yourself (createDatabase's name and database fields, createS3Bucket's name and bucket_name fields, any future VM/app name): lowercase letters, digits, and hyphens ONLY -- no underscores, no spaces, no uppercase, no leading/trailing hyphen, max 63 characters; database additionally must START with a letter, not a digit or hyphen. If the user gives you a name with underscores, spaces, or uppercase (e.g. \"my_database\", \"My DB\"), silently convert it to a valid one (underscores/spaces to hyphens, lowercase) instead of guessing and retrying after a rejection -- state the converted name you're using in the confirmation summary so the user can object. Getting this right on the first call matters: every write-tool attempt that fails backend validation still consumes this turn's limited tool-call budget, and a bad name is the single most common way to burn through it without ever creating anything.")
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
			writeSSEEvent(c, flusher, "token", text)
		},
		ToolCall: func(name string) {
			writeSSEEvent(c, flusher, "tool_call", fmt.Sprintf(`{"name":%q}`, name))
		},
	}

	assistantText, toolLog, pending, _, err := agentchat.RunTurn(ctx, h.agentChatLLM, h.agentChatTools, bearer, userSub, systemPrompt, history, message, emit)
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

// agentChatSummaryFor computes a confirmation-card summary for a write-tool
// call, resolving project/env names from the call's OWN args (the ground
// truth for what will actually execute) rather than any stale console
// context. Shared by the live confirm_request path and the history endpoint's
// reconstruction of a still-open pending action after a page reload.
// toolsNeedingProjectEnvNames are write tools whose args carry their own
// projectId/envId (they can target somewhere other than the console's current
// selection, e.g. an environment the agent resolved itself), so their summary
// needs those names resolved from the args rather than left blank. The other
// write tools are app-scoped within whatever env is already selected, where
// appName alone reads unambiguously without a project/env lookup.
var toolsNeedingProjectEnvNames = map[string]bool{
	"createDatabase": true,
	"createEndpoint": true,
	"createS3Bucket": true,
}

func (h *Handler) agentChatSummaryFor(ctx context.Context, toolName, argsJSON string) string {
	var projectName, envName string
	if toolsNeedingProjectEnvNames[toolName] {
		targetProjectID := extractUUIDArg(argsJSON, "projectId")
		targetEnvID := extractUUIDArg(argsJSON, "envId")
		projectName, envName = h.agentChatResolveNames(ctx, targetProjectID, targetEnvID)
	}
	return agentChatConfirmSummary(toolName, argsJSON, projectName, envName)
}

// agentChatPriceEstimateRUB returns a deterministic, non-agent-controlled
// monthly cost estimate for a write-tool call, reusing the exact same cost
// model the billing dashboard already uses (billing_fullcost.go's
// estimateFootprintDB + estimateCost, derived from config/billing/cluster-cost.yaml
// and the live consumption snapshot's overhead/margin) -- this is never
// something the LLM is asked to state, and it's computed ONCE here at
// proposal time and persisted, so the price the user sees on the card is
// exactly the price recorded to the audit trail, even if the billing
// snapshot changes before the user confirms. Returns nil when the tool has no
// fixed recurring footprint to price at creation time (createEndpoint is
// free; createS3Bucket bills by actual bytes stored later, not by a size
// chosen at creation).
func (h *Handler) agentChatPriceEstimateRUB(toolName string) *float64 {
	switch toolName {
	case "createDatabase":
		v := h.estimateCost(estimateFootprintDB, h.billingSnapshot().pricing)
		return &v
	default:
		return nil
	}
}

func (h *Handler) agentChatEmitConfirmRequest(c *gin.Context, flusher http.Flusher, ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, pending *agentchat.PendingWrite) {
	priceRub := h.agentChatPriceEstimateRUB(pending.ToolName)

	// The tool call's own args are the ground truth for what will actually
	// execute -- store THOSE as the pending action's project/env scope, not
	// the console context this turn happened to start with. The two can
	// legitimately differ (no project selected yet, or the agent resolved its
	// own env per the "choose for me" system-prompt instruction), and every
	// downstream read of this row (history reconstruction, the confirm_request
	// transcript row, the resumed turn's tool/assistant messages) needs the
	// real target, not a stale/absent console selection.
	targetProjectID, targetEnvID := projectID, envID
	if toolsNeedingProjectEnvNames[pending.ToolName] {
		if fromArgs := extractUUIDArg(pending.ArgsJSON, "projectId"); fromArgs != nil {
			targetProjectID = fromArgs
		}
		if fromArgs := extractUUIDArg(pending.ArgsJSON, "envId"); fromArgs != nil {
			targetEnvID = fromArgs
		}
	}

	actionID, err := h.agentChatInsertPendingAction(ctx, userSub, orgID, targetProjectID, targetEnvID, pending, priceRub)
	if err != nil {
		log.Printf("agent-chat: failed to persist pending action: %v", err)
		writeSSEEvent(c, flusher, "error", `{"code":"upstream","message":"agent could not prepare this action for confirmation, please try again"}`)
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	summary := h.agentChatSummaryFor(ctx, pending.ToolName, pending.ArgsJSON)
	payload, _ := json.Marshal(map[string]any{
		"action_id": actionID,
		"tool_name": pending.ToolName,
		"args":      json.RawMessage(nonEmptyJSON(pending.ArgsJSON)),
		"summary":   summary,
		"price_rub": priceRub,
	})
	h.agentChatInsertMessage(ctx, userSub, orgID, targetProjectID, targetEnvID, "confirm_request", summary, &pending.ToolName)

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

	h.agentChatRecordAuditEvent(ctx, claims.UserID, row, decision)
	h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "confirm_result", decision, &row.toolName)

	messages := append([]llmchat.Message{}, row.messagesSnapshot...)
	toolCallCount := row.toolCallCount
	writeCallCount := row.writeCallCount

	bearer := c.GetHeader("Authorization")

	if decision == "approve" {
		text, isError := h.agentChatTools.Execute(ctx, bearer, row.toolName, row.argsJSON)
		toolName := row.toolName
		h.agentChatInsertMessage(ctx, userSub, row.orgID, row.projectID, row.envID, "tool", truncateForTranscript(text, agentChatToolResultMaxLen), &toolName)
		messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: row.toolCallID, Content: text})
		toolCallCount++
		// The scarce per-turn WRITE budget (unlike the general tool-call
		// budget above) exists to bound how many resources one turn can
		// actually create, not how many times the agent tries. A rejected
		// attempt (bad name, quota exceeded, etc.) created nothing, so it must
		// not count against it -- otherwise a single bad name that the model
		// has to retry a few times exhausts the write budget before anything
		// is ever actually created, and the turn gives up for no real reason.
		if !isError {
			writeCallCount++
		}
	} else {
		messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: row.toolCallID, Content: agentChatConfirmDeclineMessage})
	}

	emit := agentchat.Emitter{
		Token: func(text string) {
			writeSSEEvent(c, flusher, "token", text)
		},
		ToolCall: func(name string) {
			writeSSEEvent(c, flusher, "tool_call", fmt.Sprintf(`{"name":%q}`, name))
		},
	}

	assistantText, toolLog, nextPending, _, err := agentchat.ResumeTurn(ctx, h.agentChatLLM, h.agentChatTools, bearer, userSub, messages, toolCallCount, writeCallCount, emit)
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

type agentChatHistoryMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"toolName,omitempty"`
}

type agentChatPendingActionDTO struct {
	ActionID string         `json:"actionId"`
	ToolName string         `json:"toolName"`
	Args     map[string]any `json:"args"`
	Summary  string         `json:"summary"`
	PriceRub *float64       `json:"priceRub,omitempty"`
}

type agentChatHistoryResponse struct {
	Messages      []agentChatHistoryMessage  `json:"messages"`
	PendingAction *agentChatPendingActionDTO `json:"pendingAction"`
}

// agentChatFindOpenPendingAction looks up the most recent still-pending,
// unexpired write action for this user/project/env scope -- used to
// reconstruct an interrupted confirmation card after a page reload, since the
// SSE stream that originally emitted confirm_request is long gone.
func (h *Handler) agentChatFindOpenPendingAction(ctx context.Context, userSub string, projectID, envID *uuid.UUID) (*agentChatPendingRow, error) {
	var row agentChatPendingRow
	var orgID *string
	var snapshotRaw []byte
	err := h.pool.QueryRow(ctx,
		`SELECT id, user_sub, org_id, project_id, env_id, tool_name, args_json, tool_call_id,
		        messages_snapshot, tool_call_count, write_call_count, status, expires_at, price_rub
		   FROM agent_chat_pending_actions
		  WHERE user_sub = $1
		    AND project_id IS NOT DISTINCT FROM $2
		    AND env_id IS NOT DISTINCT FROM $3
		    AND status = 'pending'
		    AND expires_at > now()
		  ORDER BY created_at DESC
		  LIMIT 1`,
		userSub, projectID, envID,
	).Scan(&row.id, &row.userSub, &orgID, &row.projectID, &row.envID, &row.toolName, &row.argsJSON,
		&row.toolCallID, &snapshotRaw, &row.toolCallCount, &row.writeCallCount, &row.status, &row.expiresAt, &row.priceRub)
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

// @ID          agentChatGetHistory
// @Summary     Get persisted chat history for this project/env, plus any still-open confirmation
// @Description Reads back what's already in agent_chat_messages (and, if one is still open, the pending write-action awaiting confirm/reject) so the panel can restore a conversation after a page reload -- the browser-side message list is otherwise pure in-memory React state and disappears on refresh. Read-only; does not touch the LLM gateway or the daily message cap.
// @Tags        agent
// @Produce     json
// @Security    BearerAuth
// @Param       projectId query    string false "Project UUID"
// @Param       envId     query    string false "Environment UUID"
// @Success     200       {object} agentChatHistoryResponse
// @Failure     401       {object} map[string]string
// @Router      /agent/chat/history [get]
func (h *Handler) AgentChatGetHistory(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	ctx := c.Request.Context()
	userSub := claims.UserID.String()
	projectID := parseOptionalUUID(c.Query("projectId"))
	envID := parseOptionalUUID(c.Query("envId"))
	clearedAt := h.agentChatContextClearedAt(ctx, userSub, projectID, envID)

	rows, err := h.pool.Query(ctx,
		`SELECT role, content, tool_name FROM (
			SELECT role, content, tool_name, created_at FROM agent_chat_messages
			WHERE user_sub = $1
			  AND project_id IS NOT DISTINCT FROM $2
			  AND env_id IS NOT DISTINCT FROM $3
			  AND role IN ('user', 'assistant', 'tool')
			  AND created_at > $5
			ORDER BY created_at DESC
			LIMIT $4
		 ) recent ORDER BY created_at ASC`,
		userSub, projectID, envID, agentChatHistoryLimit, clearedAt,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load chat history")
		return
	}
	defer rows.Close()

	messages := []agentChatHistoryMessage{}
	for rows.Next() {
		var m agentChatHistoryMessage
		var toolName *string
		if err := rows.Scan(&m.Role, &m.Content, &toolName); err != nil {
			continue
		}
		if toolName != nil {
			m.ToolName = *toolName
		}
		messages = append(messages, m)
	}

	var pending *agentChatPendingActionDTO
	if pendingRow, perr := h.agentChatFindOpenPendingAction(ctx, userSub, projectID, envID); perr == nil && pendingRow != nil {
		args := map[string]any{}
		_ = json.Unmarshal([]byte(nonEmptyJSON(pendingRow.argsJSON)), &args)
		pending = &agentChatPendingActionDTO{
			ActionID: pendingRow.id.String(),
			ToolName: pendingRow.toolName,
			Args:     args,
			Summary:  h.agentChatSummaryFor(ctx, pendingRow.toolName, pendingRow.argsJSON),
			PriceRub: pendingRow.priceRub,
		}
	}

	c.JSON(http.StatusOK, agentChatHistoryResponse{Messages: messages, PendingAction: pending})
}

type agentChatClearContextRequest struct {
	ProjectID string `json:"projectId"`
	EnvID     string `json:"envId"`
}

// @ID          agentChatClearContext
// @Summary     Clear the agent's conversation context for this project/env scope
// @Description Resets what the next chat turn sends the LLM as history and what GET /agent/chat/history reconstructs on reload -- a cluttered/stale conversation can be started fresh without losing anything real. Past agent_chat_messages rows are NOT deleted: the daily message cap and the confirm/decline audit trail are computed from those rows and are completely unaffected by clearing. Also auto-declines any write-action confirmation still open in this scope, since resuming it against a conversation the user just asked to forget would be confusing.
// @Tags        agent
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     agentChatClearContextRequest true "Scope to clear (projectId/envId may be empty for the no-project-selected scope)"
// @Success     200  {object} map[string]bool
// @Failure     401  {object} map[string]string
// @Router      /agent/chat/context/clear [post]
func (h *Handler) AgentChatClearContext(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var req agentChatClearContextRequest
	_ = c.ShouldBindJSON(&req)
	projectID := parseOptionalUUID(req.ProjectID)
	envID := parseOptionalUUID(req.EnvID)
	userSub := claims.UserID.String()
	ctx := c.Request.Context()

	if _, err := h.pool.Exec(ctx,
		`INSERT INTO agent_chat_context_resets (user_sub, project_id, env_id, cleared_at) VALUES ($1, $2, $3, now())`,
		userSub, projectID, envID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to clear context")
		return
	}

	if pendingRow, err := h.agentChatFindOpenPendingAction(ctx, userSub, projectID, envID); err == nil && pendingRow != nil {
		_, _ = h.agentChatConsumePendingAction(ctx, pendingRow.id, "declined")
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
