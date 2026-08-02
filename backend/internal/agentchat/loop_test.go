package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// capturedRequest is the subset of a gateway request body the tests assert on:
// the exact message list the loop assembled for that round.
type capturedRequest struct {
	Messages []llmchat.Message `json:"messages"`
}

// newScriptedGatewayServer serves a canned sequence of OpenAI-style SSE
// streaming completions, one per call to /v1/chat/completions, so loop.go's
// round loop can be exercised without a real LLM gateway. Calling past the
// end of the script fails the test loudly instead of hanging. When captured is
// non-nil every request body is appended to it first.
func newScriptedGatewayServer(t *testing.T, turns []scriptedTurn, captured *[]capturedRequest) *httptest.Server {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			body, _ := io.ReadAll(r.Body)
			var req capturedRequest
			_ = json.Unmarshal(body, &req)
			*captured = append(*captured, req)
		}
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
	return newTestClientCapturing(t, turns, nil)
}

func newTestClientCapturing(t *testing.T, turns []scriptedTurn, captured *[]capturedRequest) *llmchat.Client {
	srv := newScriptedGatewayServer(t, turns, captured)
	return llmchat.New(srv.URL, "test-key", "test-model")
}

func systemMessagesJoined(req capturedRequest) string {
	var parts []string
	for _, m := range req.Messages {
		if m.Role == "system" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func fakeHandler(result string, isError bool) internalmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: result}},
			IsError: isError,
		}, nil
	}
}

func slowFakeHandler(result string, d time.Duration) internalmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		time.Sleep(d)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: result}},
		}, nil
	}
}

// newFakeToolset builds a Toolset directly (bypassing BuildToolset/spec
// parsing) so loop tests can control exactly which tool names are
// classified as read vs write, and what executing them returns, without a
// running backend to self-proxy against.
func newFakeToolset(readNames, writeNames []string) *Toolset {
	ts := newEmptyFakeToolset()
	for _, name := range readNames {
		addFakeTool(ts, name, "ok: "+name, false, false)
	}
	for _, name := range writeNames {
		addFakeTool(ts, name, "executed: "+name, false, true)
	}
	return ts
}

// newEmptyFakeToolset builds a Toolset with every internal map initialised, so
// tests can register tools into it and open a ToolView over it exactly like
// BuildToolset's output.
func newEmptyFakeToolset() *Toolset {
	return &Toolset{
		handlers:  map[string]internalmcp.ToolHandler{},
		writeSet:  map[string]bool{},
		defByName: map[string]llmchat.ToolDef{SearchToolsTool: searchToolsDef},
	}
}

func addFakeTool(ts *Toolset, name, result string, isError, isWrite bool) {
	def := llmchat.ToolDef{Type: "function", Function: llmchat.ToolFunctionDef{Name: name}}
	ts.Defs = append(ts.Defs, def)
	ts.defByName[name] = def
	ts.order = append(ts.order, name)
	ts.index = append(ts.index, toolSearchEntry{name: name, lowerName: strings.ToLower(name)})
	ts.handlers[name] = fakeHandler(result, isError)
	if isWrite {
		ts.writeSet[name] = true
	}
}

// newInventoryToolset registers exactly the three tools the inventory preflight
// calls, each returning a canned JSON payload.
func newInventoryToolset(projectsJSON, projectJSON, appsJSON string) *Toolset {
	ts := newEmptyFakeToolset()
	addFakeTool(ts, preflightListProjectsTool, projectsJSON, false, false)
	addFakeTool(ts, preflightGetProjectTool, projectJSON, false, false)
	addFakeTool(ts, preflightListAppsTool, appsJSON, false, false)
	return ts
}

const groundedProjectsJSON = `{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"demo","default_environment":"prod"}]}`

const groundedProjectJSON = `{"project":{"id":"11111111-1111-1111-1111-111111111111","name":"demo","default_environment":"prod"},"environments":[{"id":"22222222-2222-2222-2222-222222222222","name":"dev","is_ephemeral":false},{"id":"33333333-3333-3333-3333-333333333333","name":"prod","is_ephemeral":false}]}`

const groundedAppsJSON = `{"apps":[{"name":"web","phase":"Ready"},{"name":"worker","phase":"Pending"}]}`

// groundedCtx is the turn context of a user already sitting on an app page:
// preflight is skipped, so the older loop tests keep their exact tool logs.
var groundedCtx = TurnContext{ProjectID: "p", EnvID: "e", AppName: "web"}

