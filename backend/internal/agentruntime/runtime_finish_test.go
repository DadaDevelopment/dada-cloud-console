package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestPGRuntimeFinishForgetsConversation proves the platform-level reset end to
// end against Postgres: /finish retires the live conversation without deleting
// history, it works while the agent is paused (so a pause is no longer a
// one-way door for the user), and the next inbound message opens a new
// conversation with a new agent context id and clean runtime state.
func TestPGRuntimeFinishForgetsConversation(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "finish-test-" + uuid.NewString()[:8]
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	t.Cleanup(func() {
		_, err := store.pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name=$1`, agent)
		require.NoError(t, err)
	})

	srv := NewServer(store.pool, t.TempDir())
	srv.runtime.hooks = testHooks{}
	var contexts []string
	var seenState []RuntimeState
	srv.runtime.a2a = runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		contexts = append(contexts, run.ContextID)
		seenState = append(seenState, run.ConversationContext.State)
		return "answer", nil
	})
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	request := func(id, text string) map[string]any {
		return map[string]any{"agent_name": agent, "channel": "telegram", "external_id": "finish-chat",
			"actor":    map[string]any{"external_id": "finish-user"},
			"messages": []map[string]any{{"content": text, "channel_message_id": id}}}
	}

	code, out := postRuntime(t, httpSrv.URL, "/message", request("1", "привет"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, "answer", out["text"])
	require.Len(t, contexts, 1)
	first := uuid.MustParse(contexts[0][len("runtime-"):])

	_, err := store.PauseAgent(ctx, first, "operator handover")
	require.NoError(t, err)
	code, out = postRuntime(t, httpSrv.URL, "/message", request("2", "ещё вопрос"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, true, out["suppressed"], "a paused conversation stays silent")

	code, out = postRuntime(t, httpSrv.URL, "/message", request("3", "/finish"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, finishAcknowledgement, out["text"], "the reset must answer even while paused")
	require.NotEqual(t, true, out["suppressed"])
	require.Len(t, contexts, 1, "the reset never reaches the model")

	var status string
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, first).Scan(&status))
	require.Equal(t, "finished", status)
	var kept int
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE conversation_id=$1`, first).Scan(&kept))
	require.Greater(t, kept, 0, "history is archived, not deleted")

	code, out = postRuntime(t, httpSrv.URL, "/message", request("4", "здравствуйте"), testRuntimeToken)
	require.Equal(t, 200, code)
	require.Equal(t, "answer", out["text"])
	require.Len(t, contexts, 2)
	second := uuid.MustParse(contexts[1][len("runtime-"):])
	require.NotEqual(t, first, second, "the user talks to a brand new conversation")

	state, err := store.GetState(ctx, second)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled, "the pause did not survive the reset")
	require.Empty(t, state.ReportedFacts)
	require.Empty(t, state.OpenLoops)
	require.Empty(t, seenState[1].ReportedFacts)

	var carried int
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE conversation_id=$1 AND role='user'`, second).Scan(&carried))
	require.Equal(t, 1, carried, "no earlier message leaks into the fresh conversation")
}
