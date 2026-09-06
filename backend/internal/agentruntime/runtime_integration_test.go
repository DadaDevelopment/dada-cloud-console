package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testRuntimeToken = "runtime-integration-test-key-32-characters"

type testHooks struct{ err error }

func (h testHooks) Execute(context.Context, string, Conversation, any) error { return h.err }
func (h testHooks) ListIdleHooks(context.Context) ([]Hook, error)            { return nil, nil }

type runFunc func(context.Context, AgentRunRequest) (string, error)

func (f runFunc) Send(ctx context.Context, r AgentRunRequest) (string, error) { return f(ctx, r) }
func postRuntime(t *testing.T, base, path string, body any, token string) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var out map[string]any
	if res.StatusCode != http.StatusUnauthorized {
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	}
	return res.StatusCode, out
}
func TestPGRuntimeControlContinuityPauseAndRestart(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	root := t.TempDir()
	agent := "runtime-test-" + uuid.NewString()[:8]
	dir := filepath.Join(root, "agents", agent, "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deposit.md"), []byte("Reports do not verify deposits."), 0600))
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	calls, crmCalls := 0, 0
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crmCalls++
		require.Equal(t, http.MethodPut, r.Method)
		require.NotEmpty(t, r.Header.Get("Idempotency-Key"))
		require.Equal(t, "Bearer crm-test", r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"applied":true,"status":"AGENT_PAUSED"}`)
	}))
	defer crm.Close()
	srv := NewServer(store.pool, root)
	srv.runtime.hooks = testHooks{}
	srv.pauseCRM = NewHTTPPauseCRM(crm.URL, "crm-test", "AGENT_PAUSED")
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	var contextID string
	var captured AgentRunRequest
	model := runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		captured = run
		if contextID != "" {
			require.Equal(t, contextID, run.ContextID)
		}
		contextID = run.ContextID
		cap := run.ConversationContext.ContextToken
		switch calls {
		case 1:
			require.Len(t, run.Messages, 1)
			require.Empty(t, run.ConversationContext.State.ReportedFacts)
			status, out := postRuntime(t, httpSrv.URL, "/tools/load-skill", map[string]any{"context_token": cap, "skill": "deposit"}, testRuntimeToken)
			require.Equal(t, 200, status)
			require.Equal(t, "Reports do not verify deposits.", out["content"])
			version := out["state_version"]
			status, out = postRuntime(t, httpSrv.URL, "/tools/load-skill", map[string]any{"context_token": cap, "skill": "deposit"}, testRuntimeToken)
			require.Equal(t, 200, status)
			require.Equal(t, version, out["state_version"])
			status, rejected := postRuntime(t, httpSrv.URL, "/tools/update-state", map[string]any{"context_token": cap, "expected_version": version, "patch": map[string]any{"reported_facts": map[string]any{"deposit": map[string]any{"value": "deposit verified", "source_message_id": run.Messages[0].ID}}}}, testRuntimeToken)
			require.Equal(t, 200, status, "model tools must receive the validation response body")
			require.Equal(t, false, rejected["updated"])
			require.Equal(t, ErrInvalidFactQuote.Error(), rejected["error"])
			status, _ = postRuntime(t, httpSrv.URL, "/tools/update-state", map[string]any{"context_token": cap, "expected_version": version, "patch": map[string]any{"reported_facts": map[string]any{"deposit": map[string]any{"value": "I deposited", "source_message_id": run.Messages[0].ID}}, "open_loops": map[string]any{"access": map[string]any{"question": "Check access", "status": "open", "source_message_id": run.Messages[0].ID}}}}, testRuntimeToken)
			require.Equal(t, 200, status)
			status, conflict := postRuntime(t, httpSrv.URL, "/tools/update-state", map[string]any{"context_token": cap, "expected_version": version, "patch": map[string]any{}}, testRuntimeToken)
			require.Equal(t, 200, status)
			require.Equal(t, false, conflict["updated"])
			require.Greater(t, conflict["state"].(map[string]any)["version"].(float64), version.(float64))
			return "Report noted", nil
		case 2:
			require.Equal(t, "I deposited", run.ConversationContext.State.ReportedFacts["deposit"].Value)
			require.Equal(t, "open", run.ConversationContext.State.OpenLoops["access"].Status)
			require.Equal(t, "Reports do not verify deposits.", run.ConversationContext.State.ActiveSkills["deposit"].Content)
			require.Len(t, run.Messages, 1, "handled messages must not be resent as new input")
			status, out := postRuntime(t, httpSrv.URL, "/tools/stop-agent", map[string]any{"context_token": cap, "reason": "outside automated handling"}, testRuntimeToken)
			require.Equal(t, 200, status)
			require.Equal(t, false, out["agent_enabled"])
			require.Equal(t, "completed", out["crm_status_sync"])
			return "This must be suppressed", nil
		default:
			t.Errorf("paused conversation invoked model")
			return "unexpected", nil
		}
	})
	srv.runtime.a2a = model
	request := func(id, text string) map[string]any {
		return map[string]any{"agent_name": agent, "channel": "telegram", "external_id": "eval-chat", "actor": map[string]any{"external_id": "eval-user"}, "messages": []map[string]any{{"content": text, "channel_message_id": id}}}
	}
	code, out := postRuntime(t, httpSrv.URL, "/message", request("101", "I deposited"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, "Report noted", out["text"])
	// Reconstruct the service against the same Postgres, without in-memory context.
	httpSrv.Close()
	srv = NewServer(store.pool, root)
	srv.runtime.hooks = testHooks{}
	srv.runtime.a2a = model
	srv.pauseCRM = NewHTTPPauseCRM(crm.URL, "crm-test", "AGENT_PAUSED")
	httpSrv = httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	code, out = postRuntime(t, httpSrv.URL, "/message", request("102", "Done"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, true, out["suppressed"])
	require.Empty(t, out["text"])
	for _, id := range []string{"102", "103"} {
		code, out = postRuntime(t, httpSrv.URL, "/message", request(id, "Hello again"), testRuntimeToken)
		require.Equal(t, 200, code)
		require.Equal(t, true, out["suppressed"])
	}
	require.Equal(t, 2, calls)
	require.Equal(t, 1, crmCalls)
	cap := captured.ConversationContext.ContextToken
	code, _ = postRuntime(t, httpSrv.URL, "/tools/stop-agent", map[string]any{"context_token": cap, "reason": "repeat"}, testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, 1, crmCalls)
	code, _ = postRuntime(t, httpSrv.URL, "/tools/load-skill", map[string]any{"context_token": cap, "skill": "deposit"}, "")
	require.Equal(t, 401, code)
	code, _ = postRuntime(t, httpSrv.URL, "/tools/load-skill", map[string]any{"context_token": cap + "bad", "skill": "deposit"}, testRuntimeToken)
	require.Equal(t, 403, code)
	state, err := store.GetState(ctx, uuid.MustParse(captured.ConversationContext.ConversationID))
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	t.Cleanup(func() {
		_, err := store.pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name=$1`, agent)
		require.NoError(t, err)
	})
}
func TestPGRuntimeRetryRetainsInterruptedInput(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.runtime.hooks = testHooks{}
	calls := 0
	srv.runtime.a2a = runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		require.Len(t, run.Messages, 1)
		if calls == 1 {
			return "", errors.New("interrupted")
		}
		return "recovered", nil
	})
	req := MessageRequest{AgentName: "retry-test-" + uuid.NewString()[:8], Channel: "test", ExternalID: "1", Messages: []InboundMessage{{Content: "one", ChannelMessageID: "1"}}}
	_, err := srv.runtime.ProcessMessage(context.Background(), req)
	require.Error(t, err)
	out, err := srv.runtime.ProcessMessage(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "recovered", out.Text)
	out, err = srv.runtime.ProcessMessage(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Suppressed)
	require.Equal(t, 2, calls)
}
func TestContextCapabilityAndSkillBoundaries(t *testing.T) {
	conv := Conversation{ID: uuid.New(), AgentName: "test-agent"}
	token, err := issueContextToken([]byte(testRuntimeToken), conv, time.Now().Add(time.Minute))
	require.NoError(t, err)
	claims, err := verifyContextToken([]byte(testRuntimeToken), token, time.Now())
	require.NoError(t, err)
	require.Equal(t, conv.ID, claims.ConversationID)
	_, err = verifyContextToken([]byte(testRuntimeToken), token, time.Now().Add(time.Hour))
	require.Error(t, err)
	_, err = verifyContextToken([]byte(strings.Repeat("x", 32)), token, time.Now())
	require.Error(t, err)
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "test-agent", "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	outside := filepath.Join(root, "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape.md")))
	p := NewFileDomainProvider(root)
	_, err = p.GetDomain(context.Background(), "test-agent", "../outside")
	require.Error(t, err)
	_, err = p.GetDomain(context.Background(), "test-agent", "escape")
	require.Error(t, err)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestA2AContextIDAndCurrentBatch(t *testing.T) {
	client := &httpA2AClient{http: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var body a2aRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "runtime-stable", body.Params.Message.ContextID)
		require.Contains(t, body.Params.Message.Parts[0].Text, "runtime_context")
		require.NotContains(t, body.Params.Message.Parts[0].Text, "Previous conversation")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":{"artifacts":[{"parts":[{"kind":"text","text":"ok"}]}]}}`)), Header: make(http.Header)}, nil
	})}}
	for i := 0; i < 2; i++ {
		reply, err := client.Send(context.Background(), AgentRunRequest{AgentName: "test", ContextID: "runtime-stable", Messages: []Message{{Role: "user", Content: "Hello"}}})
		require.NoError(t, err)
		require.Equal(t, "ok", reply)
	}
}
func TestPauseCRMNeedsActualConfirmation(t *testing.T) {
	for _, body := range []string{`{}`, `{"applied":true,"status":"OTHER"}`, `{"applied":false,"status":"AGENT_PAUSED"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			require.Error(t, NewHTTPPauseCRM(server.URL, "test", "AGENT_PAUSED").SetPaused(context.Background(), Conversation{ID: uuid.New()}, "test"))
		})
	}
}

func TestPGPendingInputBeyondHistoryWindow(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "backlog-"+uuid.NewString()[:8], "test", "1", Actor{})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = store.pool.Exec(ctx, "DELETE FROM conversations WHERE id=$1", conv.ID) })
	for i := 0; i < 25; i++ {
		_, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: fmt.Sprint(i), ChannelMessageID: fmt.Sprint(i)})
		require.NoError(t, err)
	}
	pending, err := store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 25)
	require.Equal(t, "0", pending[0].Content)
	require.NoError(t, store.MarkRuntimeHandled(ctx, pending[:5]))
	pending, err = store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 20)
	require.Equal(t, "5", pending[0].Content)
}

func TestPGHookFailureCannotBeBypassedOnRetry(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.runtime.hooks = testHooks{err: errors.New("upstream outcome unknown")}
	calls := 0
	srv.runtime.a2a = runFunc(func(context.Context, AgentRunRequest) (string, error) { calls++; return "must not run", nil })
	req := MessageRequest{AgentName: "hook-test-" + uuid.NewString()[:8], Channel: "test", ExternalID: "1", Messages: []InboundMessage{{Content: "hello", ChannelMessageID: "1"}}}
	_, err := srv.runtime.ProcessMessage(context.Background(), req)
	require.Error(t, err)
	srv.runtime.hooks = testHooks{}
	out, err := srv.runtime.ProcessMessage(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Suppressed)
	require.Zero(t, calls)
}

func TestA2AReplyDoesNotExposeHistoryOrMetadata(t *testing.T) {
	raw := json.RawMessage(`{"history":[{"text":"private-input"}],"metadata":{"text":"private-tool"},"status":{"message":{"role":"agent","parts":[{"kind":"text","text":"duplicate-status"}]}},"artifacts":[{"parts":[{"kind":"text","text":"public-reply"}]}]}`)
	require.Equal(t, "public-reply", extractText(raw))
	require.Empty(t, extractText(json.RawMessage(`{"role":"user","parts":[{"kind":"text","text":"input"}]}`)))
}
