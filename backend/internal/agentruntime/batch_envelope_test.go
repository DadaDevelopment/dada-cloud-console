package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvelopeGluesWholeBatchIntoIncomingText(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	sent := now.Add(-20 * time.Second)
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	run := AgentRunRequest{Messages: []Message{
		{ID: first, Role: "user", Content: "опыт есть", SourceSentAt: &sent},
		{ID: second, Role: "user", Content: "цель 500", SourceSentAt: &sent},
		{ID: third, Role: "user", Content: "вот скрин", SourceSentAt: &sent,
			Attachments: []any{map[string]any{"kind": "image", "description": "кошелёк FxPro, баланс 526 USD"}}},
	}}
	rendered := renderAgentRunAt(run, now)
	body := rendered[strings.Index(rendered, "\n")+1:]
	var envelope struct {
		Count    int       `json:"incoming_count"`
		Text     string    `json:"incoming_text"`
		Messages []Message `json:"incoming_messages"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope.Count != 3 || len(envelope.Messages) != 3 {
		t.Fatalf("count = %d, messages = %d", envelope.Count, len(envelope.Messages))
	}
	for _, want := range []string{"опыт есть", "цель 500", "вот скрин", "[image]: кошелёк FxPro, баланс 526 USD"} {
		if !strings.Contains(envelope.Text, want) {
			t.Fatalf("incoming_text lacks %q: %q", want, envelope.Text)
		}
	}
	if strings.Index(envelope.Text, "опыт есть") > strings.Index(envelope.Text, "цель 500") ||
		strings.Index(envelope.Text, "цель 500") > strings.Index(envelope.Text, "вот скрин") {
		t.Fatalf("batch order lost: %q", envelope.Text)
	}
	if strings.Contains(envelope.Text, "user: ") {
		t.Fatalf("role prefix leaked into incoming_text: %q", envelope.Text)
	}
	if !strings.Contains(rendered, "reply must cover all of them") {
		t.Fatalf("preface does not tell the model the batch is whole: %q", rendered[:200])
	}
}

func TestEnvelopeSingleMessageTextEqualsContent(t *testing.T) {
	now := time.Now()
	rendered := renderAgentRunAt(AgentRunRequest{Messages: []Message{{Role: "user", Content: "привет"}}}, now)
	body := rendered[strings.Index(rendered, "\n")+1:]
	var envelope struct {
		Count int    `json:"incoming_count"`
		Text  string `json:"incoming_text"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope.Count != 1 || envelope.Text != "привет" {
		t.Fatalf("count = %d, text = %q", envelope.Count, envelope.Text)
	}
}
