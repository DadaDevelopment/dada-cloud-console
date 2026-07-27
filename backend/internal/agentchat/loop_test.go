package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/llmchat"
	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type scriptedToolCall struct {
	ID   string
	Name string
	Args string
}

type scriptedTurn struct {
	Content          string
	ToolCalls        []scriptedToolCall
	FinishReason     string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// newScriptedGatewayServer serves a canned sequence of OpenAI-style SSE
// streaming completions, one per call to /v1/chat/completions, so loop.go's
// round loop can be exercised without a real LLM gateway. Calling past the
// end of the script fails the test loudly instead of hanging.
func newScriptedGatewayServer(t *testing.T, turns []scriptedTurn) *httptest.Server {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(turns) {
			t.Fatalf("gateway called more times (%d) than the script has turns (%d)", call+1, len(turns))
		}
		turn := turns[call]
		call++

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		if turn.Content != "" {
			writeSSEChunk(w, map[string]any{
				"choices": []map[string]any{{
					"delta": map[string]any{"content": turn.Content},
				}},
			})
			flusher.Flush()
		}
		for i, tc := range turn.ToolCalls {
			writeSSEChunk(w, map[string]any{
				"choices": []map[string]any{{
					"delta": map[string]any{
						"tool_calls": []map[string]any{{
							"index": i,
							"id":    tc.ID,
							"type":  "function",
							"function": map[string]any{
								"name":      tc.Name,
								"arguments": tc.Args,
							},
						}},
					},
				}},
			})
			flusher.Flush()
		}
		finish := turn.FinishReason
		if finish == "" {
			if len(turn.ToolCalls) > 0 {
				finish = "tool_calls"
			} else {
				finish = "stop"
			}
		}
		writeSSEChunk(w, map[string]any{
			"choices": []map[string]any{{"finish_reason": finish}},
		})
		if turn.TotalTokens > 0 {
			writeSSEChunk(w, map[string]any{
				"model":   turn.Model,
				"choices": []map[string]any{},
				"usage": map[string]any{
					"prompt_tokens":     turn.PromptTokens,
					"completion_tokens": turn.CompletionTokens,
					"total_tokens":      turn.TotalTokens,
				},
			})
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeSSEChunk(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func newTestClient(t *testing.T, turns []scriptedTurn) *llmchat.Client {
	srv := newScriptedGatewayServer(t, turns)
	return llmchat.New(srv.URL, "test-key", "test-model")
}

func fakeHandler(result string, isError bool) internalmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: result}},
			IsError: isError,
		}, nil
	}
}

// newFakeToolset builds a Toolset directly (bypassing BuildToolset/spec
// parsing) so loop tests can control exactly which tool names are
// classified as read vs write, and what executing them returns, without a
// running backend to self-proxy against.
func newFakeToolset(readNames, writeNames []string) *Toolset {
	ts := &Toolset{handlers: map[string]internalmcp.ToolHandler{}, writeSet: map[string]bool{}}
	for _, name := range readNames {
		ts.Defs = append(ts.Defs, llmchat.ToolDef{Type: "function", Function: llmchat.ToolFunctionDef{Name: name}})
		ts.handlers[name] = fakeHandler("ok: "+name, false)
	}
	for _, name := range writeNames {
		ts.Defs = append(ts.Defs, llmchat.ToolDef{Type: "function", Function: llmchat.ToolFunctionDef{Name: name}})
		ts.handlers[name] = fakeHandler("executed: "+name, false)
		ts.writeSet[name] = true
	}
	return ts
}

func TestRunTurn_FirstWriteToolCall_StopsWithoutExecuting(t *testing.T) {
	ts := newFakeToolset(nil, []string{"restartApp"})
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "restartApp", Args: `{"appName":"web"}`}}},
	})

	assistantText, toolLog, pending, _, err := RunTurn(context.Background(), llm, ts, "Bearer test", "test-user", "system", nil, "restart web please", Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if assistantText != "" {
		t.Fatalf("assistantText=%q, want empty (turn should have interrupted)", assistantText)
	}
	if len(toolLog) != 0 {
		t.Fatalf("toolLog=%v, want empty — the write tool must not have executed", toolLog)
	}
	if pending == nil {
		t.Fatal("pending is nil, want a PendingWrite for the interrupted restartApp call")
	}
	if pending.ToolName != "restartApp" {
		t.Fatalf("pending.ToolName=%q want restartApp", pending.ToolName)
	}
	if pending.ToolCallID != "call_1" {
		t.Fatalf("pending.ToolCallID=%q want call_1", pending.ToolCallID)
	}
	if pending.ArgsJSON != `{"appName":"web"}` {
		t.Fatalf("pending.ArgsJSON=%q want {\"appName\":\"web\"}", pending.ArgsJSON)
	}
	if len(pending.Messages) == 0 {
		t.Fatal("pending.Messages snapshot is empty")
	}
	last := pending.Messages[len(pending.Messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("last snapshotted message role=%q want assistant (the message that requested the write call)", last.Role)
	}
	if pending.WriteCallCount != 0 {
		t.Fatalf("pending.WriteCallCount=%d want 0 (no write has executed yet)", pending.WriteCallCount)
	}
}

func TestRunTurn_ReadOnlyToolCalls_RunToCompletion(t *testing.T) {
	ts := newFakeToolset([]string{"listApps"}, nil)
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "listApps", Args: `{}`}}},
		{Content: "you have 2 apps"},
	})

	assistantText, toolLog, pending, _, err := RunTurn(context.Background(), llm, ts, "Bearer test", "test-user", "system", nil, "list my apps", Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if pending != nil {
		t.Fatalf("pending=%+v, want nil — an all-read turn must not interrupt", pending)
	}
	if assistantText != "you have 2 apps" {
		t.Fatalf("assistantText=%q want %q", assistantText, "you have 2 apps")
	}
	if len(toolLog) != 1 || toolLog[0].Name != "listApps" {
		t.Fatalf("toolLog=%v, want one listApps entry", toolLog)
	}
}

