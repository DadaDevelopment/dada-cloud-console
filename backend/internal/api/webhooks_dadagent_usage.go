package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const agentTokenSourceCloudTask = "cloud_task"

// dadaAgentUsageCallback is the token-metering callback the hub POSTs after a
// `claude -p` invocation finishes: one message per (invocation, model). The hub
// mints platform_request_id deterministically (ct-<cloud_task_id>-<seq>-<model>,
// ADR-015) so retries and crash-replays collapse onto the same ledger row.
//
// Tenancy is never trusted from the hub: org_id/project_id/env_id are resolved
// console-side from the correlation id (intent_id, else cloud_task_id). The
// provider USD cost IS trusted verbatim — it is Claude Code's own
// modelUsage[model].costUSD, already cache-aware, the authoritative price for
// the run. cache legs are stored since migration 147 so effective-vs-list price
// is computable per row; num_turns/provider_attempt_id/occurred_at remain
// accepted for audit parity with ADR-015 but have no ledger column.
// prompt_tokens still folds both cache legs in, matching what the gateway path
// writes.
type dadaAgentUsageCallback struct {
	PlatformRequestID string  `json:"platform_request_id"`
	ProviderAttemptID string  `json:"provider_attempt_id"`
	CloudTaskID       string  `json:"cloud_task_id"`
	IntentID          string  `json:"intent_id"`
	Source            string  `json:"source"`
	Model             string  `json:"model"`
	PromptTokens      int64   `json:"prompt_tokens"`
	CompletionTokens  int64   `json:"completion_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	CacheReadTokens   int64   `json:"cache_read_input_tokens"`
	CacheCreateTokens int64   `json:"cache_creation_input_tokens"`
	CostUSD           float64 `json:"cost_usd"`
	NumTurns          int     `json:"num_turns"`
	OccurredAt        string  `json:"occurred_at"`
}

// cloudTaskUsageRow is the resolved, tenancy-attributed ledger row the ingest
// handler writes. It carries only the fields agent_token_usage actually stores.
type cloudTaskUsageRow struct {
	platformRequestID string
	cloudTaskID       string
	orgID             string
	projectID         uuid.UUID
	envID             uuid.UUID
	model             string
	promptTokens      int64
	completionTokens  int64
	totalTokens       int64
	costUSD           float64
	cacheReadTokens   int64
	cacheCreateTokens int64
}

// DadaAgentUsageWebhook ingests per-invocation token/cost usage for cloud-task
// (`claude -p`) runs into the shared agent_token_usage ledger. Bearer-gated by
// the same JWKS verifier as the status webhook (only azp=dada-agent). Idempotent
// on platform_request_id.
func (h *Handler) DadaAgentUsageWebhook(c *gin.Context) {
	h.dadaAgentUsageWebhook(c, h.agentVerifier)
}

func (h *Handler) dadaAgentUsageWebhook(c *gin.Context, verifier tokenVerifier) {
	header := c.GetHeader("Authorization")
	raw := strings.TrimPrefix(header, "Bearer ")
	if raw == "" || raw == header {
		respondUnauthorized(c)
		return
	}
	if verifier == nil {
		respondError(c, http.StatusServiceUnavailable, "agent webhook not configured")
		return
	}
	claims, err := verifier.Verify(c.Request.Context(), raw)
	if err != nil {
		respondUnauthorized(c)
		return
	}
	if claims.Azp != "dada-agent" && !hasClient(claims, "dada-agent") {
		respondForbidden(c)
		return
	}

	var cb dadaAgentUsageCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if cb.PlatformRequestID == "" {
		respondError(c, http.StatusBadRequest, "missing platform_request_id")
		return
	}
	if cb.Model == "" {
		respondError(c, http.StatusBadRequest, "missing model")
		return
	}
	key := cb.IntentID
	if key == "" {
		key = cb.CloudTaskID
	}
	if key == "" {
		respondError(c, http.StatusBadRequest, "missing intent_id or cloud_task_id")
		return
	}

	taskID, projectID, envID, err := h.resolveCloudTaskTenancy(c.Request.Context(), key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(c, http.StatusNotFound, "unknown cloud_task")
			return
		}
		respondError(c, http.StatusInternalServerError, "tenancy lookup failed")
		return
	}

	if cb.TotalTokens <= 0 && cb.CostUSD <= 0 {
		c.JSON(http.StatusOK, gin.H{"ok": true, "stored": false})
		return
	}

	orgID, oerr := h.projectOrg(c.Request.Context(), projectID)
	if oerr != nil {
		log.Printf("cloud-task usage: org lookup failed for project %s: %v", projectID, oerr)
	}
	if orgID == "" {
		log.Printf("cloud-task usage: no org for project %s (platform_request_id=%s) — row stored unattributed",
			projectID, cb.PlatformRequestID)
	}

	// Clamp the cache legs into [0, prompt_tokens] instead of rejecting: the
	// legs are advisory pricing metadata, and a hub that sums cache slightly
	// wrong must not lose the whole billing row to a CHECK constraint.
	cacheRead := max64(0, min64(cb.CacheReadTokens, cb.PromptTokens))
	cacheCreate := max64(0, min64(cb.CacheCreateTokens, cb.PromptTokens-cacheRead))

	if err := h.recordCloudTaskTokenUsage(c.Request.Context(), cloudTaskUsageRow{
		platformRequestID: cb.PlatformRequestID,
		cloudTaskID:       taskID.String(),
		orgID:             orgID,
		projectID:         projectID,
		envID:             envID,
		model:             cb.Model,
		promptTokens:      cb.PromptTokens,
		completionTokens:  cb.CompletionTokens,
		totalTokens:       cb.TotalTokens,
		costUSD:           cb.CostUSD,
		cacheReadTokens:   cacheRead,
		cacheCreateTokens: cacheCreate,
	}); err != nil {
		respondError(c, http.StatusInternalServerError, "ledger write failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "stored": true})
}

// resolveCloudTaskTenancy maps a hub correlation id to the console-owned task
// row. The intent_id column is the universal correlation key: it holds the
// console-minted intent_id for the agentsync-intent flow, or the agent-minted
// cloud_task_id for the runs/autofix flow (see updateCloudTaskByIntent). Returns
// the canonical cloud_tasks.id plus its project/env, or pgx.ErrNoRows if unknown.
func (h *Handler) resolveCloudTaskTenancy(ctx context.Context, correlationID string) (taskID, projectID, envID uuid.UUID, err error) {
	err = h.pool.QueryRow(ctx,
		`SELECT id, project_id, environment_id FROM cloud_tasks WHERE intent_id = $1`,
		correlationID,
	).Scan(&taskID, &projectID, &envID)
	return taskID, projectID, envID, err
}

// recordCloudTaskTokenUsage upserts one cloud-task usage row into the shared
// ledger. platform_request_id is the idempotency anchor (ADR-015): a duplicate
// callback (retry, crash-replay) collapses via ON CONFLICT DO NOTHING against
// the partial unique index. cloud_task_id is intentionally non-unique here (see
// migration 053) so a task can fan out into many (invocation, model) rows.
func (h *Handler) recordCloudTaskTokenUsage(ctx context.Context, r cloudTaskUsageRow) error {
	var orgArg any
	if r.orgID != "" {
		orgArg = r.orgID
	}
	_, err := h.pool.Exec(ctx,
		`INSERT INTO agent_token_usage
			(source, org_id, project_id, env_id, user_sub, model,
			 prompt_tokens, completion_tokens, total_tokens, cost_usd,
			 cache_read_tokens, cache_creation_tokens,
			 platform_request_id, cloud_task_id)
		 VALUES ($1, $2, $3, $4, NULL, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 ON CONFLICT (platform_request_id) WHERE platform_request_id IS NOT NULL DO NOTHING`,
		agentTokenSourceCloudTask, orgArg, r.projectID, r.envID, r.model,
		r.promptTokens, r.completionTokens, r.totalTokens, r.costUSD,
		r.cacheReadTokens, r.cacheCreateTokens,
		r.platformRequestID, r.cloudTaskID,
	)
	return err
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
