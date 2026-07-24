package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

const agentChatSlowTestTrigger = "__slowtest__"
const agentChatSlowTestDuration = 75 * time.Second
const agentChatSlowTestHeartbeat = 10 * time.Second
const agentChatTokenDelay = 50 * time.Millisecond

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

// @ID          agentChat
// @Summary     Stream a chat turn with the console agent (phase-1 skeleton)
// @Description Streams Server-Sent Events for a single chat turn. Phase-1 is an echo stub with no LLM call: it tokenizes the input message and streams it back word by word, then emits a done event. Sending the literal message "__slowtest__" instead streams a 75s heartbeat run to prove the endpoint survives the ingress proxy-read-timeout.
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
	_, ok := auth.GetClaims(c)
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

	for _, word := range strings.Fields(message) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(agentChatTokenDelay):
		}
		writeSSEEvent(c, flusher, "token", word+" ")
	}
	writeSSEEvent(c, flusher, "done", `{"ok":true}`)
}
