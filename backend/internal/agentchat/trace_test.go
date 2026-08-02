package agentchat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestNewTurnTraceInitialises(t *testing.T) {
	tr := NewTurnTrace(TurnKindConfirm)
	if tr.TraceID == uuid.Nil {
		t.Fatal("expected a minted trace id")
	}
	if tr.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to be set")
	}
	if tr.Kind != TurnKindConfirm {
		t.Fatalf("kind = %q, want %q", tr.Kind, TurnKindConfirm)
	}
	if tr.ToolCallCount() != 0 {
		t.Fatalf("tool call count = %d, want 0", tr.ToolCallCount())
	}
}

func TestAbsorbToolLogIsIncremental(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	log := []ToolLogEntry{{Name: "getProject", Result: "{}"}}
	tr.AbsorbToolLog(log)
	if got := tr.ToolCallCount(); got != 1 {
		t.Fatalf("after first absorb: %d spans, want 1", got)
	}

	log = append(log, ToolLogEntry{Name: "listApps", Result: `{"apps":[]}`})
	tr.AbsorbToolLog(log)
	if got := tr.ToolCallCount(); got != 2 {
		t.Fatalf("after second absorb: %d spans, want 2 (confirm path must not duplicate)", got)
	}
	if tr.ToolSpans[0].Name != "getProject" || tr.ToolSpans[1].Name != "listApps" {
		t.Fatalf("call order lost: %+v", tr.ToolSpans)
	}
}

func TestAbsorbToolLogHandlesShrunkLog(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "d"}})
	if got := tr.ToolCallCount(); got != 4 {
		t.Fatalf("spans = %d, want 4", got)
	}
}

func TestToolSpanFromLogMarksErrors(t *testing.T) {
	result := strings.Repeat("x", MaxToolErrorLen+500)
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "createApp", Result: result, IsError: true}})

	span := tr.ToolSpans[0]
	if span.OK {
		t.Fatal("expected OK=false for an errored tool")
	}
	if span.Error == "" {
		t.Fatal("expected the error text to be captured")
	}
	if len(span.Error) > MaxToolErrorLen+len("... [truncated]") {
		t.Fatalf("error not truncated: len=%d", len(span.Error))
	}
	if span.ResultLen != len(result) {
		t.Fatalf("result_len = %d, want %d", span.ResultLen, len(result))
	}
}

func TestAbsorbInventoryCountsApps(t *testing.T) {
	tests := []struct {
		name       string
		entry      ToolLogEntry
		wantApps   *int
		wantProjs  *int
		checkApps  bool
		checkProjs bool
	}{
		{
			name:      "empty apps array is the production footgun",
			entry:     ToolLogEntry{Name: "listApps", Result: `{"apps":[]}`},
			wantApps:  intPtr(0),
			checkApps: true,
		},
		{
			name:      "two apps",
			entry:     ToolLogEntry{Name: "listApps", Result: `{"apps":[{"name":"a"},{"name":"b"}]}`},
			wantApps:  intPtr(2),
			checkApps: true,
		},
		{
			name:      "bare array result",
			entry:     ToolLogEntry{Name: "listApps", Result: `[{"name":"a"}]`},
			wantApps:  intPtr(1),
			checkApps: true,
		},
		{
			name:       "projects",
			entry:      ToolLogEntry{Name: "listProjects", Result: `{"projects":[{"id":"p"}]}`},
			wantProjs:  intPtr(1),
			checkProjs: true,
		},
		{
			name:       "truncated json leaves inventory unknown",
			entry:      ToolLogEntry{Name: "listApps", Result: `{"apps":[{"name":`},
			checkApps:  true,
			checkProjs: true,
		},
		{
			name:       "errored listApps is not inventory evidence",
			entry:      ToolLogEntry{Name: "listApps", Result: `{"apps":[]}`, IsError: true},
			checkApps:  true,
			checkProjs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTurnTrace(TurnKindTurn)
			tr.AbsorbToolLog([]ToolLogEntry{tc.entry})
			if tc.checkApps && !samePtr(tr.InventoryApps, tc.wantApps) {
				t.Fatalf("InventoryApps = %v, want %v", derefPtr(tr.InventoryApps), derefPtr(tc.wantApps))
			}
			if tc.checkProjs && !samePtr(tr.InventoryProjects, tc.wantProjs) {
				t.Fatalf("InventoryProjects = %v, want %v", derefPtr(tr.InventoryProjects), derefPtr(tc.wantProjs))
			}
		})
	}
}

