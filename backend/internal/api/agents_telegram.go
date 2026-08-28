package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/tggatewayclient"
)

// agentProjectID resolves the project a named agent belongs to, from the same
// resource_snapshots table ListAgents reads (a claim wins over a raw CR of the
// same name, matching agentSnapshotKind). Telegram binding needs it because
// tg-gateway's own bindings row carries project_id, even though this route
// (like ListAgentTools/GetAgentState) is not itself project-scoped in the URL.
func (h *Handler) agentProjectID(ctx context.Context, agentName string) (string, error) {
	var projectID string
	err := h.pool.QueryRow(ctx,
		`SELECT project_id::text FROM resource_snapshots
		 WHERE kind IN ('ManagedAgent', 'Agent') AND name = $1
		 ORDER BY (kind = 'ManagedAgent') DESC
		 LIMIT 1`, agentName,
	).Scan(&projectID)
	return projectID, err
}

// bindAgentTelegramRequest is the body BindAgentTelegram accepts.
type bindAgentTelegramRequest struct {
	BotToken string `json:"bot_token"`
}

// BindAgentTelegram connects a Telegram bot to an agent: the token is
// validated against Telegram's getMe by tg-gateway before anything is
// persisted, so a bad token is a field error in the modal rather than a
// silent dead poller.
//
// @ID          bindAgentTelegram
// @Summary     Connect a Telegram bot to an agent
// @Description Validates the bot token via Telegram getMe (through tg-gateway) and starts a long-poll bridge between the bot and the agent's A2A endpoint. 400 when the token is invalid, 503 when tg-gateway is unreachable or unconfigured.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       agentName path     string                     true "Agent name"
// @Param       request   body     bindAgentTelegramRequest   true "Bot token"
// @Success     200       {object} map[string]string          "bot_username"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /agents/{agentName}/telegram [post]
func (h *Handler) BindAgentTelegram(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	if h.tgGateway == nil {
		respondError(c, http.StatusServiceUnavailable, "telegram binding is not configured on this console")
		return
	}

	agentName := c.Param("agentName")
	var req bindAgentTelegramRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.BotToken == "" {
		respondError(c, http.StatusBadRequest, "bot_token is required")
		return
	}

	projectID, err := h.agentProjectID(c.Request.Context(), agentName)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(c, http.StatusBadRequest, "unknown agent")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve agent project")
		return
	}

	binding, err := h.tgGateway.Bind(c.Request.Context(), agentName, projectID, req.BotToken)
	if errors.Is(err, tggatewayclient.ErrInvalidToken) {
		respondError(c, http.StatusBadRequest, "invalid bot token")
		return
	}
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "telegram gateway is not reachable from this console")
		return
	}
	c.JSON(http.StatusOK, gin.H{"bot_username": binding.BotUsername})
}

// UnbindAgentTelegram disconnects an agent's Telegram bot, if any.
//
// @ID          unbindAgentTelegram
// @Summary     Disconnect an agent's Telegram bot
// @Description Stops the long-poll bridge and forgets the token. Safe to call for an agent with no binding.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       agentName path     string true "Agent name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /agents/{agentName}/telegram [delete]
func (h *Handler) UnbindAgentTelegram(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	if h.tgGateway == nil {
		respondError(c, http.StatusServiceUnavailable, "telegram binding is not configured on this console")
		return
	}

	if err := h.tgGateway.Unbind(c.Request.Context(), c.Param("agentName")); err != nil {
		respondError(c, http.StatusServiceUnavailable, "telegram gateway is not reachable from this console")
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// GetAgentTelegram reports whether an agent is bound to a Telegram bot.
//
// @ID          getAgentTelegram
// @Summary     Get an agent's Telegram binding
// @Description Returns whether the agent has a bot connected, and its @username when it does. Reports bound=false rather than 404 when there is none.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       agentName path     string true "Agent name"
// @Success     200       {object} map[string]interface{} "bound, bot_username"
// @Failure     401       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /agents/{agentName}/telegram [get]
func (h *Handler) GetAgentTelegram(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	if h.tgGateway == nil {
		respondError(c, http.StatusServiceUnavailable, "telegram binding is not configured on this console")
		return
	}

	binding, err := h.tgGateway.Get(c.Request.Context(), c.Param("agentName"))
	if errors.Is(err, tggatewayclient.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"bound": false})
		return
	}
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "telegram gateway is not reachable from this console")
		return
	}
	c.JSON(http.StatusOK, gin.H{"bound": binding.Bound, "bot_username": binding.BotUsername})
}
