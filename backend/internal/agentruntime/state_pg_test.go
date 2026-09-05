package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func stateTestConversation(t *testing.T, store *pgStore) Conversation {
	t.Helper()
	conv, _, err := store.GetOrCreateConversation(context.Background(), "state-test-"+uuid.NewString(), "test", uuid.NewString(), Actor{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), `DELETE FROM conversations WHERE id=$1`, conv.ID)
		require.NoError(t, err)
	})
	return conv
}
func TestPGStateEvidenceCASAndPause(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	other := stateTestConversation(t, store)
	user, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "I deposited"})
	require.NoError(t, err)
	assistant, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: "ok"})
	require.NoError(t, err)
	foreign, err := store.SaveMessage(ctx, other.ID, SaveMessageInput{Role: "user", Content: "foreign"})
	require.NoError(t, err)
	state, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
	require.Zero(t, state.Version)
	for _, id := range []uuid.UUID{assistant.ID, foreign.ID, uuid.New()} {
		_, err = store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "reported", SourceMessageID: id}}})
		require.ErrorIs(t, err, ErrInvalidStateEvidence)
	}
	state, err = store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "reported", SourceMessageID: user.ID}}})
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Version)
	_, err = store.ApplyState(ctx, conv.ID, 0, StatePatch{OpenLoops: map[string]OpenLoop{"access": {Question: "when", SourceMessageID: user.ID, Status: "open"}}})
	require.ErrorIs(t, err, ErrStateConflict)
	state, err = store.ApplyState(ctx, conv.ID, 1, StatePatch{OpenLoops: map[string]OpenLoop{"access": {Question: "when", SourceMessageID: user.ID, Status: "open"}}})
	require.NoError(t, err)
	require.Equal(t, "reported", state.ReportedFacts["deposit"].Value)
	_, err = store.MarkPauseCRMSync(ctx, conv.ID, "completed")
	require.ErrorIs(t, err, ErrInvalidStatePatch)
	state, err = store.PauseAgent(ctx, conv.ID, "human review")
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "pending", state.CRMStatusSync)
	version := state.Version
	state, err = store.PauseAgent(ctx, conv.ID, "retry")
	require.NoError(t, err)
	require.Equal(t, version, state.Version)
	require.Equal(t, "human review", state.PauseReason)
	state, err = store.ApplyState(ctx, conv.ID, state.Version, StatePatch{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "still only reported", SourceMessageID: user.ID}}})
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	state, err = store.MarkPauseCRMSync(ctx, conv.ID, "failed")
	require.NoError(t, err)
	require.Equal(t, "failed", state.CRMStatusSync)
	state, err = store.MarkPauseCRMSync(ctx, conv.ID, "completed")
	require.NoError(t, err)
	version = state.Version
	state, err = store.MarkPauseCRMSync(ctx, conv.ID, "failed")
	require.NoError(t, err)
	require.Equal(t, "completed", state.CRMStatusSync)
	require.Equal(t, version, state.Version)
	state, err = store.PauseAgent(ctx, conv.ID, "late retry")
	require.NoError(t, err)
	require.Equal(t, "completed", state.CRMStatusSync)
	reloaded, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, state, reloaded)
	_, err = store.GetState(ctx, uuid.New())
	require.ErrorIs(t, err, ErrConversationNotFound)
}
func TestPGStateConcurrentCASAndSkillPersistence(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	user, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "hello"})
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{key: {Value: key, SourceMessageID: user.ID}}})
			results <- err
		}(key)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrStateConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	content := "Short trusted skill"
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	state, err := store.ActivateSkill(ctx, conv.ID, "registration", content, digest)
	require.NoError(t, err)
	require.Len(t, state.ReportedFacts, 1)
	version := state.Version
	state, err = store.ActivateSkill(ctx, conv.ID, "registration", content, digest)
	require.NoError(t, err)
	require.Equal(t, version, state.Version)
	loaded, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, ActiveSkill{Content: content, Digest: digest}, loaded.ActiveSkills["registration"])
}