func TestAbsorbResultUsesEngineInventory(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbResult(TurnResult{
		AssistantText:         "у тебя пока ничего не развёрнуто",
		ToolLog:               []ToolLogEntry{{Name: "listProjects", Preflight: true, DurationMs: 8}, {Name: "listApps", Preflight: true, DurationMs: 11}},
		Usage:                 Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, Calls: 2, Model: "claude"},
		WriteCallCount:        0,
		InventoryProjects:     1,
		InventoryApps:         0,
		InventoryAppsLookedUp: true,
		PreflightCalls:        2,
	})

	if tr.InventoryApps == nil || *tr.InventoryApps != 0 {
		t.Fatalf("InventoryApps = %v, want 0 (looked up and found nothing)", derefPtr(tr.InventoryApps))
	}
	if tr.InventoryProjects == nil || *tr.InventoryProjects != 1 {
		t.Fatalf("InventoryProjects = %v, want 1", derefPtr(tr.InventoryProjects))
	}
	if tr.PreflightCalls != 2 {
		t.Fatalf("PreflightCalls = %d, want 2", tr.PreflightCalls)
	}
	if tr.Usage.TotalTokens != 120 || tr.Usage.Model != "claude" {
		t.Fatalf("usage not absorbed: %+v", tr.Usage)
	}
	if tr.OutputText == "" {
		t.Fatal("assistant text not absorbed")
	}
	if tr.ToolCallCount() != 2 {
		t.Fatalf("spans = %d, want 2", tr.ToolCallCount())
	}
	if !tr.ToolSpans[0].Preflight {
		t.Fatal("preflight flag lost on the span")
	}
	if tr.ToolSpans[1].DurationMs != 11 {
		t.Fatalf("duration = %d, want 11", tr.ToolSpans[1].DurationMs)
	}
}

func TestAbsorbResultLeavesInventoryUnknownWhenNotLookedUp(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbResult(TurnResult{Usage: Usage{Calls: 1}})

	if tr.InventoryApps != nil {
		t.Fatalf("InventoryApps = %v, want nil when listApps never ran", derefPtr(tr.InventoryApps))
	}
	if tr.InventoryProjects != nil {
		t.Fatalf("InventoryProjects = %v, want nil", derefPtr(tr.InventoryProjects))
	}
}

func TestAbsorbResultRedactsToolArgs(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbResult(TurnResult{
		ToolLog: []ToolLogEntry{{Name: "setEnvVar", ArgsJSON: `{"appName":"bot","key":"TOKEN","value":"123:AAH"}`}},
	})

	args := tr.ToolSpans[0].Args
	if args["appName"] != "bot" || args["key"] != "TOKEN" {
		t.Fatalf("non-secret args lost: %v", args)
	}
	if args["value"] != "[redacted]" {
		t.Fatalf("bot token leaked into the trace: %v", args["value"])
	}
}

