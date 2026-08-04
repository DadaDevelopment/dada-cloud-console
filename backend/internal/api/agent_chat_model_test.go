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
		{"allowlisted model is honoured", []string{"gpt-4o", "or-gpt-4o-mini"}, "gpt-4o", "gpt-4o"},
		{"model outside the allowlist is dropped", []string{"gpt-4o"}, "groq-llama", ""},
		{"empty request keeps the default", []string{"gpt-4o"}, "", ""},
		{"whitespace request keeps the default", []string{"gpt-4o"}, "   ", ""},
		{"case and padding do not matter", []string{" GPT-4o "}, "gpt-4o", "gpt-4o"},
		{"an allowlisted anthropic alias is still refused", []string{"claude", "claude-haiku"}, "claude", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{cfg: &config.Config{AgentChatModelAllowlist: tc.allowlist}}
			if got := h.agentChatModelFor(tc.requested, "user-1"); got != tc.want {
				t.Fatalf("agentChatModelFor(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

// TestAgentChatDefaultModel_NeverStartsOnAnthropic pins the removal itself.
// Production ran AGENT_CHAT_MODEL=claude-haiku, which routes through the
// platform project's own BYOK anthropic credential -- the console assistant is
// not a customer of that key and must not spend it.
func TestAgentChatDefaultModel_NeverStartsOnAnthropic(t *testing.T) {
	for _, configured := range []string{"claude", "claude-haiku", " CLAUDE ", ""} {
		if got := agentChatDefaultModel(configured); got != agentChatDefaultModelFallback {
			t.Fatalf("agentChatDefaultModel(%q) = %q, want the fallback %q", configured, got, agentChatDefaultModelFallback)
		}
	}
	for _, configured := range []string{"or-gpt-41-mini", "gpt-4o", "some-future-alias"} {
		if got := agentChatDefaultModel(configured); got != configured {
			t.Fatalf("agentChatDefaultModel(%q) = %q, want it kept", configured, got)
		}
	}
	if !agentChatModelIsAnthropic("claude-haiku") {
		t.Fatal("claude-haiku must be recognised as anthropic from the routing catalog")
	}
	if agentChatModelIsAnthropic("or-gpt-41-mini") {
		t.Fatal("or-gpt-41-mini is not anthropic")
	}
}

// TestAgentChatABModel_IsStickyPerUser is the property that makes the A/B
// readable: a per-turn coin flip would swap the model in the middle of a
// conversation -- including between a write proposal and the user's
// confirmation -- and the turn rows could not be grouped into cohorts.
func TestAgentChatABModel_IsStickyPerUser(t *testing.T) {
	h := &Handler{cfg: &config.Config{AgentChatModelB: "gpt-4o", AgentChatModelBPercent: 50}}

	for _, sub := range []string{"user-a", "user-b", "user-c", "user-d"} {
		first := h.agentChatABModel(sub)
		for i := 0; i < 5; i++ {
			if got := h.agentChatABModel(sub); got != first {
				t.Fatalf("cohort of %q flipped: %q then %q", sub, first, got)
			}
		}
		if first != "" && first != "gpt-4o" {
			t.Fatalf("cohort of %q = %q, want either the default or gpt-4o", sub, first)
		}
	}

	off := &Handler{cfg: &config.Config{AgentChatModelB: "gpt-4o", AgentChatModelBPercent: 0}}
	if got := off.agentChatABModel("user-a"); got != "" {
		t.Fatalf("experiment at 0%% still routed to %q", got)
	}
	unset := &Handler{cfg: &config.Config{AgentChatModelBPercent: 100}}
	if got := unset.agentChatABModel("user-a"); got != "" {
		t.Fatalf("experiment without a B model routed to %q", got)
	}
	anthropicB := &Handler{cfg: &config.Config{AgentChatModelB: "claude", AgentChatModelBPercent: 100}}
	if got := anthropicB.agentChatABModel("user-a"); got != "" {
		t.Fatalf("experiment routed to anthropic: %q", got)
	}
	all := &Handler{cfg: &config.Config{AgentChatModelB: "gpt-4o", AgentChatModelBPercent: 100}}
	if got := all.agentChatABModel("user-a"); got != "gpt-4o" {
		t.Fatalf("experiment at 100%% = %q, want gpt-4o", got)
	}
	if got := all.agentChatABModel(""); got != "" {
		t.Fatalf("anonymous caller must stay on the default, got %q", got)
	}
}

// TestAgentChatModelFor_RequestBeatsTheExperiment keeps the eval harness able to
// drive one specific model: without this an A/B cohort would silently rewrite
// what the harness asked for and every scored run would be untrustworthy.
func TestAgentChatModelFor_RequestBeatsTheExperiment(t *testing.T) {
	h := &Handler{cfg: &config.Config{
		AgentChatModelAllowlist: []string{"or-gpt-4o-mini"},
		AgentChatModelB:         "gpt-4o",
		AgentChatModelBPercent:  100,
	}}
	if got := h.agentChatModelFor("or-gpt-4o-mini", "user-a"); got != "or-gpt-4o-mini" {
		t.Fatalf("requested model = %q, want or-gpt-4o-mini", got)
	}
	if got := h.agentChatModelFor("", "user-a"); got != "gpt-4o" {
		t.Fatalf("without a request the cohort applies, got %q", got)
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
