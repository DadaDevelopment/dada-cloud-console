package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestA2ASendCarriesIdentityMetadata: the runtime names the trace's user and
// session after the Telegram sender only when the identity arrives as message
// metadata; the envelope text is invisible to it.
func TestA2ASendCarriesIdentityMetadata(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{pausedOnPlaceholder, completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	run := testRun()
	run.ConversationContext = AgentConversationContext{ConversationID: "c1", Channel: "telegram", ExternalID: "-900100000423", Username: "ivan_petrov"}
	run.ActorMetadata = map[string]any{"first_name": "Иван"}
	run.Trigger = "idle"
	if _, err := newTestA2AClient(srv.URL).Send(context.Background(), run); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want 2 (turn + retry)", len(agent.requests))
	}
	want := map[string]any{
		"dada.channel": "telegram", "dada.chat_id": "-900100000423", "dada.username": "ivan_petrov",
		"dada.conversation_id": "c1", "dada.first_name": "Иван", "dada.trigger": "idle",
	}
	for i, msg := range agent.requests {
		for k, v := range want {
			if msg.Metadata[k] != v {
				t.Errorf("request %d metadata[%s] = %v, want %v", i, k, msg.Metadata[k], v)
			}
		}
	}
}

func TestA2AMetadataOmittedWhenNothingKnown(t *testing.T) {
	if got := a2aMetadata(AgentRunRequest{}); got != nil {
		t.Fatalf("metadata = %v, want nil", got)
	}
}

// TestA2ASendMutesTelemetryOverBudget: while the Langfuse budget guard reports
// exceeded, every request tells the agent to keep the turn out of Langfuse;
// once it clears, the flag disappears.
func TestA2ASendMutesTelemetryOverBudget(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{completedReply, completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	over := true
	client := newTestA2AClient(srv.URL)
	client.telemetryOff = func() bool { return over }
	if _, err := client.Send(context.Background(), testRun()); err != nil {
		t.Fatalf("send: %v", err)
	}
	over = false
	if _, err := client.Send(context.Background(), testRun()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(agent.requests))
	}
	if got := agent.requests[0].Metadata["dada.telemetry"]; got != "off" {
		t.Errorf("over budget: dada.telemetry = %v, want off", got)
	}
	if _, ok := agent.requests[1].Metadata["dada.telemetry"]; ok {
		t.Errorf("under budget: dada.telemetry still set: %v", agent.requests[1].Metadata)
	}
}