func TestToolCallsJSONNeverNull(t *testing.T) {
	empty := NewTurnTrace(TurnKindTurn)
	if got := string(empty.ToolCallsJSON()); got != "[]" {
		t.Fatalf("empty trace tool_calls = %q, want []", got)
	}

	tr := NewTurnTrace(TurnKindTurn)
	tr.RecordTool(ToolSpan{Name: "listApps", OK: true, DurationMs: 12, ResultLen: 9})
	var decoded []map[string]any
	if err := json.Unmarshal(tr.ToolCallsJSON(), &decoded); err != nil {
		t.Fatalf("tool_calls is not a JSON array: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("decoded %d spans, want 1", len(decoded))
	}
	for _, key := range []string{"name", "ok", "duration_ms", "result_len"} {
		if _, ok := decoded[0][key]; !ok {
			t.Fatalf("span is missing %q: %v", key, decoded[0])
		}
	}
}

func TestRedactArgsHidesSecrets(t *testing.T) {
	args := RedactArgs(`{"appName":"a","key":"K","value":"s3cr3t","TOKEN":"t"}`)
	if args["appName"] != "a" {
		t.Fatalf("appName lost: %v", args["appName"])
	}
	if args["key"] != "K" {
		t.Fatalf("key should not be redacted (it is an env var name): %v", args["key"])
	}
	if args["value"] != "[redacted]" {
		t.Fatalf("value not redacted: %v", args["value"])
	}
	if args["TOKEN"] != "[redacted]" {
		t.Fatalf("TOKEN not redacted (case-insensitive match failed): %v", args["TOKEN"])
	}

	if RedactArgs("") != nil {
		t.Fatal("empty args should yield nil")
	}
	if RedactArgs(`{"broken":`) != nil {
		t.Fatal("malformed args should yield nil, never stored verbatim")
	}
}

func TestFinishIsIdempotent(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	tr.Finish(OutcomeAnswered, "")
	first := tr.FinishedAt
	tr.Finish(OutcomeError, "upstream")

	if tr.Outcome != OutcomeAnswered {
		t.Fatalf("outcome overwritten: %q", tr.Outcome)
	}
	if tr.ErrorCode != "" {
		t.Fatalf("error code overwritten: %q", tr.ErrorCode)
	}
	if !tr.FinishedAt.Equal(first) {
		t.Fatal("FinishedAt overwritten by second Finish")
	}
	if tr.LatencyMs() < 0 {
		t.Fatalf("latency = %d, want >= 0", tr.LatencyMs())
	}
}

func TestLatencyMeasuredToNowWhenUnfinished(t *testing.T) {
	tr := NewTurnTrace(TurnKindTurn)
	if tr.LatencyMs() < 0 {
		t.Fatalf("latency = %d, want >= 0", tr.LatencyMs())
	}
}

func TestNilTraceIsSafe(t *testing.T) {
	var tr *TurnTrace
	tr.RecordTool(ToolSpan{Name: "x"})
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "y"}})
	tr.SetUsage(Usage{TotalTokens: 1})
	tr.Finish(OutcomeAnswered, "")
	if tr.LatencyMs() != 0 || tr.ToolCallCount() != 0 {
		t.Fatal("nil trace should read as zero")
	}
	if string(tr.ToolCallsJSON()) != "[]" {
		t.Fatal("nil trace tool_calls should be []")
	}
}

func TestTraceIDContextRoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := WithTraceID(context.Background(), id)
	if got := TraceIDFrom(ctx); got != id.String() {
		t.Fatalf("TraceIDFrom = %q, want %q", got, id.String())
	}
	if got := TraceIDFrom(context.Background()); got != "" {
		t.Fatalf("bare context should carry no trace id, got %q", got)
	}
}

func TestRedactArgsRedactsNestedBulkSetEnvVars(t *testing.T) {
	const raw = `{"projectId":"p","envId":"e","appName":"bot","vars":[{"key":"TELEGRAM_BOT_TOKEN","value":"7712345:AAF-SUPER-SECRET"},{"key":"DATABASE_URL","value":"postgres://u:p4ss@db:5432/x"}]}`

	args := RedactArgs(raw)
	blob, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal redacted args: %v", err)
	}
	for _, secret := range []string{"AAF-SUPER-SECRET", "p4ss"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, blob)
		}
	}

	vars, ok := args["vars"].([]any)
	if !ok || len(vars) != 2 {
		t.Fatalf("vars array lost: %v", args["vars"])
	}
	first, ok := vars[0].(map[string]any)
	if !ok {
		t.Fatalf("vars[0] is not an object: %v", vars[0])
	}
	if first["key"] != "TELEGRAM_BOT_TOKEN" {
		t.Fatalf("env var name must survive: %v", first["key"])
	}
	if first["value"] != RedactedMarker {
		t.Fatalf("vars[0].value not redacted: %v", first["value"])
	}
	if args["appName"] != "bot" {
		t.Fatalf("non-secret arg lost: %v", args["appName"])
	}
}

