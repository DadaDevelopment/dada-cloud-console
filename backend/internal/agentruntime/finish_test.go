package agentruntime

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type recordingA2A struct {
	calls   int
	history [][]Message
}

func (a *recordingA2A) Send(_ context.Context, _ string, messages []Message) (string, error) {
	a.calls++
	a.history = append(a.history, messages)
	return "answer", nil
}

// TestFinishForgetsConversation proves the platform-level reset against
// Postgres: /finish retires the live conversation without deleting its history,
// answers without consulting the agent, and leaves the next inbound message in
// a brand new conversation whose history the agent sees as empty.
func TestFinishForgetsConversation(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	agent := "finish-test-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		pool, err := poolOf(store)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name=$1`, agent)
		require.NoError(t, err)
	})

	model := &recordingA2A{}
	runtime := NewRuntime(store, &noopHooks{}, model, nil)
	send := func(id, text string) MessageResponse {
		t.Helper()
		res, err := runtime.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "finish-chat",
			Actor:    Actor{ExternalID: "finish-user"},
			Messages: []InboundMessage{{Content: text, ChannelMessageID: id}}})
		require.NoError(t, err)
		return res
	}

	require.Equal(t, "answer", send("1", "привет").Text)
	require.Equal(t, "answer", send("2", "у меня вопрос про депозит").Text)
	require.Equal(t, 2, model.calls)
	require.Len(t, model.history[1], 3, "the agent carries the earlier turns while the conversation lives")

	first, err := liveConversation(ctx, store, agent)
	require.NoError(t, err)

	res := send("3", "/finish")
	require.Equal(t, finishAcknowledgement, res.Text)
	require.Equal(t, "3", res.ReplyToChannelMessageID)
	require.Equal(t, 2, model.calls, "the reset never reaches the model")

	pool, err := poolOf(store)
	require.NoError(t, err)
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, first).Scan(&status))
	require.Equal(t, "finished", status)
	var kept int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE conversation_id=$1`, first).Scan(&kept))
	require.Greater(t, kept, 0, "history is archived, not deleted")

	require.Equal(t, "answer", send("4", "здравствуйте").Text)
	require.Equal(t, 3, model.calls)
	second, err := liveConversation(ctx, store, agent)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "the user talks to a brand new conversation")
	require.Len(t, model.history[2], 1, "no earlier turn survives the reset")
	require.Equal(t, "здравствуйте", model.history[2][0].Content)
}

func TestFinishCommandMatchesWholeMessageOnly(t *testing.T) {
	for _, text := range []string{"/finish", " /FINISH ", "/finish@hello_tradin_bot"} {
		require.True(t, finishCommand([]InboundMessage{{Content: text}}), text)
	}
	for _, text := range []string{"напиши /finish чтобы сбросить", "/finished", "/finish сейчас", "finish"} {
		require.False(t, finishCommand([]InboundMessage{{Content: text}}), text)
	}
}

func poolOf(store ConversationStore) (*pgxpool.Pool, error) {
	s, ok := store.(*pgStore)
	if !ok {
		return nil, fmt.Errorf("test requires the postgres store, got %T", store)
	}
	return s.pool, nil
}

func liveConversation(ctx context.Context, store ConversationStore, agent string) (uuid.UUID, error) {
	pool, err := poolOf(store)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM conversations WHERE agent_name=$1 AND status='active'`, agent).Scan(&id)
	return id, err
}
