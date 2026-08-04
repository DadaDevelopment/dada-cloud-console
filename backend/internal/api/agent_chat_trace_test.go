package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/google/uuid"
)

func newAnsweredTrace(t *testing.T) *agentchat.TurnTrace {
	t.Helper()
	projectID := uuid.New()
	tr := agentchat.NewTurnTrace(agentchat.TurnKindTurn)
	tr.UserSub = "user-1"
	tr.OrgID = "dada"
	tr.ProjectID = &projectID
	tr.InputMessage = "как мне захостить бота в тг?"
	tr.OutputText = "поехали"
	tr.RecordTool(agentchat.ToolSpan{Name: "getProject", OK: true, DurationMs: 12, ResultLen: 40})
	tr.RecordTool(agentchat.ToolSpan{Name: "listApps", OK: true, DurationMs: 30, ResultLen: 11})
	tr.WriteCallCount = 1
	tr.SetUsage(agentchat.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Calls: 3, Model: "claude"})
	tr.Finish(agentchat.OutcomeAnswered, "")
	return tr
}

// TestAgentChatLangfuseBatch_UsesOnlyTypesTheAPIAccepts pins the failure that
// let tracing look wired while every tool call was dropped: Langfuse rejects an
// unknown observation type per event and stores the rest of the batch, so the
// trace arrived with no children and only a log line said why.
func TestAgentChatLangfuseBatch_UsesOnlyTypesTheAPIAccepts(t *testing.T) {
	accepted := map[string]bool{
		langfuse.ObservationTypeGeneration: true,
		langfuse.ObservationTypeSpan:       true,
		langfuse.ObservationTypeEvent:      true,
	}

	for _, event := range agentChatLangfuseBatch(newAnsweredTrace(t)) {
		body, ok := event.Body.(langfuse.ObservationBody)
		if !ok {
			continue
		}
		if !accepted[body.Type] {
			t.Fatalf("observation %q has type %q, which the ingestion API rejects; allowed: GENERATION, SPAN, EVENT", body.Name, body.Type)
		}
	}
}

func TestAgentChatLangfuseBatchShape(t *testing.T) {
	tr := newAnsweredTrace(t)
	batch := agentChatLangfuseBatch(tr)

	if len(batch) != 4 {
		t.Fatalf("batch length = %d, want 4 (1 trace + 1 generation + 2 tools)", len(batch))
	}

	if batch[0].Type != langfuse.EventTypeTraceCreate {
		t.Fatalf("batch[0].Type = %q, want %q", batch[0].Type, langfuse.EventTypeTraceCreate)
	}
	traceBody, ok := batch[0].Body.(langfuse.TraceBody)
	if !ok {
		t.Fatalf("batch[0].Body is %T, want langfuse.TraceBody", batch[0].Body)
	}
	if traceBody.ID != tr.TraceID.String() {
		t.Fatalf("trace id = %q, want %q", traceBody.ID, tr.TraceID.String())
	}
	if traceBody.UserID != "user-1" {
		t.Fatalf("userId = %q, want user-1", traceBody.UserID)
	}
	if traceBody.SessionID != "user-1|"+tr.ProjectID.String() {
		t.Fatalf("sessionId = %q, want user-1|<project>", traceBody.SessionID)
	}
	if traceBody.Metadata["gateway_calls"] != 3 {
		t.Fatalf("gateway_calls = %v, want 3", traceBody.Metadata["gateway_calls"])
	}

	if batch[1].Type != langfuse.EventTypeObservationCreate {
		t.Fatalf("batch[1].Type = %q, want observation-create", batch[1].Type)
	}
	gen, ok := batch[1].Body.(langfuse.ObservationBody)
	if !ok {
		t.Fatalf("batch[1].Body is %T, want langfuse.ObservationBody", batch[1].Body)
	}
	if gen.Type != langfuse.ObservationTypeGeneration {
		t.Fatalf("observation type = %q, want GENERATION", gen.Type)
	}
	if gen.Model != "claude" {
		t.Fatalf("model = %q, want claude", gen.Model)
	}
	if gen.Usage == nil || gen.Usage.TotalTokens != 15 || gen.Usage.PromptTokens != 10 || gen.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v, want 10/5/15", gen.Usage)
	}
	if gen.TraceID != tr.TraceID.String() {
		t.Fatalf("generation traceId = %q, want %q", gen.TraceID, tr.TraceID.String())
	}
	if gen.Level != langfuse.LevelDefault {
		t.Fatalf("level = %q, want DEFAULT", gen.Level)
	}

	wantNames := []string{"getProject", "listApps"}
	for i, want := range wantNames {
		tool, ok := batch[2+i].Body.(langfuse.ObservationBody)
		if !ok {
			t.Fatalf("batch[%d].Body is %T, want langfuse.ObservationBody", 2+i, batch[2+i].Body)
		}
		if tool.Type != langfuse.ObservationTypeSpan {
			t.Fatalf("batch[%d] type = %q, want SPAN", 2+i, tool.Type)
		}
		if tool.Name != want {
			t.Fatalf("batch[%d] name = %q, want %q (call order must survive)", 2+i, tool.Name, want)
		}
		if tool.ParentObservationID != gen.ID {
			t.Fatalf("batch[%d] parentObservationId = %q, want %q", 2+i, tool.ParentObservationID, gen.ID)
		}
		if tool.StartTime == "" || tool.EndTime == "" {
			t.Fatalf("batch[%d] has no time range", 2+i)
		}
	}

	ids := map[string]bool{}
	for i, ev := range batch {
		if ev.ID == "" {
			t.Fatalf("batch[%d] has an empty envelope id", i)
		}
		if ids[ev.ID] {
			t.Fatalf("batch[%d] reuses envelope id %q", i, ev.ID)
		}
		ids[ev.ID] = true
		if ev.Timestamp == "" {
			t.Fatalf("batch[%d] has an empty envelope timestamp", i)
		}
	}
}

