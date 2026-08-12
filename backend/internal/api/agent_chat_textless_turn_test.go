package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/llmchat"
)

// scriptedTurnStep is one gateway round in a multi-round scripted upstream:
// either a normal completion (Content, optionally with ToolCalls) or Fail,
// which makes the gateway answer with an HTTP error status so RunTurn sees a
// streamErr exactly the way a real upstream outage produces one.
type scriptedTurnStep struct {
	Content   string
	ToolCalls []llmchat.ToolCall
	Fail      bool
}

// newScriptedToolCallGateway is newScriptedAgentGateway's sibling: it can also
// emit tool_calls deltas and can fail a round outright, which the plain
// content-only helper in agent_chat_confirm_test.go cannot do. Steps are
// served in order, one per gateway call; calling it more times than scripted
// fails the test the same way.
func newScriptedToolCallGateway(t *testing.T, steps []scriptedTurnStep) *llmchat.Client {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(steps) {
			t.Fatalf("gateway called more times (%d) than scripted (%d)", call+1, len(steps))
		}
		step := steps[call]
		call++

		if step.Fail {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"simulated upstream failure"}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		if len(step.ToolCalls) > 0 {
			for i, tc := range step.ToolCalls {
				b, _ := json.Marshal(map[string]any{
					"choices": []map[string]any{{
						"delta": map[string]any{
							"tool_calls": []map[string]any{{
								"index": i,
								"id":    tc.ID,
								"type":  "function",
								"function": map[string]any{
									"name":      tc.Function.Name,
									"arguments": tc.Function.Arguments,
								},
							}},
						},
					}},
				})
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			fb, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"finish_reason": "tool_calls"}},
			})
			fmt.Fprintf(w, "data: %s\n\n", fb)
		} else {
			b, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": step.Content}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", b)
			fb, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"finish_reason": "stop"}},
			})
			fmt.Fprintf(w, "data: %s\n\n", fb)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return llmchat.New(srv.URL, "test-key", "test-model")
}