func TestRunTurn_FirstWriteToolCall_StopsWithoutExecuting(t *testing.T) {
	ts := newFakeToolset(nil, []string{"restartApp"})
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "restartApp", Args: `{"appName":"web"}`}}},
	})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "restart web please", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.AssistantText != "" {
		t.Fatalf("assistantText=%q, want empty (turn should have interrupted)", res.AssistantText)
	}
	if len(res.ToolLog) != 0 {
		t.Fatalf("toolLog=%v, want empty - the write tool must not have executed", res.ToolLog)
	}
	if res.Pending == nil {
		t.Fatal("pending is nil, want a PendingWrite for the interrupted restartApp call")
	}
	if res.Pending.ToolName != "restartApp" {
		t.Fatalf("pending.ToolName=%q want restartApp", res.Pending.ToolName)
	}
	if res.Pending.ToolCallID != "call_1" {
		t.Fatalf("pending.ToolCallID=%q want call_1", res.Pending.ToolCallID)
	}
	if res.Pending.ArgsJSON != `{"appName":"web"}` {
		t.Fatalf("pending.ArgsJSON=%q want {\"appName\":\"web\"}", res.Pending.ArgsJSON)
	}
	if len(res.Pending.Messages) == 0 {
		t.Fatal("pending.Messages snapshot is empty")
	}
	last := res.Pending.Messages[len(res.Pending.Messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("last snapshotted message role=%q want assistant (the message that requested the write call)", last.Role)
	}
	if res.Pending.WriteCallCount != 0 {
		t.Fatalf("pending.WriteCallCount=%d want 0 (no write has executed yet)", res.Pending.WriteCallCount)
	}
}

func TestRunTurn_ReadOnlyToolCalls_RunToCompletion(t *testing.T) {
	ts := newFakeToolset([]string{"listApps"}, nil)
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "listApps", Args: `{}`}}},
		{Content: "you have 2 apps"},
	})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "list my apps", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Pending != nil {
		t.Fatalf("pending=%+v, want nil - an all-read turn must not interrupt", res.Pending)
	}
	if res.AssistantText != "you have 2 apps" {
		t.Fatalf("assistantText=%q want %q", res.AssistantText, "you have 2 apps")
	}
	if len(res.ToolLog) != 1 || res.ToolLog[0].Name != "listApps" {
		t.Fatalf("toolLog=%v, want one listApps entry", res.ToolLog)
	}
}

func TestRunTurn_WriteCallBudget_CappedAtThreeAcrossResumes(t *testing.T) {
	ts := newFakeToolset(nil, []string{"restartApp"})

	llm1 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "restartApp", Args: `{}`}}},
	})
	res1, err := RunTurn(context.Background(), llm1, ts.NewView(), "Bearer test", "test-user", "system", nil, "restart", groundedCtx, Emitter{})
	if err != nil || res1.Pending == nil {
		t.Fatalf("round 1: pending=%+v err=%v", res1.Pending, err)
	}

	messages := append([]llmchat.Message{}, res1.Pending.Messages...)
	messages = append(messages, llmchat.Message{Role: "tool", ToolCallID: res1.Pending.ToolCallID, Content: "executed: restartApp"})
	toolCallCount := res1.Pending.ToolCallCount + 1
	writeCallCount := res1.Pending.WriteCallCount + 1

	llm2 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_2", Name: "restartApp", Args: `{}`}}},
	})
	res2, err := ResumeTurn(context.Background(), llm2, ts.NewView(), "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil || res2.Pending == nil {
		t.Fatalf("round 2: pending=%+v err=%v", res2.Pending, err)
	}
	if res2.Pending.WriteCallCount != 1 {
		t.Fatalf("pending2.WriteCallCount=%d want 1", res2.Pending.WriteCallCount)
	}

	messages = append(append([]llmchat.Message{}, res2.Pending.Messages...), llmchat.Message{Role: "tool", ToolCallID: res2.Pending.ToolCallID, Content: "executed: restartApp"})
	toolCallCount = res2.Pending.ToolCallCount + 1
	writeCallCount = res2.Pending.WriteCallCount + 1

	llm3 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_3", Name: "restartApp", Args: `{}`}}},
	})
	res3, err := ResumeTurn(context.Background(), llm3, ts.NewView(), "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil || res3.Pending == nil {
		t.Fatalf("round 3: pending=%+v err=%v", res3.Pending, err)
	}
	if res3.Pending.WriteCallCount != 2 {
		t.Fatalf("pending3.WriteCallCount=%d want 2", res3.Pending.WriteCallCount)
	}

	messages = append(append([]llmchat.Message{}, res3.Pending.Messages...), llmchat.Message{Role: "tool", ToolCallID: res3.Pending.ToolCallID, Content: "executed: restartApp"})
	toolCallCount = res3.Pending.ToolCallCount + 1
	writeCallCount = res3.Pending.WriteCallCount + 1
	if writeCallCount != MaxWriteCallsPerTurn {
		t.Fatalf("writeCallCount=%d want %d (budget) before the 4th attempt", writeCallCount, MaxWriteCallsPerTurn)
	}

	llm4 := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_4", Name: "restartApp", Args: `{}`}}},
		{Content: "I could not restart it a 4th time this turn"},
	})
	res4, err := ResumeTurn(context.Background(), llm4, ts.NewView(), "Bearer test", "test-user", messages, toolCallCount, writeCallCount, Emitter{})
	if err != nil {
		t.Fatalf("round 4: err=%v", err)
	}
	if res4.Pending != nil {
		t.Fatalf("pending4=%+v, want nil - the 4th write call must be refused inline, not interrupt again", res4.Pending)
	}
	if res4.AssistantText != "I could not restart it a 4th time this turn" {
		t.Fatalf("assistantText=%q, want the final answer after the budget-exhausted tool result", res4.AssistantText)
	}
}

