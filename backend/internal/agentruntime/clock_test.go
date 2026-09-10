package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func withRuntimeLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	SetRuntimeLocation(loc)
	t.Cleanup(func() { SetRuntimeLocation(nil) })
	return loc
}

func TestRenderAgentRunClockInRuntimeZone(t *testing.T) {
	withRuntimeLocation(t, "Europe/Moscow")
	sent := time.Date(2026, 9, 10, 21, 5, 0, 0, time.UTC)
	created := sent.Add(2 * time.Second)
	now := sent.Add(90 * time.Second)
	run := AgentRunRequest{
		ConversationContext: AgentConversationContext{ConversationID: "c1", Channel: "telegram"},
		Messages:            []Message{{Role: "user", Content: "hi", SourceSentAt: &sent, CreatedAt: created}},
	}
	rendered := renderAgentRunAt(run, now)
	body := rendered[strings.Index(rendered, "{"):]
	var envelope struct {
		Context  AgentConversationContext `json:"runtime_context"`
		Messages []Message                `json:"incoming_messages"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("envelope must stay valid JSON: %v\n%s", err, body)
	}
	if envelope.Context.Now != "2026-09-11T00:06:30+03:00" {
		t.Fatalf("now must be rendered in MSK, got %q", envelope.Context.Now)
	}
	if envelope.Context.TimeZone != "Europe/Moscow" {
		t.Fatalf("time_zone must name the runtime zone, got %q", envelope.Context.TimeZone)
	}
	if !strings.Contains(body, `"source_sent_at":"2026-09-11T00:05:00+03:00"`) {
		t.Fatalf("source_sent_at must carry the MSK offset, got:\n%s", body)
	}
	if !strings.Contains(body, `"created_at":"2026-09-11T00:05:02+03:00"`) {
		t.Fatalf("created_at must carry the MSK offset, got:\n%s", body)
	}
	if !run.Messages[0].SourceSentAt.Equal(sent) || run.Messages[0].SourceSentAt.Location() != time.UTC {
		t.Fatalf("stored message must not be mutated by rendering")
	}
}

func TestRenderAgentRunDefaultsToUTC(t *testing.T) {
	SetRuntimeLocation(nil)
	now := time.Date(2026, 9, 10, 21, 0, 0, 0, time.FixedZone("X", 3*3600))
	rendered := renderAgentRunAt(AgentRunRequest{}, now)
	if !strings.Contains(rendered, `"now":"2026-09-10T18:00:00Z"`) {
		t.Fatalf("default zone must be UTC, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `"time_zone":"UTC"`) {
		t.Fatalf("default time_zone must be UTC, got:\n%s", rendered)
	}
}

func TestRenderMessageSentTimeInRuntimeZone(t *testing.T) {
	withRuntimeLocation(t, "Europe/Moscow")
	sent := time.Date(2026, 9, 10, 21, 5, 0, 0, time.UTC)
	got := renderMessage(Message{Role: "user", Content: "hi", SourceSentAt: &sent}, sent.Add(3*time.Minute))
	if !strings.HasPrefix(got, "user [sent 00:05 MSK, 3m ago]: hi\n") {
		t.Fatalf("history line must show MSK wall clock, got %q", got)
	}
}

func TestLocationFromEnv(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_TZ", "")
	if loc, err := LocationFromEnv(); err != nil || loc != time.UTC {
		t.Fatalf("empty env must mean UTC, got %v %v", loc, err)
	}
	t.Setenv("AGENT_RUNTIME_TZ", " Europe/Moscow ")
	if loc, err := LocationFromEnv(); err != nil || loc.String() != "Europe/Moscow" {
		t.Fatalf("trimmed IANA name must load, got %v %v", loc, err)
	}
	t.Setenv("AGENT_RUNTIME_TZ", "Mars/Olympus")
	if _, err := LocationFromEnv(); err == nil {
		t.Fatalf("unknown zone must fail startup")
	}
}