func TestAgentChatLangfuseBatchErrorLevels(t *testing.T) {
	tr := agentchat.NewTurnTrace(agentchat.TurnKindTurn)
	tr.RecordTool(agentchat.ToolSpan{Name: "createApp", OK: false, Error: "quota exceeded", DurationMs: 5})
	tr.Finish(agentchat.OutcomeError, "upstream")

	batch := agentChatLangfuseBatch(tr)
	if len(batch) != 3 {
		t.Fatalf("batch length = %d, want 3", len(batch))
	}

	gen := batch[1].Body.(langfuse.ObservationBody)
	if gen.Level != langfuse.LevelError {
		t.Fatalf("generation level = %q, want ERROR", gen.Level)
	}
	if gen.StatusMessage != "upstream" {
		t.Fatalf("generation statusMessage = %q, want upstream", gen.StatusMessage)
	}

	tool := batch[2].Body.(langfuse.ObservationBody)
	if tool.Level != langfuse.LevelError {
		t.Fatalf("tool level = %q, want ERROR", tool.Level)
	}
	if tool.StatusMessage != "quota exceeded" {
		t.Fatalf("tool statusMessage = %q, want the tool error", tool.StatusMessage)
	}
}

func TestAgentChatLangfuseBatchPendingConfirm(t *testing.T) {
	tr := agentchat.NewTurnTrace(agentchat.TurnKindTurn)
	tr.PendingToolName = "createApp"
	tr.Finish(agentchat.OutcomePendingConfirm, "")

	batch := agentChatLangfuseBatch(tr)
	traceBody := batch[0].Body.(langfuse.TraceBody)
	if traceBody.Metadata["pending_tool_name"] != "createApp" {
		t.Fatalf("pending_tool_name = %v, want createApp", traceBody.Metadata["pending_tool_name"])
	}
	if len(traceBody.Tags) != 2 || traceBody.Tags[1] != string(agentchat.OutcomePendingConfirm) {
		t.Fatalf("tags = %v, want [turn pending_confirm]", traceBody.Tags)
	}
}

func TestAgentChatLangfuseBatchNilTrace(t *testing.T) {
	if batch := agentChatLangfuseBatch(nil); batch != nil {
		t.Fatalf("nil trace should yield a nil batch, got %d events", len(batch))
	}
}

func TestAgentChatLangfuseNilConfig(t *testing.T) {
	h := &Handler{}
	c := h.agentChatLangfuse()
	if c != nil {
		t.Fatalf("handler without config should yield a nil client, got %+v", c)
	}
	if c.Configured() {
		t.Fatal("nil client must not report as configured")
	}
	c.IngestAsync(agentChatLangfuseBatch(newAnsweredTrace(t)))
}

func TestAgentChatRecordTurnWithoutPoolOrConfig(t *testing.T) {
	h := &Handler{}
	h.agentChatRecordTurn(nil)

	tr := agentchat.NewTurnTrace(agentchat.TurnKindTurn)
	h.agentChatRecordTurn(tr)
	if tr.FinishedAt.IsZero() {
		t.Fatal("recording an unfinished trace should finish it")
	}
	if tr.Outcome != agentchat.OutcomeError || tr.ErrorCode != "unfinished" {
		t.Fatalf("unfinished trace outcome = %q/%q, want error/unfinished", tr.Outcome, tr.ErrorCode)
	}
}