func TestRunTurn_AccumulatesUsageAcrossLoop(t *testing.T) {
	ts := newFakeToolset([]string{"listApps"}, nil)
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: "listApps", Args: `{}`}}, Model: "claude-sonnet-5", PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		{Content: "you have 2 apps", Model: "claude-sonnet-5", PromptTokens: 150, CompletionTokens: 30, TotalTokens: 180},
	})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "list my apps", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Pending != nil {
		t.Fatalf("pending=%+v, want nil", res.Pending)
	}
	if res.AssistantText != "you have 2 apps" {
		t.Fatalf("assistantText=%q want %q", res.AssistantText, "you have 2 apps")
	}
	if res.Usage.Calls != 2 {
		t.Fatalf("usage.Calls=%d want 2 (one gateway call per round)", res.Usage.Calls)
	}
	if res.Usage.PromptTokens != 250 {
		t.Fatalf("usage.PromptTokens=%d want 250 (100+150)", res.Usage.PromptTokens)
	}
	if res.Usage.CompletionTokens != 50 {
		t.Fatalf("usage.CompletionTokens=%d want 50 (20+30)", res.Usage.CompletionTokens)
	}
	if res.Usage.TotalTokens != 300 {
		t.Fatalf("usage.TotalTokens=%d want 300 (120+180)", res.Usage.TotalTokens)
	}
	if res.Usage.Model != "claude-sonnet-5" {
		t.Fatalf("usage.Model=%q want claude-sonnet-5", res.Usage.Model)
	}
}

func TestRunTurn_Preflight_EmptyContext_LooksUpProjectsAndApps(t *testing.T) {
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, groundedAppsJSON)
	var captured []capturedRequest
	llm := newTestClientCapturing(t, []scriptedTurn{{Content: "web is Ready, worker is Pending"}}, &captured)

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "how is my stuff", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(res.ToolLog) != 3 {
		t.Fatalf("toolLog=%+v, want 3 preflight entries", res.ToolLog)
	}
	want := []string{preflightListProjectsTool, preflightGetProjectTool, preflightListAppsTool}
	for i, name := range want {
		if res.ToolLog[i].Name != name {
			t.Fatalf("toolLog[%d].Name=%q want %q", i, res.ToolLog[i].Name, name)
		}
		if !res.ToolLog[i].Preflight {
			t.Fatalf("toolLog[%d].Preflight=false, want true", i)
		}
	}
	if res.PreflightCalls != 3 {
		t.Fatalf("PreflightCalls=%d want 3", res.PreflightCalls)
	}
	if res.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount=%d want 0 - preflight must not spend the model's budget", res.ToolCallCount)
	}
	if res.InventoryProjects != 1 {
		t.Fatalf("InventoryProjects=%d want 1", res.InventoryProjects)
	}
	if res.InventoryApps != 2 {
		t.Fatalf("InventoryApps=%d want 2", res.InventoryApps)
	}
	if !res.InventoryAppsLookedUp {
		t.Fatal("InventoryAppsLookedUp=false, want true")
	}
	if len(captured) == 0 {
		t.Fatal("gateway captured no requests")
	}
	sys := systemMessagesJoined(captured[0])
	if !strings.Contains(sys, inventoryHeader) {
		t.Fatalf("system messages missing inventory header: %q", sys)
	}
	if !strings.Contains(sys, "web") || !strings.Contains(sys, "worker") {
		t.Fatalf("system messages missing app names: %q", sys)
	}
}