func TestRunTurn_WriteCallBudget_CappedAtThreeAcrossResumes(t *testing.T) {
	ts := newFakeToolset(nil, []string{"restartApp"})

	llm1 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "restartApp", Args: `{}`}}},
	})
	_, _, pending1, _, err := RunTurn(context.Background(), llm1, ts, "Bearer test", "test-user", "system", nil, "restart", Emitter{})
	if err != nil || pending1 == nil {
		t.Fatalf("round 1: pending=%+v err=%v", pending1, err)
	}

	messages := append([]llmchat.Message{}, pending1.Messages...)
	messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: pending1.ToolCallID, Content: "executed: restartApp"})
	toolCallCount := pending1.ToolCallCount + 1
	writeCallCount := pending1.WriteCallCount + 1

	llm2 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_2", Name: "restartApp", Args: `{}`}}},
	})
	_, _, pending2, _, err := ResumeTurn(context.Background(), llm2, ts, "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil || pending2 == nil {
		t.Fatalf("round 2: pending=%+v err=%v", pending2, err)
	}
	if pending2.WriteCallCount != 1 {
		t.Fatalf("pending2.WriteCallCount=%d want 1", pending2.WriteCallCount)
	}

	messages = append(append([]llmchat.Message{}, pending2.Messages...), llmchat.Message{Role: "tool", ToolCallID: pending2.ToolCallID, Content: "executed: restartApp"})
	toolCallCount = pending2.ToolCallCount + 1
	writeCallCount = pending2.WriteCallCount + 1

	llm3 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_3", Name: "restartApp", Args: `{}`}}},
	})
	_, _, pending3, _, err := ResumeTurn(context.Background(), llm3, ts, "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil || pending3 == nil {
		t.Fatalf("round 3: pending=%+v err=%v", pending3, err)
	}
	if pending3.WriteCallCount != 2 {
		t.Fatalf("pending3.WriteCallCount=%d want 2", pending3.WriteCallCount)
	}

	messages = append(append([]llmchat.Message{}, pending3.Messages...), llmchat.Message{Role: "tool", ToolCallID: pending3.ToolCallID, Content: "executed: restartApp"})
	toolCallCount = pending3.ToolCallCount + 1
	writeCallCount = pending3.WriteCallCount + 1
	if writeCallCount != MaxWriteCallsPerTurn {
		t.Fatalf("writeCallCount=%d want %d (budget) before the 4th attempt", writeCallCount, MaxWriteCallsPerTurn)
	}

	llm4 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_4", Name: "restartApp", Args: `{}`}}},
		{Content: "I could not restart it a 4th time this turn"},
	})
	assistantText, _, pending4, _, err := ResumeTurn(context.Background(), llm4, ts, "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil {
		t.Fatalf("round 4: err=%v", err)
	}
	if pending4 != nil {
		t.Fatalf("pending4=%+v, want nil — the 4th write call must be refused inline, not interrupt again", pending4)
	}
	if assistantText != "I could not restart it a 4th time this turn" {
		t.Fatalf("assistantText=%q, want the final answer after the budget-exhausted tool result", assistantText)
	}
}

func TestRunTurn_AccumulatesUsageAcrossLoop(t *testing.T) {
	ts := newFakeToolset([]string{"listApps"}, nil)
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "listApps", Args: `{}`}}, Model: "claude-sonnet-5", PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		{Content: "you have 2 apps", Model: "claude-sonnet-5", PromptTokens: 150, CompletionTokens: 30, TotalTokens: 180},
	})

	assistantText, _, pending, usage, err := RunTurn(context.Background(), llm, ts, "Bearer test", "test-user", "system", nil, "list my apps", Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if pending != nil {
		t.Fatalf("pending=%+v, want nil", pending)
	}
	if assistantText != "you have 2 apps" {
		t.Fatalf("assistantText=%q want %q", assistantText, "you have 2 apps")
	}
	if usage.Calls != 2 {
		t.Fatalf("usage.Calls=%d want 2 (one gateway call per round)", usage.Calls)
	}
	if usage.PromptTokens != 250 {
		t.Fatalf("usage.PromptTokens=%d want 250 (100+150)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Fatalf("usage.CompletionTokens=%d want 50 (20+30)", usage.CompletionTokens)
	}
	if usage.TotalTokens != 300 {
		t.Fatalf("usage.TotalTokens=%d want 300 (120+180)", usage.TotalTokens)
	}
	if usage.Model != "claude-sonnet-5" {
		t.Fatalf("usage.Model=%q want claude-sonnet-5", usage.Model)
	}
}