func TestRedactArgsRedactsAtAnyDepth(t *testing.T) {
	args := RedactArgs(`{"a":{"b":[{"c":{"token":"deep-secret","name":"keep"}}]},"list":[["x",{"password":"pw"}]]}`)
	blob, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"deep-secret", "pw"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("secret %q survived at depth: %s", secret, blob)
		}
	}
	if !strings.Contains(string(blob), "keep") {
		t.Fatalf("non-secret sibling dropped: %s", blob)
	}
}

func TestRedactArgsRedactsKeyValuePairShape(t *testing.T) {
	args := RedactArgs(`{"vars":[{"key":"K","value":{"nested":"still-secret"}}]}`)
	blob, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "still-secret") {
		t.Fatalf("structured env var value leaked: %s", blob)
	}
}

func TestRedactArgsJSONRoundTrip(t *testing.T) {
	got := RedactArgsJSON(`{"appName":"bot","vars":[{"key":"T","value":"7712345:AAF-SUPER-SECRET"}]}`)
	if strings.Contains(got, "AAF-SUPER-SECRET") {
		t.Fatalf("secret survived RedactArgsJSON: %s", got)
	}
	if !strings.Contains(got, RedactedMarker) {
		t.Fatalf("no redaction marker in %s", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("RedactArgsJSON must return valid JSON, got %q: %v", got, err)
	}
	if decoded["appName"] != "bot" {
		t.Fatalf("non-secret arg lost: %v", decoded["appName"])
	}

	if got := RedactArgsJSON(""); got != "" {
		t.Fatalf("blank input should stay blank, got %q", got)
	}
	if got := RedactArgsJSON(`{"secret":`); got != "{}" {
		t.Fatalf("malformed args must be dropped, got %q", got)
	}
	if got := RedactArgsJSON(`[{"token":"t"}]`); strings.Contains(got, `"t"`) {
		t.Fatalf("top-level array not redacted: %s", got)
	}
}

func TestRedactValueWalksAnyShape(t *testing.T) {
	out := RedactValue([]any{map[string]any{"api_key": "k", "keep": "v"}})
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), `"k"`) {
		t.Fatalf("api_key leaked: %s", blob)
	}
	if !strings.Contains(string(blob), "keep") {
		t.Fatalf("non-secret dropped: %s", blob)
	}
	if RedactValue("plain") != "plain" {
		t.Fatal("scalar must pass through untouched")
	}
}