func TestRunTurn_Preflight_PicksProdEnvironmentForListApps(t *testing.T) {
	projectJSON := `{"project":{"default_environment":"prod"},"environments":[` +
		`{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"dev","is_ephemeral":false},` +
		`{"id":"aaaaaaaa-0000-0000-0000-000000000002","name":"pr-42","is_ephemeral":true},` +
		`{"id":"aaaaaaaa-0000-0000-0000-000000000003","name":"prod","is_ephemeral":false}]}`
	ts := newInventoryToolset(groundedProjectsJSON, projectJSON, groundedAppsJSON)
	llm := newTestClient(t, []scriptedTurn{{Content: "ok"}})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "status?", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(res.ToolLog) != 3 {
		t.Fatalf("toolLog=%+v, want 3 entries", res.ToolLog)
	}
	if !strings.Contains(res.ToolLog[2].ArgsJSON, "aaaaaaaa-0000-0000-0000-000000000003") {
		t.Fatalf("listApps args=%q, want the prod environment id", res.ToolLog[2].ArgsJSON)
	}
}

func TestRunTurn_Preflight_SkippedWhenAppNameKnown(t *testing.T) {
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, groundedAppsJSON)
	var captured []capturedRequest
	llm := newTestClientCapturing(t, []scriptedTurn{{Content: "ok"}}, &captured)

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "why is it down", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(res.ToolLog) != 0 {
		t.Fatalf("toolLog=%+v, want empty - the turn is already grounded on an app", res.ToolLog)
	}
	if res.PreflightCalls != 0 {
		t.Fatalf("PreflightCalls=%d want 0", res.PreflightCalls)
	}
	if res.InventoryProjects != 0 {
		t.Fatalf("InventoryProjects=%d want 0", res.InventoryProjects)
	}
	if strings.Contains(systemMessagesJoined(captured[0]), inventoryHeader) {
		t.Fatal("system messages contain an inventory block, want none")
	}
}

func TestRunTurn_Preflight_NoApps_TellsModelNothingIsDeployed(t *testing.T) {
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, `{"apps":[]}`)
	var captured []capturedRequest
	llm := newTestClientCapturing(t, []scriptedTurn{{Content: "nothing deployed yet"}}, &captured)

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "how do I host my telegram bot", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.InventoryApps != 0 {
		t.Fatalf("InventoryApps=%d want 0", res.InventoryApps)
	}
	if !res.InventoryAppsLookedUp {
		t.Fatal("InventoryAppsLookedUp=false, want true - listApps did run")
	}
	sys := systemMessagesJoined(captured[0])
	if !strings.Contains(sys, inventoryNoAppsMarker) {
		t.Fatalf("system messages missing %q: %q", inventoryNoAppsMarker, sys)
	}
	if !strings.Contains(sys, inventoryNoAppsInstruction) {
		t.Fatalf("system messages missing the do-not-ask instruction: %q", sys)
	}
}

func TestRunTurn_Preflight_ToolError_DoesNotBreakTurn(t *testing.T) {
	ts := newEmptyFakeToolset()
	addFakeTool(ts, preflightListProjectsTool, "forbidden", true, false)
	var captured []capturedRequest
	llm := newTestClientCapturing(t, []scriptedTurn{{Content: "here is what I can tell you"}}, &captured)

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "help", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.AssistantText != "here is what I can tell you" {
		t.Fatalf("assistantText=%q, want the model answer", res.AssistantText)
	}
	if res.InventoryProjects != 0 {
		t.Fatalf("InventoryProjects=%d want 0", res.InventoryProjects)
	}
	if len(res.ToolLog) != 1 {
		t.Fatalf("toolLog=%+v, want the single failed preflight entry", res.ToolLog)
	}
	if !res.ToolLog[0].IsError || !res.ToolLog[0].Preflight {
		t.Fatalf("toolLog[0]=%+v, want IsError and Preflight true", res.ToolLog[0])
	}
	if strings.Contains(systemMessagesJoined(captured[0]), inventoryHeader) {
		t.Fatal("system messages contain an inventory block, want none after a failed preflight")
	}
}

