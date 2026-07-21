package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// tokenVerifier is the subset of auth.KeycloakVerifier the webhook needs, kept
// as an interface so tests can inject a fake verifier without a live JWKS.
type tokenVerifier interface {
	Verify(ctx context.Context, raw string) (*auth.KeycloakClaims, error)
}

type dadaAgentCallback struct {
	CloudTaskID string `json:"cloud_task_id"`
	IntentID    string `json:"intent_id"`
	WorkflowID  string `json:"workflow_id"`
	Event       string `json:"event"`
	Status      string `json:"status"`
	PRURL       string `json:"pr_url"`
	Artifacts   []struct {
		FileID string `json:"file_id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Kind   string `json:"kind"`
	} `json:"artifacts"`
	Error string `json:"error"`
}

// mapAgentStatus folds agent task status into the cloud_tasks status enum.
func mapAgentStatus(event, status string) string {
	switch {
	case event == "completed" || status == "completed":
		return "completed"
	case event == "failed" || status == "failed":
		return "failed"
	case event == "canceled" || status == "canceled":
		return "canceled"
	default:
		return "running"
	}
}

// correlationKey picks the cloud_tasks lookup key from a callback: intent_id
// for the legacy agentsync-intent flow, falling back to cloud_task_id for the
// runs-based flow (autofix and any future /v1/runs skill), which always sends
// intent_id as null since it has no LangGraph workflow behind it.
func correlationKey(cb dadaAgentCallback) string {
	if cb.IntentID != "" {
		return cb.IntentID
	}
	return cb.CloudTaskID
}

// hasClient reports whether the agent client appears under resource_access.
func hasClient(claims *auth.KeycloakClaims, client string) bool {
	for _, c := range claims.ResourceAccessClients {
		if c == client {
			return true
		}
	}
	return false
}

// DadaAgentWebhook ingests agent status/artifact callbacks. Bearer-gated by JWKS;
// only the agent's own client (azp=dada-agent) is accepted. Idempotent updates.
func (h *Handler) DadaAgentWebhook(c *gin.Context) {
	h.dadaAgentWebhook(c, h.agentVerifier)
}

func (h *Handler) dadaAgentWebhook(c *gin.Context, verifier tokenVerifier) {
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

	var cb dadaAgentCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	key := correlationKey(cb)
	if key == "" {
		respondError(c, http.StatusBadRequest, "missing intent_id or cloud_task_id")
		return
	}
	var artifactsJSON []byte
	if len(cb.Artifacts) > 0 {
		artifactsJSON, _ = json.Marshal(cb.Artifacts)
	}
	if err := h.updateCloudTaskByIntent(c.Request.Context(), key,
		mapAgentStatus(cb.Event, cb.Status), cb.PRURL, artifactsJSON, cb.Error); err != nil {
		respondError(c, http.StatusInternalServerError, "update failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