func TestTruncateForTrace(t *testing.T) {
	if got := TruncateForTrace("short", 100); got != "short" {
		t.Fatalf("short string mangled: %q", got)
	}
	got := TruncateForTrace(strings.Repeat("a", 50), 10)
	if !strings.HasSuffix(got, "... [truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestTruncateForTraceKeepsUTF8Valid(t *testing.T) {
	s := strings.Repeat("привет мир, вот подробный ответ агента. ", 300)

	if got := TruncateForTrace(s, MaxTraceTextLen); !utf8.ValidString(got) {
		t.Fatalf("MaxTraceTextLen cut produced invalid UTF-8 (Postgres 22021): %q", got[len(got)-20:])
	}

	for max := 1; max < 200; max++ {
		got := TruncateForTrace(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("cut at %d produced invalid UTF-8: %q", max, got)
		}
		body := strings.TrimSuffix(got, "... [truncated]")
		if len(body) > max {
			t.Fatalf("cut at %d exceeded the byte budget: %d bytes", max, len(body))
		}
	}
}

func TestTruncateForTraceCutsWholeRunes(t *testing.T) {
	got := TruncateForTrace("привет", 3)
	if got != "п... [truncated]" {
		t.Fatalf("expected a whole-rune cut, got %q", got)
	}
	if got := TruncateForTrace("привет", 1); got != "... [truncated]" {
		t.Fatalf("a budget smaller than one rune must yield no partial rune, got %q", got)
	}
}

func TestRuneSafeCut(t *testing.T) {
	if got := RuneSafeCut("привет", 5); got != "пр" {
		t.Fatalf("RuneSafeCut = %q, want %q", got, "пр")
	}
	if got := RuneSafeCut("abc", 10); got != "abc" {
		t.Fatalf("short string mangled: %q", got)
	}
	if got := RuneSafeCut("abc", 0); got != "" {
		t.Fatalf("zero budget = %q, want empty", got)
	}
	if got := RuneSafeCut("abc", -1); got != "" {
		t.Fatalf("negative budget = %q, want empty", got)
	}
}

func intPtr(v int) *int { return &v }

func derefPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func samePtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// TestToolSpanErrorIsRedacted pins the failure path of a tool whose result
// carries a credential. A presigned download that fails downstream still
// returns the signed URL in its error body, and span.Error is written verbatim
// into agent_chat_turns.tool_calls and shipped to Langfuse -- the redaction
// that protects the success path has to cover the error path too.
func TestToolSpanErrorIsRedacted(t *testing.T) {
	raw := `{"error":"upstream 500","url":"https://s3.dada/backups/dump.sql.gz?X-Amz-Signature=deadbeefcafe"}`
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "downloadDatabaseBackup", Result: raw, IsError: true}})

	span := tr.ToolSpans[0]
	if strings.Contains(span.Error, "X-Amz-Signature") {
		t.Fatalf("presigned signature survived into span.Error: %q", span.Error)
	}
	if !strings.Contains(span.Error, redactedQueryMarker) {
		t.Fatalf("expected the redaction marker in span.Error, got %q", span.Error)
	}
	if !strings.Contains(string(tr.ToolCallsJSON()), redactedQueryMarker) {
		t.Fatalf("persisted tool_calls kept the raw error: %s", tr.ToolCallsJSON())
	}
}

// TestToolSpanErrorDropsMintedSecret is the same guard for a tool that mints a
// permanent credential rather than a signed URL.
func TestToolSpanErrorDropsMintedSecret(t *testing.T) {
	raw := `{"token":"dh_live_9f3c1a","error":"partially applied"}`
	tr := NewTurnTrace(TurnKindTurn)
	tr.AbsorbToolLog([]ToolLogEntry{{Name: "createDeployHook", Result: raw, IsError: true}})

	if strings.Contains(tr.ToolSpans[0].Error, "dh_live_9f3c1a") {
		t.Fatalf("minted token survived into span.Error: %q", tr.ToolSpans[0].Error)
	}
}

// TestEnsureModel_FillsOnlyWhatTheGatewayNeverReported pins that a turn which
// died before the gateway answered still records which model it was sent to,
// and that a model the gateway did report is never overwritten.
func TestEnsureModel_FillsOnlyWhatTheGatewayNeverReported(t *testing.T) {
	failed := NewTurnTrace(TurnKindTurn)
	failed.EnsureModel("claude")
	if failed.Usage.Model != "claude" {
		t.Fatalf("failed turn model = %q, want claude", failed.Usage.Model)
	}

	answered := NewTurnTrace(TurnKindTurn)
	answered.SetUsage(Usage{Model: "claude-haiku"})
	answered.EnsureModel("claude")
	if answered.Usage.Model != "claude-haiku" {
		t.Fatalf("reported model was overwritten: %q", answered.Usage.Model)
	}

	var nilTrace *TurnTrace
	nilTrace.EnsureModel("claude")
}