func TestRunTurn_Preflight_MissingTool_DoesNotBreakTurn(t *testing.T) {
	ts := newFakeToolset([]string{"getAppLogs"}, nil)
	var captured []capturedRequest
	llm := newTestClientCapturing(t, []scriptedTurn{{Content: "answered anyway"}}, &captured)

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "help", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.AssistantText != "answered anyway" {
		t.Fatalf("assistantText=%q, want the model answer", res.AssistantText)
	}
	if len(res.ToolLog) != 1 || !res.ToolLog[0].IsError {
		t.Fatalf("toolLog=%+v, want one failed preflight entry", res.ToolLog)
	}
	if !strings.Contains(res.ToolLog[0].Result, "unknown tool") {
		t.Fatalf("toolLog[0].Result=%q, want an unknown tool error", res.ToolLog[0].Result)
	}
	if strings.Contains(systemMessagesJoined(captured[0]), inventoryHeader) {
		t.Fatal("system messages contain an inventory block, want none")
	}
}

func TestRunTurn_Preflight_DoesNotConsumeToolBudget(t *testing.T) {
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, groundedAppsJSON)
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: preflightListAppsTool, Args: `{"projectId":"p","envId":"e"}`}}},
		{Content: "still 2 apps"},
	})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "recheck", TurnContext{}, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount=%d want 1 (only the model's own call)", res.ToolCallCount)
	}
	if len(res.ToolLog) != 4 {
		t.Fatalf("toolLog=%+v, want 4 entries (3 preflight + 1 model)", res.ToolLog)
	}
	if res.ToolLog[3].Preflight {
		t.Fatal("toolLog[3].Preflight=true, want false for the model's own call")
	}
}

func TestRunTurn_ToolLog_RecordsArgsAndDuration(t *testing.T) {
	ts := newEmptyFakeToolset()
	addFakeTool(ts, preflightListAppsTool, "", false, false)
	ts.handlers[preflightListAppsTool] = slowFakeHandler(groundedAppsJSON, 5*time.Millisecond)

	args := `{"projectId":"p","envId":"e"}`
	llm := newTestClient(t, []scriptedTurn{
		{ToolCalls: []scriptedToolCall{{ID: "call_1", Name: preflightListAppsTool, Args: args}}},
		{Content: "done"},
	})

	res, err := RunTurn(context.Background(), llm, ts.NewView(), "Bearer test", "test-user", "system", nil, "list", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(res.ToolLog) != 1 {
		t.Fatalf("toolLog=%+v, want exactly the model's own call", res.ToolLog)
	}
	entry := res.ToolLog[0]
	if entry.ArgsJSON != args {
		t.Fatalf("ArgsJSON=%q want %q", entry.ArgsJSON, args)
	}
	if entry.DurationMs < 1 {
		t.Fatalf("DurationMs=%d, want at least 1 for a 5ms tool", entry.DurationMs)
	}
	if entry.Preflight {
		t.Fatal("Preflight=true, want false for the model's own call")
	}
}

// TestRunTurn_SearchToolsDoesNotSpendToolBudget guards the lazy-loading design:
// discovery is how the model reaches a capability at all, so charging it against
// MaxToolCallsPerTurn would make a few searches eat the whole turn and reproduce
// the "go to the console UI" dead end the toolset was built to remove.
func TestRunTurn_SearchToolsDoesNotSpendToolBudget(t *testing.T) {
	ts := newFakeToolset([]string{"listDomains"}, nil)

	script := []scriptedTurn{}
	for i := 0; i < maxSearchCallsPerTurn; i++ {
		script = append(script, scriptedTurn{ToolCalls: []scriptedToolCall{
			{ID: fmt.Sprintf("call_s%d", i), Name: SearchToolsTool, Args: `{"query":"domain"}`},
		}})
	}
	script = append(script,
		scriptedTurn{ToolCalls: []scriptedToolCall{{ID: "call_r", Name: "listDomains", Args: `{}`}}},
		scriptedTurn{Content: "here are your domains"},
	)

	res, err := RunTurn(context.Background(), newTestClient(t, script), ts.NewView(), "Bearer test", "test-user", "system", nil, "домен biba.ru", groundedCtx, Emitter{})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.AssistantText != "here are your domains" {
		t.Fatalf("assistantText=%q want the final answer", res.AssistantText)
	}
	if res.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount=%d, want 1: only listDomains may spend the budget, %d search_tools calls must be free", res.ToolCallCount, maxSearchCallsPerTurn)
	}
}
