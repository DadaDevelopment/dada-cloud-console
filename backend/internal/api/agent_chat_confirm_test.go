package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/llmchat"
)

func testAgentChatPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping agent-chat-confirm DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

const fakeRestartSwaggerSpec = `{
	"swagger": "2.0",
	"basePath": "/api/v1",
	"paths": {
		"/fake-restart": {
			"post": {
				"operationId": "restartApp",
				"parameters": [],
				"responses": {"200": {"description": "ok"}}
			}
		},
		"/fake-list": {
			"get": {
				"operationId": "listApps",
				"parameters": [],
				"responses": {"200": {"description": "ok"}}
			}
		}
	}
}`

// newFakeAgentBackend serves the two fake operations referenced by
// fakeRestartSwaggerSpec, recording the Authorization header it saw so
// tests can assert the CONFIRM request's own bearer (not the original chat
// request's) is what actually executes the write.
func newFakeAgentBackend(t *testing.T, restartAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/fake-restart":
			if restartAuth != nil {
				*restartAuth = r.Header.Get("Authorization")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"restarted"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFakeAgentToolset(t *testing.T, backendURL string) *agentchat.Toolset {
	t.Helper()
	ts, err := agentchat.BuildToolset([]byte(fakeRestartSwaggerSpec), backendURL)
	if err != nil {
		t.Fatalf("BuildToolset: %v", err)
	}
	return ts
}

type scriptedGatewayTurn struct {
	Content   string
	ToolCalls []struct {
		ID   string
		Name string
		Args string
	}
}

// newScriptedAgentGateway serves canned OpenAI-style SSE completions, one
// per call, so a full RunTurn/ResumeTurn round can be driven deterministically.
func newScriptedAgentGateway(t *testing.T, finalTexts []string) *llmchat.Client {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(finalTexts) {
			t.Fatalf("gateway called more times (%d) than scripted (%d)", call+1, len(finalTexts))
		}
		text := finalTexts[call]
		call++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		b, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": text}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fb, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop"}},
		})
		fmt.Fprintf(w, "data: %s\n\n", fb)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return llmchat.New(srv.URL, "test-key", "test-model")
}

func newAgentConfirmCtx(userSub, token, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat/confirm", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c.Request = req
	uid, err := uuid.Parse(userSub)
	if err != nil {
		uid = uuid.New()
	}
	auth.SetClaims(c, &auth.Claims{UserID: uid})
	return c, rec
}

func insertPendingRestartAction(t *testing.T, h *Handler, userSub string) uuid.UUID {
	t.Helper()
	pending := &agentchat.PendingWrite{
		ToolName:   "restartApp",
		ToolCallID: "call_1",
		ArgsJSON:   `{"appName":"web"}`,
		Messages: []llmchat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "restart web please"},
			{
				Role: "assistant",
				ToolCalls: []llmchat.ToolCall{
					{ID: "call_1", Type: "function", Function: llmchat.ToolCallFunction{Name: "restartApp", Arguments: `{"appName":"web"}`}},
				},
			},
		},
		ToolCallCount:  0,
		WriteCallCount: 0,
	}
	actionID, err := h.agentChatInsertPendingAction(context.Background(), userSub, "", nil, nil, pending, nil)
	if err != nil {
		t.Fatalf("agentChatInsertPendingAction: %v", err)
	}
	return actionID
}

