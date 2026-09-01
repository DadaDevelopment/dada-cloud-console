package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// sendAgentMessageRequest is the body SendAgentMessage accepts.
type sendAgentMessageRequest struct {
	Text string `json:"text"`
}

// SendAgentMessage sends one text message straight to an agent's A2A endpoint
// (internal/tggateway/a2a.go) and returns its reply, synchronously, stateless
// across calls.
//
// @ID          sendAgentMessage
// @Summary     Send one message to an agent and get its reply
// @Description POSTs a JSON-RPC 2.0 message/send to the agent's cluster-internal A2A endpoint and returns the reply text. Stateless: no conversation history carries between calls. 400 for an unknown agent name, 502 when the agent's A2A endpoint errors or times out (it can take up to 90s), 200 with a note when the agent pauses on an input-required (human-in-the-loop) tool mid-turn.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       agentName path     string                  true "Agent name"
// @Param       request   body     sendAgentMessageRequest true "Message text"
// @Success     200       {object} map[string]string       "reply"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /agents/{agentName}/message [post]
func (h *Handler) SendAgentMessage(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	if h.a2a == nil {
		respondError(c, http.StatusServiceUnavailable, "agent messaging is not configured on this console")
		return
	}

	agentName := c.Param("agentName")
	var req sendAgentMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Text == "" {
		respondError(c, http.StatusBadRequest, "text is required")
		return
	}

	if _, err := h.agentProjectID(c.Request.Context(), agentName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(c, http.StatusBadRequest, "unknown agent")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to resolve agent")
		return
	}

	reply, err := h.a2a.Send(c.Request.Context(), agentName, req.Text)
	if err != nil {
		respondError(c, http.StatusBadGateway, "agent did not answer: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply})
}
