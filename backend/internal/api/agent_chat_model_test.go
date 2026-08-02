package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/llmchat"
)

// TestAgentChatModelFor_HonoursOnlyTheAllowlist pins the whole point of the
// per-request model override: it exists so a model can be measured against the
// live tool catalog, not so any caller can redirect the platform's gateway
// budget. An empty allowlist (the default deployment) must ignore every
// request.
func TestAgentChatModelFor_HonoursOnlyTheAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		requested string
		want      string
	}{
		{"no allowlist ignores the request", nil, "gpt-4o", ""},
		{"allowlisted model is honoured", []string{"gpt-4o", "claude"}, "gpt-4o", "gpt-4o"},
		{"model outside the allowlist is dropped", []string{"gpt-4o"}, "claude", ""},
		{"empty request keeps the default", []string{"gpt-4o"}, "", ""},
		{"whitespace request keeps the default", []string{"gpt-4o"}, "   ", ""},
		{"case and padding do not matter", []string{" GPT-4o "}, "gpt-4o", "gpt-4o"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{cfg: &config.Config{AgentChatModelAllowlist: tc.allowlist}}
			if got := h.agentChatModelFor(tc.requested); got != tc.want {
				t.Fatalf("agentChatModelFor(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

// TestAgentChatUpstreamErrorCode_KeepsTheStatus guards the diagnosability of a
// dead chat: on 2026-08-03 every turn failed with a flat "upstream" while the
// real cause was the provider answering 429 for one model group, which cost an
// hour of guessing that the trace row could have answered.
func TestAgentChatUpstreamErrorCode_KeepsTheStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, "upstream"},
		{"rate limited model group", fmt.Errorf("gateway status 429: {\"error\":{\"code\":\"429\"}}"), "upstream_429"},
		{"bad key", errors.New("gateway status 401: invalid api key"), "upstream_401"},
		{"wrapped", fmt.Errorf("stream chat: %w", errors.New("gateway status 500: boom")), "upstream_500"},
		{"transport failure has no status", errors.New("gateway request: dial tcp: i/o timeout"), "upstream"},
		{"streamed error has no status", errors.New("gateway error: context length exceeded"), "upstream"},
		{"truncated status", errors.New("gateway status : weird"), "upstream"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentChatUpstreamErrorCode(tc.err); got != tc.want {
				t.Fatalf("agentChatUpstreamErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestLLMChatWithModel_DoesNotMutateTheSharedClient pins that an A/B turn
// cannot leak its model into every other user's turn: the handler holds one
// long-lived client and clones it per request.
func TestLLMChatWithModel_DoesNotMutateTheSharedClient(t *testing.T) {
	base := llmchat.New("http://gateway.test", "key", "claude-haiku")

	if got := base.WithModel("gpt-4o"); got.Model != "gpt-4o" {
		t.Fatalf("clone model = %q, want gpt-4o", got.Model)
	}
	if base.Model != "claude-haiku" {
		t.Fatalf("shared client was mutated: model = %q", base.Model)
	}
	if got := base.WithModel(""); got != base {
		t.Fatalf("empty model must return the same client, got a copy")
	}
	if got := base.WithModel("claude-haiku"); got != base {
		t.Fatalf("same model must return the same client, got a copy")
	}
	var nilClient *llmchat.Client
	if got := nilClient.WithModel("gpt-4o"); got != nil {
		t.Fatalf("nil client must stay nil, got %v", got)
	}
}
