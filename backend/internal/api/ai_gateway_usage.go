package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/auth"
)

const agentTokenSourceGateway = "gateway"

type aiUsageRecordRequest struct {
	ProjectID         uuid.UUID  `json:"project_id" binding:"required"`
	OrgID             string     `json:"org_id"`
	EnvID             *uuid.UUID `json:"env_id"`
	Provider          string     `json:"provider" binding:"required"`
	Model             string     `json:"model" binding:"required"`
	PromptTokens      int64      `json:"prompt_tokens"`
	CompletionTokens  int64      `json:"completion_tokens"`
	TotalTokens       int64      `json:"total_tokens"`
	CostUSD           float64    `json:"cost_usd"`
	PlatformRequestID string     `json:"platform_request_id" binding:"required"`
	EndUser           string     `json:"end_user"`
	Source            string     `json:"source"`
}

// AIRecordUsage persists one gateway-observed usage row into the shared
// agent_token_usage ledger. Called by the AI Gateway's LiteLLM success
// callback for every completed call it routes -- BYOK-direct and
// console-chat alike, since both flow through the same plugin hooks
// (ADR-015: LiteLLM callback is the primary source of actual usage).
// Idempotent on platform_request_id: a retried callback collapses via
// ON CONFLICT DO NOTHING against the partial unique index.
//
// POST /internal/ai/usage/record (guarded by requireInternalToken)
func (h *Handler) AIRecordUsage(c *gin.Context) {
	var req aiUsageRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	source := req.Source
	if source == "" {
		source = agentTokenSourceGateway
	}

	var orgArg, userArg any
	if req.OrgID != "" {
		orgArg = req.OrgID
	}
	if req.EndUser != "" {
		userArg = req.EndUser
	}

	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO agent_token_usage
			(source, org_id, project_id, env_id, user_sub, model, provider,
			 prompt_tokens, completion_tokens, total_tokens, cost_usd, platform_request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (platform_request_id) WHERE platform_request_id IS NOT NULL DO NOTHING
	`, source, orgArg, req.ProjectID, req.EnvID, userArg, req.Model, req.Provider,
		req.PromptTokens, req.CompletionTokens, req.TotalTokens, req.CostUSD, req.PlatformRequestID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "record usage: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type aiGatewayProviderStat struct {
	Provider         string  `json:"provider"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type aiGatewayProjectStat struct {
	ProjectID   string  `json:"project_id"`
	ProjectName string  `json:"project_name"`
	Calls       int64   `json:"calls"`
	CostUSD     float64 `json:"cost_usd"`
}

type aiGatewayModelStat struct {
	Model   string  `json:"model"`
	Calls   int64   `json:"calls"`
	CostUSD float64 `json:"cost_usd"`
}

type aiGatewaySourceStat struct {
	Source  string  `json:"source"`
	Calls   int64   `json:"calls"`
	CostUSD float64 `json:"cost_usd"`
}

// GetAIGatewayUsage returns a provider/project/model/source cost-and-token
// breakdown of the agent_token_usage ledger over the trailing window. This is
// the AI Gateway's own dashboard -- gateway-native operational reporting
// (which provider, which project, which model, chat-vs-task), separate from
// /admin/costs' business revenue/margin view.
//
// @ID          getAIGatewayUsage
// @Summary     AI Gateway usage/cost breakdown (platform-admin only)
// @Description Returns provider/project/model/source cost-and-token breakdown of the agent_token_usage ledger over the trailing window. Platform-admin only; every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       days query    int false "Window length in days: 7 or 30 (default 7)"
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/ai-gateway/usage [get]
func (h *Handler) GetAIGatewayUsage(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	days := 7
	if v, err := strconv.Atoi(c.Query("days")); err == nil && (v == 7 || v == 30) {
		days = v
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	ctx := c.Request.Context()

	providers, err := h.aiGatewayByProvider(ctx, from, to)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "load provider breakdown: "+err.Error())
		return
	}
	projects, err := h.aiGatewayByProject(ctx, from, to)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "load project breakdown: "+err.Error())
		return
	}
	models, err := h.aiGatewayByModel(ctx, from, to)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "load model breakdown: "+err.Error())
		return
	}
	sources, err := h.aiGatewayBySource(ctx, from, to)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "load source breakdown: "+err.Error())
		return
	}

	var totalCost float64
	var totalCalls int64
	for _, p := range providers {
		totalCost += p.CostUSD
		totalCalls += p.Calls
	}

	c.JSON(http.StatusOK, gin.H{
		"days":        days,
		"window":      gin.H{"from": from, "to": to},
		"currency":    "usd",
		"total_cost":  round2(totalCost),
		"total_calls": totalCalls,
		"providers":   providers,
		"projects":    projects,
		"models":      models,
		"sources":     sources,
	})
}

func (h *Handler) aiGatewayByProvider(ctx context.Context, from, to time.Time) ([]aiGatewayProviderStat, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(provider, ''), 'unknown'),
		       COUNT(*),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(cost_usd), 0)::float8
		  FROM agent_token_usage
		 WHERE created_at >= $1 AND created_at < $2
		 GROUP BY 1
		 ORDER BY 5 DESC`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aiGatewayProviderStat{}
	for rows.Next() {
		var s aiGatewayProviderStat
		if err := rows.Scan(&s.Provider, &s.Calls, &s.PromptTokens, &s.CompletionTokens, &s.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) aiGatewayByProject(ctx context.Context, from, to time.Time) ([]aiGatewayProjectStat, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT COALESCE(atu.project_id::text, ''),
		       COALESCE(NULLIF(p.display_name, ''), atu.project_id::text, 'unassigned'),
		       COUNT(*),
		       COALESCE(SUM(atu.cost_usd), 0)::float8
		  FROM agent_token_usage atu
		  LEFT JOIN projects p ON p.id = atu.project_id
		 WHERE atu.created_at >= $1 AND atu.created_at < $2
		 GROUP BY 1, 2
		 ORDER BY 4 DESC
		 LIMIT 50`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aiGatewayProjectStat{}
	for rows.Next() {
		var s aiGatewayProjectStat
		if err := rows.Scan(&s.ProjectID, &s.ProjectName, &s.Calls, &s.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) aiGatewayByModel(ctx context.Context, from, to time.Time) ([]aiGatewayModelStat, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT model, COUNT(*), COALESCE(SUM(cost_usd), 0)::float8
		  FROM agent_token_usage
		 WHERE created_at >= $1 AND created_at < $2
		 GROUP BY model
		 ORDER BY 3 DESC
		 LIMIT 50`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aiGatewayModelStat{}
	for rows.Next() {
		var s aiGatewayModelStat
		if err := rows.Scan(&s.Model, &s.Calls, &s.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) aiGatewayBySource(ctx context.Context, from, to time.Time) ([]aiGatewaySourceStat, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT source, COUNT(*), COALESCE(SUM(cost_usd), 0)::float8
		  FROM agent_token_usage
		 WHERE created_at >= $1 AND created_at < $2
		 GROUP BY source
		 ORDER BY 3 DESC`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aiGatewaySourceStat{}
	for rows.Next() {
		var s aiGatewaySourceStat
		if err := rows.Scan(&s.Source, &s.Calls, &s.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
