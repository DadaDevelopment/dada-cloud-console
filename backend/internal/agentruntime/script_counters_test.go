package agentruntime

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// metadataCounts reads a map[string]int out of the JSONB.
func metadataCounts(conv Conversation, key string) map[string]int {
	raw, _ := conv.Metadata[key].(map[string]any)
	out := make(map[string]int, len(raw))
	for k, v := range raw {
		if n, ok := v.(float64); ok && n > 0 {
			out[k] = int(n)
		}
	}
	return out
}

// The same input (attempt 1, a recovery replay) counts once per key; two
// reasons on one input both count; a broken value starts over.
func TestPGRecordRefusal_IncrementsAndDedupsPerKey(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "counters-"+uuid.NewString()[:8], "telegram", "1", Actor{ExternalID: "1"})
	require.NoError(t, err)

	n, err := store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"b", "a"})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "the same input is not a second refusal")
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_distrust", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "another reason on the same input counts")
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"a", "b", "x"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "a replay with one more client message is the same refusal")
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"c"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"c", "y"})
	require.NoError(t, err)
	require.Equal(t, 2, n, "the ids of every counted refusal are kept, not only the last")
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"b"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_time", nil)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_time", nil)
	require.NoError(t, err)
	require.Equal(t, 2, n, "without message_ids there is nothing to dedup on")

	fresh, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"refusal_money": 2, "refusal_distrust": 1, "refusal_time": 2}, metadataCounts(fresh, obstacleRefusalsKey))

	_, err = store.pool.Exec(ctx, `UPDATE conversations SET metadata = jsonb_set(metadata, '{obstacle_refusals}', '"broken"') WHERE id = $1`, conv.ID)
	require.NoError(t, err)
	n, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"d"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "a broken value starts over")

	_, err = store.RecordRefusal(ctx, uuid.New(), "refusal_money", nil)
	require.Error(t, err)
	_, err = store.RecordRefusal(ctx, conv.ID, " ", nil)
	require.Error(t, err)
}

func TestPGResetRefusalsAfterPause_OnlyWithTheMark(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "counters-"+uuid.NewString()[:8], "telegram", "1", Actor{ExternalID: "1"})
	require.NoError(t, err)
	_, err = store.RecordRefusal(ctx, conv.ID, "refusal_money", []string{"a"})
	require.NoError(t, err)

	reset, err := store.ResetRefusalsAfterPause(ctx, conv.ID)
	require.NoError(t, err)
	require.False(t, reset, "no pause, nothing to reset")
	require.NoError(t, store.MarkRefusalsPaused(ctx, conv.ID))
	reset, err = store.ResetRefusalsAfterPause(ctx, conv.ID)
	require.NoError(t, err)
	require.True(t, reset)
	fresh, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	for _, key := range []string{obstacleRefusalsKey, obstacleRefusalMsgsKey, refusalsPausedKey} {
		require.NotContains(t, fresh.Metadata, key)
	}
}

func TestRuntimeFlags_PrecheckDefaultsAndStartupCheck(t *testing.T) {
	f := runtimeFlagsFromEnv()
	require.Equal(t, precheckOff, f.Precheck)
	require.False(t, f.ScriptCounters)
	require.Equal(t, 2, f.RefusalHandoffAt)
	require.False(t, f.HandoffTriggers)
	require.NoError(t, ValidateFlagsFromEnv())

	t.Setenv("AGENT_RUNTIME_PRECHECK", "sometimes")
	require.Equal(t, precheckOff, runtimeFlagsFromEnv().Precheck, "an unknown mode keeps the safe default")

	t.Setenv("AGENT_RUNTIME_PRECHECK", "LOG")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "AGENT_JUDGE_LLM_", "a check without its model does not start")
	t.Setenv("AGENT_JUDGE_LLM_URL", "http://judge")
	t.Setenv("AGENT_JUDGE_LLM_KEY", "k")
	t.Setenv("AGENT_JUDGE_LLM_MODEL", "m")
	require.NoError(t, ValidateFlagsFromEnv())

	t.Setenv("AGENT_RUNTIME_SCRIPT_COUNTERS", "on")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "AGENT_RUNTIME_PRECHECK=block", "counters under log would hand off one turn late")
	t.Setenv("AGENT_RUNTIME_PRECHECK", "block")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "AGENT_RUNTIME_HANDOFF_TRIGGERS", "block without the triggers does not pause on E_LEGAL_TAX")
	t.Setenv("AGENT_RUNTIME_HANDOFF_TRIGGERS", "on")
	require.NoError(t, ValidateFlagsFromEnv())
	t.Setenv("AGENT_RUNTIME_SCRIPT_COUNTERS", "")
	require.NoError(t, ValidateFlagsFromEnv())
}
