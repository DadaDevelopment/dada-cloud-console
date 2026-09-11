package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type stateCRMStub struct {
	t      *testing.T
	fail   atomic.Bool
	mu     sync.Mutex
	bodies []map[string]any
}

func (c *stateCRMStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	require.Equal(c.t, http.MethodPut, r.Method)
	require.Equal(c.t, "Bearer "+testRuntimeToken, r.Header.Get("Authorization"))
	var body map[string]any
	require.NoError(c.t, json.NewDecoder(r.Body).Decode(&body))
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	if c.fail.Load() {
		w.WriteHeader(502)
		fmt.Fprint(w, `{"applied":false,"error":"crm_note_not_confirmed"}`)
		return
	}
	fmt.Fprintf(w, `{"applied":true,"note_id":%q,"version":%v}`, uuid.NewString(), body["state"].(map[string]any)["version"])
}

func (c *stateCRMStub) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *stateCRMStub) last() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies[len(c.bodies)-1]
}

func stateReceiptOf(t *testing.T, store *pgStore, id uuid.UUID) map[string]any {
	t.Helper()
	conv, err := store.GetConversation(context.Background(), id)
	require.NoError(t, err)
	receipt, _ := conv.Metadata["crm_state_sync"].(map[string]any)
	return receipt
}

func TestPGStateSyncSkipsConfirmedVersionsAndKeepsSummary(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "crm-state-" + uuid.NewString()
	stub := &stateCRMStub{t: t}
	crm := httptest.NewServer(stub)
	defer crm.Close()
	syncer := &StateSync{store: store, endpoint: crm.URL, token: testRuntimeToken, agent: agent, client: crm.Client()}
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{Username: "alice"})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = store.pool.Exec(context.Background(), `DELETE FROM conversations WHERE id=$1`, conv.ID) })
	user, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "торгую год"})
	require.NoError(t, err)
	state, err := store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{"experience": {Value: "торгую год", SourceMessageID: user.ID}}})
	require.NoError(t, err)

	require.NoError(t, syncer.Push(ctx, conv, state, ""))
	require.Equal(t, 1, stub.calls())
	body := stub.last()
	require.Equal(t, conv.ID.String(), body["conversation_id"])
	require.Equal(t, "4242", body["external_id"])
	require.Equal(t, "alice", body["username"])
	sent := body["state"].(map[string]any)
	require.Equal(t, float64(state.Version), sent["version"])
	require.Equal(t, true, sent["agent_enabled"])
	require.Equal(t, "", sent["pause_reason"])
	require.Equal(t, map[string]any{}, sent["open_loops"])
	require.Equal(t, "торгую год", sent["reported_facts"].(map[string]any)["experience"].(map[string]any)["value"])
	_, err = time.Parse(time.RFC3339, body["updated_at"].(string))
	require.NoError(t, err)
	receipt := stateReceiptOf(t, store, conv.ID)
	require.Equal(t, "completed", receipt["status"])
	require.Equal(t, float64(state.Version), receipt["version"])

	require.NoError(t, syncer.Push(ctx, conv, state, ""))
	require.Equal(t, 1, stub.calls())

	require.NoError(t, syncer.Push(ctx, conv, state, "Клиент не верит, просит доказательств"))
	require.Equal(t, 2, stub.calls())
	require.Equal(t, "Клиент не верит, просит доказательств", stub.last()["summary"])
	require.NoError(t, syncer.Push(ctx, conv, state, ""))
	require.Equal(t, 2, stub.calls())

	paused, err := store.PauseAgent(ctx, conv.ID, "escalated: E_DISTRUST")
	require.NoError(t, err)
	require.NoError(t, syncer.Push(ctx, conv, paused, ""))
	require.Equal(t, 3, stub.calls())
	require.Equal(t, "Клиент не верит, просит доказательств", stub.last()["summary"])
	require.Equal(t, "escalated: E_DISTRUST", stub.last()["state"].(map[string]any)["pause_reason"])
	require.Equal(t, false, stub.last()["state"].(map[string]any)["agent_enabled"])

	stub.fail.Store(true)
	newer, err := store.ApplyState(ctx, conv.ID, paused.Version, StatePatch{OpenLoops: map[string]OpenLoop{"account_link": {Question: "По ссылке?", SourceMessageID: user.ID, Status: "open"}}})
	require.NoError(t, err)
	require.NoError(t, syncer.Push(ctx, conv, newer, ""))
	require.Equal(t, 4, stub.calls())
	receipt = stateReceiptOf(t, store, conv.ID)
	require.Equal(t, "failed", receipt["status"])
	require.Equal(t, float64(newer.Version), receipt["version"])
	stub.fail.Store(false)
	require.NoError(t, syncer.Push(ctx, conv, newer, ""))
	require.Equal(t, 5, stub.calls())
	require.Equal(t, "completed", stateReceiptOf(t, store, conv.ID)["status"])

	other := Conversation{ID: conv.ID, AgentName: "someone-else", Channel: "telegram"}
	require.NoError(t, syncer.Push(ctx, other, newer, ""))
	require.Equal(t, 5, stub.calls())
}

func TestPGProcessMessageMirrorsStateAfterTurn(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "crm-state-turn-" + uuid.NewString()
	stub := &stateCRMStub{t: t}
	crm := httptest.NewServer(stub)
	defer crm.Close()
	rt := NewRuntime(store, testHooks{}, runFunc(func(context.Context, AgentRunRequest) (string, error) { return "Привет!", nil }), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.stateSync = &StateSync{store: store, endpoint: crm.URL, token: testRuntimeToken, agent: agent, client: crm.Client()}
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "4343", Actor: Actor{Username: "bob"}, Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}}
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "Привет!", out.Text)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "4343", req.Actor)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = store.pool.Exec(context.Background(), `DELETE FROM conversations WHERE id=$1`, conv.ID) })
	require.Eventually(t, func() bool { return stateReceiptOf(t, store, conv.ID)["status"] == "completed" }, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, 1, stub.calls())
	require.Equal(t, "bob", stub.last()["username"])
	require.Equal(t, true, stub.last()["state"].(map[string]any)["agent_enabled"])
}