// newFakeAgentBackendWithListApps extends newFakeAgentBackend's fixture with a
// working GET /api/v1/fake-list (listApps), so a turn can execute a real,
// non-write tool call before its final answer.
func newFakeAgentBackendWithListApps(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/fake-list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"apps":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAgentChatCtx builds a gin.Context/ResponseRecorder pair for POST
// /api/v1/agent/chat, the SSE entry point AgentChat streams over. Mirrors
// newAgentConfirmCtx in agent_chat_confirm_test.go for the sibling endpoint.
func newAgentChatCtx(userSub, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	uid, err := uuid.Parse(userSub)
	if err != nil {
		uid = uuid.New()
	}
	auth.SetClaims(c, &auth.Claims{UserID: uid})
	return c, rec
}

// TestAgentChat_EmptyFinalMessageAfterToolCall_PersistsFallbackAndStreamsIt is
// the artem case: the model runs a tool, then answers with a genuinely empty
// final message. Before the fix, `if assistantText != "" { insert }` skipped
// the assistant row entirely and "done":{"ok":true} went out over an empty
// stream, leaving the transcript ending on the tool rows with nothing after
// them. It must now fall back to agentChatEmptyTurnAnswer, both in what's
// streamed and in what's persisted.
func TestAgentChat_EmptyFinalMessageAfterToolCall_PersistsFallbackAndStreamsIt(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := agentChatUser(t, pool)

	backend := newFakeAgentBackendWithListApps(t)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedToolCallGateway(t, []scriptedTurnStep{
		{ToolCalls: []llmchat.ToolCall{
			{ID: "call_1", Type: "function", Function: llmchat.ToolCallFunction{Name: "listApps", Arguments: "{}"}},
		}},
		{Content: ""},
	})

	h := &Handler{pool: pool, cfg: &config.Config{}, agentChatLLM: llm, agentChatTools: ts}

	c, rec := newAgentChatCtx(userSub, `{"message":"what apps do I have"}`)
	h.AgentChat(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 0 {
		t.Fatalf("unexpected error events: %v", events["error"])
	}
	if len(events["done"]) == 0 || !strings.Contains(events["done"][len(events["done"])-1], `"ok":true`) {
		t.Fatalf("done events=%v, want a final ok:true", events["done"])
	}

	gotText := strings.Join(events["token"], "")
	if gotText != agentChatEmptyTurnAnswer {
		t.Fatalf("streamed token text=%q, want the fallback %q", gotText, agentChatEmptyTurnAnswer)
	}

	var toolCount, assistantCount int
	var assistantContent string
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='tool' AND tool_name='listApps'`, userSub).Scan(&toolCount)
	if toolCount != 1 {
		t.Fatalf("tool transcript rows=%d want 1 (the executed tool call must survive)", toolCount)
	}
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantCount)
	if assistantCount != 1 {
		t.Fatalf("assistant transcript rows=%d want exactly 1 (the transcript must not end on the tool row)", assistantCount)
	}
	_ = pool.QueryRow(context.Background(), `SELECT content FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantContent)
	if assistantContent != agentChatEmptyTurnAnswer {
		t.Fatalf("assistant row content=%q, want the fallback %q", assistantContent, agentChatEmptyTurnAnswer)
	}
}

// TestAgentChat_UpstreamError_PersistsExecutedToolsAndFailureNotice covers the
// other half of the same bug: RunTurn returning a non-nil error used to make
// the handler return before writing anything, dropping tool rows that already
// executed for real. The fix must persist those tool rows plus one assistant
// row carrying agentChatFailedTurnAnswer, and must still surface the SSE error
// event to the client.
func TestAgentChat_UpstreamError_PersistsExecutedToolsAndFailureNotice(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := agentChatUser(t, pool)

	backend := newFakeAgentBackendWithListApps(t)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedToolCallGateway(t, []scriptedTurnStep{
		{ToolCalls: []llmchat.ToolCall{
			{ID: "call_1", Type: "function", Function: llmchat.ToolCallFunction{Name: "listApps", Arguments: "{}"}},
		}},
		{Fail: true},
	})

	h := &Handler{pool: pool, cfg: &config.Config{}, agentChatLLM: llm, agentChatTools: ts}

	c, rec := newAgentChatCtx(userSub, `{"message":"what apps do I have"}`)
	h.AgentChat(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 1 || !strings.Contains(events["error"][0], `"code":"upstream"`) {
		t.Fatalf("error events=%v, want exactly one upstream error", events["error"])
	}
	if len(events["done"]) == 0 || !strings.Contains(events["done"][len(events["done"])-1], `"ok":false`) {
		t.Fatalf("done events=%v, want a final ok:false", events["done"])
	}

	var toolCount, assistantCount int
	var assistantContent string
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='tool' AND tool_name='listApps'`, userSub).Scan(&toolCount)
	if toolCount != 1 {
		t.Fatalf("tool transcript rows=%d want 1 (the tool ran for real before the upstream call failed)", toolCount)
	}
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantCount)
	if assistantCount != 1 {
		t.Fatalf("assistant transcript rows=%d want exactly 1 (a failure notice must close the turn)", assistantCount)
	}
	_ = pool.QueryRow(context.Background(), `SELECT content FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantContent)
	if assistantContent != agentChatFailedTurnAnswer {
		t.Fatalf("assistant row content=%q, want the failure notice %q", assistantContent, agentChatFailedTurnAnswer)
	}
}

// TestAgentChat_NormalTurn_PersistsModelTextWithoutFallback is the regression
// guard: a turn that legitimately answers with non-empty text must persist
// exactly that text, once, and must never substitute agentChatEmptyTurnAnswer.
func TestAgentChat_NormalTurn_PersistsModelTextWithoutFallback(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := agentChatUser(t, pool)

	backend := newFakeAgentBackendWithListApps(t)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedToolCallGateway(t, []scriptedTurnStep{
		{Content: "You have no apps yet."},
	})

	h := &Handler{pool: pool, cfg: &config.Config{}, agentChatLLM: llm, agentChatTools: ts}

	c, rec := newAgentChatCtx(userSub, `{"message":"what apps do I have"}`)
	h.AgentChat(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 0 {
		t.Fatalf("unexpected error events: %v", events["error"])
	}

	gotText := strings.Join(events["token"], "")
	if gotText != "You have no apps yet." {
		t.Fatalf("streamed token text=%q, want the model's own text", gotText)
	}

	var assistantCount int
	var assistantContent string
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantCount)
	if assistantCount != 1 {
		t.Fatalf("assistant transcript rows=%d want exactly 1", assistantCount)
	}
	_ = pool.QueryRow(context.Background(), `SELECT content FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant'`, userSub).Scan(&assistantContent)
	if assistantContent != "You have no apps yet." {
		t.Fatalf("assistant row content=%q, want the model's own text, and never the fallback %q", assistantContent, agentChatEmptyTurnAnswer)
	}
}