// parseSSEEvents groups the response body into event-name -> payloads under
// the gin sse.Encode framing (4419e8a): "event:"/"data:" carry no cosmetic
// space, and a multi-line payload arrives as several "data:" continuation
// lines within one block, joined back with "\n" on the blank terminator.
func parseSSEEvents(t *testing.T, body string) map[string][]string {
	t.Helper()
	events := map[string][]string{}
	currentEvent := "message"
	var dataLines []string
	flush := func() {
		if len(dataLines) > 0 {
			events[currentEvent] = append(events[currentEvent], strings.Join(dataLines, "\n"))
		}
		currentEvent = "message"
		dataLines = nil
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	flush()
	return events
}

func TestAgentChatConfirm_Approve_ExecutesToolAndResumes(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := uuid.New().String()

	var seenAuth string
	backend := newFakeAgentBackend(t, &seenAuth)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedAgentGateway(t, []string{"Restarted web."})

	h := &Handler{pool: pool, agentChatLLM: llm, agentChatTools: ts}
	actionID := insertPendingRestartAction(t, h, userSub)

	c, rec := newAgentConfirmCtx(userSub, "confirm-bearer-token", fmt.Sprintf(`{"action_id":%q,"decision":"approve"}`, actionID))
	h.AgentChatConfirm(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 0 {
		t.Fatalf("unexpected error events: %v", events["error"])
	}
	if len(events["done"]) == 0 || !strings.Contains(events["done"][len(events["done"])-1], `"ok":true`) {
		t.Fatalf("done events=%v, want a final ok:true", events["done"])
	}
	gotText := strings.Join(events["token"], "")
	if gotText != "Restarted web." {
		t.Fatalf("streamed token text=%q want %q", gotText, "Restarted web.")
	}

	if seenAuth != "Bearer confirm-bearer-token" {
		t.Fatalf("backend saw Authorization=%q, want the CONFIRM request's own bearer (Bearer confirm-bearer-token)", seenAuth)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM agent_chat_pending_actions WHERE id=$1`, actionID).Scan(&status); err != nil {
		t.Fatalf("query pending row: %v", err)
	}
	if status != "approved" {
		t.Fatalf("pending row status=%q want approved", status)
	}

	var toolCount, confirmResultCount, assistantCount int
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='tool' AND tool_name='restartApp'`, userSub).Scan(&toolCount)
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='confirm_result'`, userSub).Scan(&confirmResultCount)
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='assistant' AND content='Restarted web.'`, userSub).Scan(&assistantCount)
	if toolCount != 1 {
		t.Fatalf("tool transcript rows=%d want 1 (the tool must have actually executed)", toolCount)
	}
	if confirmResultCount != 1 {
		t.Fatalf("confirm_result transcript rows=%d want 1", confirmResultCount)
	}
	if assistantCount != 1 {
		t.Fatalf("assistant transcript rows=%d want 1 (final resumed answer)", assistantCount)
	}
}

func TestAgentChatConfirm_Reject_LeavesStateUntouched(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := uuid.New().String()

	var seenAuth string
	backend := newFakeAgentBackend(t, &seenAuth)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedAgentGateway(t, []string{"OK, I will not restart it."})

	h := &Handler{pool: pool, agentChatLLM: llm, agentChatTools: ts}
	actionID := insertPendingRestartAction(t, h, userSub)

	c, rec := newAgentConfirmCtx(userSub, "confirm-bearer-token", fmt.Sprintf(`{"action_id":%q,"decision":"reject"}`, actionID))
	h.AgentChatConfirm(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 0 {
		t.Fatalf("unexpected error events: %v", events["error"])
	}

	if seenAuth != "" {
		t.Fatalf("backend saw Authorization=%q, want the write tool to never have executed on reject", seenAuth)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM agent_chat_pending_actions WHERE id=$1`, actionID).Scan(&status); err != nil {
		t.Fatalf("query pending row: %v", err)
	}
	if status != "rejected" {
		t.Fatalf("pending row status=%q want rejected", status)
	}

	var toolCount int
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_chat_messages WHERE user_sub=$1 AND role='tool' AND tool_name='restartApp'`, userSub).Scan(&toolCount)
	if toolCount != 0 {
		t.Fatalf("tool transcript rows=%d want 0 (nothing must have executed on reject)", toolCount)
	}
}

func TestAgentChatConfirm_Expired_NoExecution(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := uuid.New().String()

	var seenAuth string
	backend := newFakeAgentBackend(t, &seenAuth)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedAgentGateway(t, nil)

	h := &Handler{pool: pool, agentChatLLM: llm, agentChatTools: ts}
	actionID := insertPendingRestartAction(t, h, userSub)

	if _, err := pool.Exec(context.Background(), `UPDATE agent_chat_pending_actions SET expires_at = now() - interval '1 minute' WHERE id=$1`, actionID); err != nil {
		t.Fatalf("force-expire pending row: %v", err)
	}

	c, rec := newAgentConfirmCtx(userSub, "confirm-bearer-token", fmt.Sprintf(`{"action_id":%q,"decision":"approve"}`, actionID))
	h.AgentChatConfirm(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 1 || !strings.Contains(events["error"][0], `"code":"expired"`) {
		t.Fatalf("error events=%v, want exactly one expired error", events["error"])
	}
	if seenAuth != "" {
		t.Fatalf("backend saw Authorization=%q, an expired action must never execute", seenAuth)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM agent_chat_pending_actions WHERE id=$1`, actionID).Scan(&status); err != nil {
		t.Fatalf("query pending row: %v", err)
	}
	if status != "expired" {
		t.Fatalf("pending row status=%q want expired", status)
	}
}

func TestAgentChatConfirm_WrongUser_Forbidden(t *testing.T) {
	pool := testAgentChatPool(t)
	ownerSub := uuid.New().String()
	attackerSub := uuid.New().String()

	var seenAuth string
	backend := newFakeAgentBackend(t, &seenAuth)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedAgentGateway(t, nil)

	h := &Handler{pool: pool, agentChatLLM: llm, agentChatTools: ts}
	actionID := insertPendingRestartAction(t, h, ownerSub)

	c, rec := newAgentConfirmCtx(attackerSub, "attacker-bearer-token", fmt.Sprintf(`{"action_id":%q,"decision":"approve"}`, actionID))
	h.AgentChatConfirm(c)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events["error"]) != 1 || !strings.Contains(events["error"][0], `"code":"forbidden"`) {
		t.Fatalf("error events=%v, want exactly one forbidden error", events["error"])
	}
	if seenAuth != "" {
		t.Fatalf("backend saw Authorization=%q, another user's pending action must never execute", seenAuth)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM agent_chat_pending_actions WHERE id=$1`, actionID).Scan(&status); err != nil {
		t.Fatalf("query pending row: %v", err)
	}
	if status != "pending" {
		t.Fatalf("pending row status=%q want still pending (an unauthorized attempt must not consume it)", status)
	}
}

func TestAgentChatConfirm_DoubleConfirm_SecondGetsConflict(t *testing.T) {
	pool := testAgentChatPool(t)
	userSub := uuid.New().String()

	var restartCount int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/fake-restart" {
			restartCount++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"restarted"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(backend.Close)
	ts := newFakeAgentToolset(t, backend.URL)
	llm := newScriptedAgentGateway(t, []string{"Restarted web."})

	h := &Handler{pool: pool, agentChatLLM: llm, agentChatTools: ts}
	actionID := insertPendingRestartAction(t, h, userSub)

	c1, rec1 := newAgentConfirmCtx(userSub, "confirm-bearer-token", fmt.Sprintf(`{"action_id":%q,"decision":"approve"}`, actionID))
	h.AgentChatConfirm(c1)
	events1 := parseSSEEvents(t, rec1.Body.String())
	if len(events1["error"]) != 0 {
		t.Fatalf("first confirm: unexpected error events: %v", events1["error"])
	}

	c2, rec2 := newAgentConfirmCtx(userSub, "confirm-bearer-token-2", fmt.Sprintf(`{"action_id":%q,"decision":"approve"}`, actionID))
	h.AgentChatConfirm(c2)
	events2 := parseSSEEvents(t, rec2.Body.String())
	if len(events2["error"]) != 1 || !strings.Contains(events2["error"][0], `"code":"conflict"`) {
		t.Fatalf("second confirm error events=%v, want exactly one conflict error", events2["error"])
	}

	if restartCount != 1 {
		t.Fatalf("restart backend called %d times, want exactly 1 (no double-execution)", restartCount)
	}
}
