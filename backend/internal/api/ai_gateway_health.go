package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/metrics"
)

// aiFailureRecordRequest is one upstream refusal the gateway saw, or one
// request a fallback group answered.
//
// ModelGroup is the alias the router was serving when it failed, which for a
// fallback is the group it fell into rather than the one the caller named --
// Requested carries that. Status is the provider's HTTP status where the
// exception exposed one; a transport error that never reached the provider
// arrives as 0.
type aiFailureRecordRequest struct {
	ModelGroup    string `json:"model_group"`
	Requested     string `json:"requested"`
	Provider      string `json:"provider"`
	Status        int    `json:"status"`
	ExceptionType string `json:"exception_type"`
	Fallback      bool   `json:"fallback"`
	CredentialID  string `json:"credential_id"`
}

func aiCredentialCooldown(status int) time.Duration {
	switch {
	case status == 401 || status == 403:
		return 24 * time.Hour
	case status == 402:
		return time.Hour
	case status == 429:
		return time.Minute
	case status >= 500:
		return 30 * time.Second
	default:
		return 0
	}
}

// AIRecordFailure counts an AI gateway upstream failure so an alert can see it.
//
// This endpoint exists because of 2026-08-04: the shared OpenRouter account ran
// out of credit, the gateway answered 402 to 66 of 66 calls over three hours,
// every or-* alias went down together, and nothing anywhere raised a signal.
// The failure was found by reading pod logs by hand. The gateway's usage
// callback only ever reported successes -- a call that never happened writes no
// ledger row, so the one shape of trouble that matters most was the one shape
// the platform could not see.
//
// Nothing is persisted. The counters are in-process, which is the right storage
// for "is this still happening": increase() over a window survives a restart,
// sums across the console's two replicas, and does not put non-billable events
// in agent_token_usage, which is a billing table.
//
// Labels are rejected rather than truncated when the gateway names a group the
// console does not know: an unbounded label from a request body is how a
// metrics stack falls over during the outage it was supposed to report. An
// unknown name still counts, under "other", so a mismatch between the two repos
// shows up as a visible bucket rather than as silence.
//
// Best-effort by contract: the gateway fires this after the response has
// already gone out and ignores the result, so this handler must never be the
// reason a caller waits.
//
// POST /internal/ai/failure/record (guarded by requireInternalToken)
func (h *Handler) AIRecordFailure(c *gin.Context) {
	var req aiFailureRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Fallback {
		metrics.RecordAIFallback(aiMetricLabel(req.Requested), aiMetricLabel(req.ModelGroup))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	metrics.RecordAIUpstreamFailure(
		aiMetricLabel(req.ModelGroup), aiProviderLabel(req.Provider), req.Status)
	if h.pool != nil {
		if credentialID, err := uuid.Parse(req.CredentialID); err == nil {
			cooldown := aiCredentialCooldown(req.Status)
			if cooldown > 0 {
				_, _ = h.pool.Exec(c.Request.Context(), `UPDATE ai_gateway_key_credentials
					SET status='cooldown',unavailable_until=now()+$2 * interval '1 second',updated_at=now()
					WHERE id=$1 AND deleted_at IS NULL`, credentialID, int64(cooldown/time.Second))
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// aiProviderLabel bounds the provider label the same way.
//
// isKnownAIProvider alone is not enough: it answers the BYOK question -- which
// providers a customer may store a key for -- and nvidia_nim is deliberately
// absent from that list because the platform holds the only key. It is also the
// provider behind every tier, so leaving it out here would file the platform's
// own failures under "other", which is exactly the case this metric exists for.
func aiProviderLabel(provider string) string {
	if provider == "" {
		return ""
	}
	if provider == aiPlatformOwnedProvider || isKnownAIProvider(provider) {
		return provider
	}
	return "other"
}

// aiMetricLabel keeps the model-group label inside the catalog the console
// already knows, so the series count is bounded by config.yaml rather than by
// whatever the gateway sends.
func aiMetricLabel(group string) string {
	if group == "" {
		return ""
	}
	if !isKnownAIAlias(group) {
		return "other"
	}
	return group
}
