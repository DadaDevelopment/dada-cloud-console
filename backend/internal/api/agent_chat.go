package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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

	assistantText, toolLog, err := agentchat.RunTurn(ctx, h.agentChatLLM, h.agentChatTools, bearer, systemPrompt, history, message, emit)
	if err != nil {
		writeSSEEvent(c, flusher, "error", fmt.Sprintf(`{"code":"upstream","message":%q}`, "agent could not complete this turn, please try again"))
		writeSSEEvent(c, flusher, "done", `{"ok":false}`)
		return
	}

	for _, t := range toolLog {
		toolName := t.Name
		h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "tool", truncateForTranscript(t.Result, agentChatToolResultMaxLen), &toolName)
	}
	if assistantText != "" {
		h.agentChatInsertMessage(ctx, userSub, orgID, projectID, envID, "assistant", assistantText, nil)
	}

	writeSSEEvent(c, flusher, "done", `{"ok":true}`)
}
