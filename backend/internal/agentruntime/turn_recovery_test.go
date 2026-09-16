package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type recoveryFixture struct {
	srv   *Server
	store *pgStore
	agent string
	mu    sync.Mutex
	sent  []string
	calls int
}

func newRecoveryFixture(t *testing.T, a2a func(calls int, run AgentRunRequest) (string, error)) *recoveryFixture {
	t.Helper()
	store := setupTestStore(t).(*pgStore)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.runtime.hooks = testHooks{}
	f := &recoveryFixture{srv: srv, store: store, agent: "recovery-test-" + uuid.NewString()[:8]}
	srv.runtime.a2a = runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		f.mu.Lock()
		f.calls++
		n := f.calls
		f.mu.Unlock()
		return a2a(n, run)
	})
	srv.runtime.outbound = func(_ context.Context, _, _, text, _ string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, text)
		return nil
	}
	srv.runtime.recoveryDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), `DELETE FROM conversations WHERE agent_name=$1`, f.agent)
		require.NoError(t, err)
	})
	return f
}

func (f *recoveryFixture) request(id, text string) MessageRequest {
	return MessageRequest{AgentName: f.agent, Channel: "test", ExternalID: "1", Messages: []InboundMessage{{Content: text, ChannelMessageID: id}}}
}

func (f *recoveryFixture) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *recoveryFixture) assistantMessages(t *testing.T) []string {
	t.Helper()
	rows, err := f.store.pool.Query(context.Background(),
		`SELECT m.content FROM conversation_messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.agent_name=$1 AND m.role='assistant' ORDER BY m.created_at`, f.agent)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

func TestPGTurnRecovery_ReplaysPendingInputAndDeliversOutbound(t *testing.T) {
	f := newRecoveryFixture(t, func(calls int, run AgentRunRequest) (string, error) {
		require.Len(t, run.Messages, 1)
		require.Equal(t, "one", run.Messages[0].Content)
		if calls == 1 {
			return "", errors.New("a2a task did not complete: failed: Error code: 429")
		}
		return "recovered reply", nil
	})
	_, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "one"))
	require.Error(t, err)
	var failure *turnFailure
	require.True(t, errors.As(err, &failure))

	require.Eventually(t, func() bool { return len(f.delivered()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"recovered reply"}, f.delivered())
	require.Equal(t, []string{"recovered reply"}, f.assistantMessages(t))

	out, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "one"))
	require.NoError(t, err)
	require.True(t, out.Suppressed, "recovered input must be marked handled")
	require.Equal(t, 2, f.calls)
}

func TestPGTurnRecovery_StopsWhenNewerInboundAlreadyAnswered(t *testing.T) {
	f := newRecoveryFixture(t, func(calls int, run AgentRunRequest) (string, error) {
		if calls == 1 {
			return "", errors.New("context deadline exceeded")
		}
		return "answered both", nil
	})
	f.srv.runtime.recoveryDelays = []time.Duration{300 * time.Millisecond}
	_, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "one"))
	require.Error(t, err)

	out, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("2", "two"))
	require.NoError(t, err)
	require.Equal(t, "answered both", out.Text)

	time.Sleep(600 * time.Millisecond)
	require.Empty(t, f.delivered(), "recovery must not send a second reply once the inbox is drained")
	require.Equal(t, 2, f.calls)
	require.Equal(t, []string{"answered both"}, f.assistantMessages(t))
}

func TestPGTurnRecovery_ExhaustsAndLeavesInputPending(t *testing.T) {
	f := newRecoveryFixture(t, func(calls int, run AgentRunRequest) (string, error) {
		return "", errors.New("Error code: 429")
	})
	_, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "one"))
	require.Error(t, err)

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.calls == 3
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, f.delivered())
	require.Equal(t, 3, f.calls)
	f.srv.runtime.recoveryMu.Lock()
	require.False(t, f.srv.runtime.recovering[uuid.Nil], "sanity")
	require.Empty(t, f.srv.runtime.recovering, "exhausted recovery must release the conversation slot")
	f.srv.runtime.recoveryMu.Unlock()
}

func TestPGTurnRecovery_HookFailureIsNotReplayed(t *testing.T) {
	f := newRecoveryFixture(t, func(calls int, run AgentRunRequest) (string, error) {
		return "should not be reached", nil
	})
	f.srv.runtime.hooks = testHooks{err: errors.New("crm down")}
	_, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "one"))
	require.Error(t, err)
	var failure *turnFailure
	require.False(t, errors.As(err, &failure))
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, f.delivered())
	require.Equal(t, 0, f.calls)
}
