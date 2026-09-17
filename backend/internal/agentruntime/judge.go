package agentruntime

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/langfuse"
)

// judgeObservationName is the trace and root span name the Langfuse
// evaluation rule judge/roman-turn filters on (call_center/judge).
const judgeObservationName = "judge_turn"

const judgeHistoryDepth = 6

// JudgeInput is what the LLM-as-a-judge evaluators in Langfuse see as
// {{input}}: the turn context without the delivered text, which goes to
// {{output}}. Shape is shared with call_center/judge/calibration/*.json.
type JudgeInput struct {
	Turn       string            `json:"turn"`
	History    []JudgeMsg        `json:"history"`
	Incoming   []string          `json:"incoming"`
	Flags      JudgeFlags        `json:"flags"`
	Escalation *JudgeEscal       `json:"escalation"`
	State      map[string]string `json:"state"`
}

type JudgeMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type JudgeFlags struct {
	NoQuestionThisTurn bool `json:"no_question_this_turn"`
	ReplySplit         bool `json:"reply_split"`
	SeamlessHandoff    bool `json:"seamless_handoff"`
	NarrowMode         bool `json:"narrow_mode"`
}

// JudgeEscal is the escalation the model raised during this turn. Hand-off
// codes pause the agent before the turn is saved, so only signal codes and
// narrow-mode hand-offs reach the judge.
type JudgeEscal struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

// noteEscalation records the escalation raised mid-turn so emitJudgeTurn can
// attach it to the observation. One slot per conversation, consumed by the
// turn that delivers.
func (r *Runtime) noteEscalation(convID uuid.UUID, code, summary string) {
	r.judgeMu.Lock()
	defer r.judgeMu.Unlock()
	if r.judgeEscal == nil {
		r.judgeEscal = map[uuid.UUID]JudgeEscal{}
	}
	r.judgeEscal[convID] = JudgeEscal{Code: code, Summary: summary}
}

func (r *Runtime) takeEscalation(convID uuid.UUID) *JudgeEscal {
	r.judgeMu.Lock()
	defer r.judgeMu.Unlock()
	e, ok := r.judgeEscal[convID]
	if !ok {
		return nil
	}
	delete(r.judgeEscal, convID)
	return &e
}

// judgeHistory keeps the last n non-system messages before the pending
// input, in chronological order, rendered the way the model saw them.
func judgeHistory(history, pending []Message, n int) []JudgeMsg {
	skip := map[uuid.UUID]bool{}
	for _, m := range pending {
		skip[m.ID] = true
	}
	var out []JudgeMsg
	for _, m := range history {
		if m.Role == "system" || skip[m.ID] {
			continue
		}
		out = append(out, JudgeMsg{Role: m.Role, Text: judgeText(m)})
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if out == nil {
		out = []JudgeMsg{}
	}
	return out
}

// judgeText is the message as the judge reads it: content plus typed
// attachment lines from renderAttachment, no role prefix or timestamps.
func judgeText(m Message) string {
	var sb strings.Builder
	sb.WriteString(m.Content)
	for _, a := range m.Attachments {
		if am, ok := a.(map[string]any); ok {
			sb.WriteString("\n")
			sb.WriteString(renderAttachment(am))
		}
	}
	return strings.TrimSpace(sb.String())
}

func judgeState(state RuntimeState) map[string]string {
	out := map[string]string{}
	for k, f := range state.ReportedFacts {
		out[k] = f.Value
	}
	return out
}

// buildJudgeEvents renders one trace with one root span named judge_turn.
// sessionId matches the kagent run ContextID so the verdict lands in the
// same Langfuse session as the model trace of the turn.
func buildJudgeEvents(conv Conversation, in JudgeInput, delivered []string, now time.Time) []langfuse.Event {
	ts := langfuse.FormatTime(now)
	traceID := uuid.NewString()
	metadata := map[string]any{
		"agent":           conv.AgentName,
		"escalation_code": "",
		"flags":           in.Flags,
	}
	if in.Escalation != nil {
		metadata["escalation_code"] = in.Escalation.Code
	}
	return []langfuse.Event{
		{
			ID:        uuid.NewString(),
			Type:      langfuse.EventTypeTraceCreate,
			Timestamp: ts,
			Body: langfuse.TraceBody{
				ID:        traceID,
				Timestamp: ts,
				Name:      judgeObservationName,
				UserID:    conv.Channel + ":" + conv.ExternalID,
				SessionID: "runtime-" + conv.ID.String(),
				Metadata:  metadata,
				Tags:      []string{"judge"},
			},
		},
		{
			ID:        uuid.NewString(),
			Type:      langfuse.EventTypeObservationCreate,
			Timestamp: ts,
			Body: langfuse.ObservationBody{
				ID:        uuid.NewString(),
				TraceID:   traceID,
				Type:      langfuse.ObservationTypeSpan,
				Name:      judgeObservationName,
				StartTime: ts,
				EndTime:   ts,
				Input:     in,
				Output:    delivered,
				Metadata:  metadata,
			},
		},
	}
}

// emitJudgeTurn ships the judge_turn observation for a delivered turn. No
// client configured = no-op; ingest errors are logged by the client and
// never touch the turn.
func (r *Runtime) emitJudgeTurn(conv Conversation, history, pending []Message, after RuntimeState, run AgentRunRequest, delivered []string) {
	if r.judge == nil || !r.judge.Configured() {
		return
	}
	incoming := make([]string, 0, len(pending))
	for _, m := range pending {
		incoming = append(incoming, judgeText(m))
	}
	_, narrow := narrowSince(conv)
	in := JudgeInput{
		Turn:     uuid.NewString(),
		History:  judgeHistory(history, pending, judgeHistoryDepth),
		Incoming: incoming,
		Flags: JudgeFlags{
			NoQuestionThisTurn: run.ConversationContext.NoQuestionThisTurn,
			ReplySplit:         run.ConversationContext.ReplySplit,
			SeamlessHandoff:    run.ConversationContext.SeamlessHandoff,
			NarrowMode:         narrow,
		},
		Escalation: r.takeEscalation(conv.ID),
		State:      judgeState(after),
	}
	r.judge.IngestAsync(buildJudgeEvents(conv, in, delivered, time.Now()))
}
