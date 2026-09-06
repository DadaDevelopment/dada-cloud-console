package agentruntime

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type pauseFunc func(context.Context, Conversation, string) error

func (f pauseFunc) SetPaused(ctx context.Context, conv Conversation, reason string) error {
	return f(ctx, conv, reason)
}

// The worker scans the entire eligible queue, so isolate these tests from
// other integration fixtures without deleting or changing their rows.
func pauseRetryTestStore(t *testing.T) *pgStore {
	t.Helper()
	base := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	schema := "pause_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := base.pool.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	config := base.pool.Config().Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, err := base.pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	for _, migration := range []string{"148_conversation_state.sql", "150_canonical_message.sql", "151_conversation_runtime_state.sql"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "migrations", migration))
		require.NoError(t, err)
		_, err = pool.Exec(ctx, string(data))
		require.NoError(t, err)
	}
	return NewPGStore(pool).(*pgStore)
}

func TestPGPauseRetryAfterServerRestart(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	first := NewServer(store.pool, t.TempDir())
	calls := 0
	first.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error {
		calls++
		return errors.New("CRM temporarily unavailable")
	})
	httpServer := httptest.NewServer(first.Handler())
	token, err := issueContextToken([]byte(testRuntimeToken), conv, time.Now().Add(time.Minute))
	require.NoError(t, err)
	status, result := postRuntime(t, httpServer.URL, "/tools/stop-agent", map[string]any{"context_token": token, "reason": "client declined"}, testRuntimeToken)
	httpServer.Close()
	require.Equal(t, 200, status)
	require.Equal(t, false, result["agent_enabled"])
	require.Equal(t, "failed", result["crm_status_sync"])
	require.Equal(t, 1, calls)
	// Reconstruct all in-process runtime state. Recovery needs only PostgreSQL,
	// and neither an incoming user message nor a model callback.
	restarted := NewServer(store.pool, t.TempDir())
	restarted.runtime.a2a = runFunc(func(context.Context, AgentRunRequest) (string, error) {
		t.Fatal("CRM recovery must not invoke the model")
		return "", nil
	})
	restarted.pauseCRM = pauseFunc(func(_ context.Context, got Conversation, reason string) error {
		calls++
		require.Equal(t, conv.ID, got.ID)
		require.Equal(t, "client declined", reason)
		state, err := store.GetState(ctx, conv.ID)
		require.NoError(t, err)
		require.False(t, state.AgentEnabled)
		return nil
	})
	n, err := restarted.ReconcilePaused(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	state, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "completed", state.CRMStatusSync)
	n, err = restarted.ReconcilePaused(ctx, 5)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, calls, "completed state must never retry CRM")
}

func TestPGPauseRetryFairnessAndCancellation(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	first := stateTestConversation(t, store)
	second := stateTestConversation(t, store)
	active := stateTestConversation(t, store)
	for i, conv := range []Conversation{first, second} {
		_, err := store.PauseAgent(ctx, conv.ID, "pause")
		require.NoError(t, err)
		_, err = store.MarkPauseCRMSync(ctx, conv.ID, "failed")
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, `UPDATE conversation_runtime_state SET updated_at=$2 WHERE conversation_id=$1`, conv.ID, time.Unix(int64(i+1), 0))
		require.NoError(t, err)
	}
	_, err := store.GetState(ctx, active.ID)
	require.NoError(t, err)
	srv := NewServer(store.pool, t.TempDir())
	var called []uuid.UUID
	srv.pauseCRM = pauseFunc(func(_ context.Context, conv Conversation, _ string) error {
		called = append(called, conv.ID)
		return errors.New("still unavailable")
	})
	_, err = srv.ReconcilePaused(ctx, 1)
	require.NoError(t, err)
	_, err = srv.ReconcilePaused(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{first.ID, second.ID}, called, "repeated failed status must advance retry order")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = srv.ReconcilePaused(cancelled, 5)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, called, 2)
	state, err := store.GetState(ctx, active.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled, "active conversations are outside retry scope")
}

func TestPauseSyncLockWaitHonorsCancellation(t *testing.T) {
	srv := &Server{}
	conv := Conversation{ID: uuid.New()}
	lock := &srv.pauseSyncLocks[int(conv.ID[0])%len(srv.pauseSyncLocks)]
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := srv.syncPausedCRM(ctx, conv)
	require.ErrorIs(t, err, context.Canceled)
}
