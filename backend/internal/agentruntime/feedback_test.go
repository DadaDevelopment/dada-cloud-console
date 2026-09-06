package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCourtesyCounterexamples(t *testing.T) {
	history := []Message{{Role: "assistant", Content: "Средства находятся на вашем брокерском счёте."}}
	for _, text := range []string{"Спасибо!", "Ок, спасибо", "👍"} {
		require.True(t, courtesyOnly([]Message{{Content: text}}, history, RuntimeState{}))
	}
	for _, text := range []string{"Спасибо, а сколько стоит?", "Готово", "Хорошо", "Спасибо?", "👍 сколько?"} {
		require.False(t, courtesyOnly([]Message{{Content: text}}, history, RuntimeState{}))
	}
	require.False(t, courtesyOnly([]Message{{Content: "Спасибо"}, {Content: "Как зарегистрироваться?"}}, history, RuntimeState{}))
	require.False(t, courtesyOnly([]Message{{Content: "Спасибо"}}, []Message{{Role: "assistant", Content: "Продолжим?"}}, RuntimeState{}))
	require.False(t, courtesyOnly([]Message{{Content: "Спасибо"}}, history, RuntimeState{OpenLoops: map[string]OpenLoop{"a": {Status: "open"}}}))
	require.False(t, courtesyOnly([]Message{{Content: "Спасибо", Attachments: []any{map[string]any{"kind": "image"}}}}, history, RuntimeState{}))
}
func TestPGCourtesyReceiptsAndResume(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	presence := 0
	rt := NewRuntime(store, testHooks{}, runFunc(func(context.Context, AgentRunRequest) (string, error) {
		calls++
		return "Ответ на вопрос.", nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	req := MessageRequest{AgentName: "courtesy-" + uuid.NewString(), Channel: "telegram", ExternalID: "123", OnProcessing: func() { presence++ }}
	rt.courtesyAgents = map[string]bool{req.AgentName: true}
	call := func(id, text string) MessageResponse {
		req.Messages = []InboundMessage{{Content: text, ChannelMessageID: id}}
		out, err := rt.ProcessMessage(ctx, req)
		require.NoError(t, err)
		return out
	}
	require.False(t, call("1", "Как устроена группа?").Suppressed)
	require.True(t, call("2", "Спасибо!").Suppressed)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, presence)
	require.True(t, call("2", "Спасибо!").Suppressed)
	require.Equal(t, 1, calls)
	require.False(t, call("3", "А сколько стоит?").Suppressed)
	require.Equal(t, 2, calls)
	require.Equal(t, 2, presence)
	conv, _, err := store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, Actor{})
	require.NoError(t, err)
	state, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
	pending, err := store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Empty(t, pending)
	_, err = store.PauseAgent(ctx, conv.ID, "stop")
	require.NoError(t, err)
	require.True(t, call("4", "Ещё вопрос").Suppressed)
	require.Equal(t, 2, presence)
}
func TestPGContactFailureRetryAndReplay(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "crm-contact-" + uuid.NewString()
	var fail atomic.Bool
	fail.Store(true)
	var calls atomic.Int32
	pid := uuid.NewString()
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "Bearer "+testRuntimeToken, r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Alice \"quoted\"", body["first_name"])
		if fail.Load() {
			http.Error(w, "unavailable", 502)
			return
		}
		fmt.Fprintf(w, `{"applied":true,"person_id":%q}`, pid)
	}))
	defer crm.Close()
	rt := NewRuntime(store, testHooks{}, runFunc(func(context.Context, AgentRunRequest) (string, error) { return "Привет!", nil }), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.contacts = &ContactSync{store: store, endpoint: crm.URL, token: testRuntimeToken, agent: agent, client: crm.Client()}
	srv := &Server{runtime: rt}
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "4242", Actor: Actor{Username: "alice", Metadata: map[string]any{"first_name": "Alice \"quoted\""}}, Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}}
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.False(t, out.Suppressed)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "4242", req.Actor)
	require.NoError(t, err)
	receipt := conv.Metadata["crm_contact_sync"].(map[string]any)
	require.Equal(t, "failed", receipt["status"])
	fail.Store(false)
	n, err := srv.ReconcileContacts(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	conv, err = store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	receipt = conv.Metadata["crm_contact_sync"].(map[string]any)
	require.Equal(t, "completed", receipt["status"])
	require.Equal(t, pid, receipt["person_id"])
	out, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.True(t, out.Suppressed)
	require.EqualValues(t, 2, calls.Load())
	n, err = srv.ReconcileContacts(ctx, 5)
	require.NoError(t, err)
	require.Zero(t, n)
}
