package agentruntime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/langfuse"
)

func TestJudgeHistoryDropsSystemAndPendingKeepsTail(t *testing.T) {
	pending := []Message{{ID: uuid.New(), Role: "user", Content: "ты бот?"}}
	var history []Message
	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, Message{ID: uuid.New(), Role: role, Content: strings.Repeat("m", i+1)})
	}
	history = append(history, Message{ID: uuid.New(), Role: "system", Content: "skill loaded"})
	history = append(history, pending[0])

	got := judgeHistory(history, pending, 6)
	if len(got) != 6 {
		t.Fatalf("want 6 messages, got %d: %+v", len(got), got)
	}
	if got[0].Text != "mmm" || got[5].Text != "mmmmmmmm" {
		t.Fatalf("wrong tail: %+v", got)
	}
	for _, m := range got {
		if m.Role == "system" || m.Text == "ты бот?" {
			t.Fatalf("system or pending leaked into history: %+v", got)
		}
	}
	if empty := judgeHistory(nil, pending, 6); empty == nil || len(empty) != 0 {
		t.Fatalf("empty history must be [] not null: %#v", empty)
	}
}

func TestJudgeTextRendersAttachments(t *testing.T) {
	m := Message{Role: "user", Content: "вот", Attachments: []any{map[string]any{"kind": "voice", "duration_seconds": 7.0, "transcript": "салам"}}}
	got := judgeText(m)
	if got != "вот\n[voice 7s]: \"салам\"" {
		t.Fatalf("unexpected text %q", got)
	}
}

func TestBuildJudgeEventsShape(t *testing.T) {
	conv := Conversation{ID: uuid.New(), AgentName: "tg-exchange-support", Channel: "telegram", ExternalID: "42"}
	in := JudgeInput{Turn: "t1", History: []JudgeMsg{}, Incoming: []string{"салам"}, Escalation: &JudgeEscal{Code: "E_LOST_MONEY", Summary: "s"}, State: map[string]string{}}
	events := buildJudgeEvents(conv, in, []string{"И вам салам"}, time.Unix(0, 0))
	if len(events) != 2 {
		t.Fatalf("want trace+span, got %d", len(events))
	}
	trace := events[0].Body.(langfuse.TraceBody)
	span := events[1].Body.(langfuse.ObservationBody)
	if trace.Name != "judge_turn" || span.Name != "judge_turn" || span.Type != langfuse.ObservationTypeSpan {
		t.Fatalf("names/type: %+v %+v", trace, span)
	}
	if trace.SessionID != "runtime-"+conv.ID.String() || trace.UserID != "telegram:42" {
		t.Fatalf("session/user: %+v", trace)
	}
	if span.TraceID != trace.ID || span.ParentObservationID != "" {
		t.Fatalf("span must be root of its trace: %+v", span)
	}
	if span.Metadata["escalation_code"] != "E_LOST_MONEY" {
		t.Fatalf("escalation_code missing: %+v", span.Metadata)
	}
	if out, ok := span.Output.([]string); !ok || out[0] != "И вам салам" {
		t.Fatalf("output must be delivered parts: %#v", span.Output)
	}
}

func TestEmitJudgeTurnShipsAndConsumesEscalation(t *testing.T) {
	bodies := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies <- string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &Runtime{judge: langfuse.New(srv.URL, "pk", "sk", true)}
	conv := Conversation{ID: uuid.New(), AgentName: "tg-exchange-support", Channel: "telegram", ExternalID: "42"}
	r.noteEscalation(conv.ID, "E_LOST_MONEY", "слил по сигналам")
	pending := []Message{{ID: uuid.New(), Role: "user", Content: "слил 1500"}}
	after := RuntimeState{ReportedFacts: map[string]ReportedFact{"experience": {Value: "lost"}}}
	run := AgentRunRequest{ConversationContext: AgentConversationContext{NoQuestionThisTurn: true}}

	r.emitJudgeTurn(conv, pending, pending, after, run, []string{"Понимаю", "Сколько свободно?"})

	select {
	case body := <-bodies:
		for _, want := range []string{"judge_turn", "E_LOST_MONEY", "Сколько свободно?", `\"no_question_this_turn\":true`, `\"experience\":\"lost\"`, "runtime-" + conv.ID.String()} {
			if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, `\"`, `"`)) {
				t.Fatalf("payload lacks %q:\n%s", want, body)
			}
		}
		var js any
		if err := json.Unmarshal([]byte(body), &js); err != nil {
			t.Fatalf("payload not json: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("judge_turn never shipped")
	}
	if r.takeEscalation(conv.ID) != nil {
		t.Fatal("escalation must be consumed by the turn that delivered")
	}
}

func TestEmitJudgeTurnNoClientIsNoop(t *testing.T) {
	r := &Runtime{}
	r.emitJudgeTurn(Conversation{ID: uuid.New()}, nil, nil, RuntimeState{}, AgentRunRequest{}, nil)
	r.judge = langfuse.New("", "", "", true)
	r.emitJudgeTurn(Conversation{ID: uuid.New()}, nil, nil, RuntimeState{}, AgentRunRequest{}, nil)
}
