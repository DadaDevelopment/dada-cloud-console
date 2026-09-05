package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPGRuntimeRefreshesSelectedSkillsAndRetainsConversationState(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	// FileDomainProvider requires an agent name <=63 characters; fixture fits.
	root := t.TempDir()
	dir := filepath.Join(root, "agents", conv.AgentName, "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	old := "discovery version 1: old question"
	current := "discovery version 2: source-defined first question"
	sum := sha256.Sum256([]byte(old))
	state, err := store.ActivateSkill(ctx, conv.ID, "discovery", old, hex.EncodeToString(sum[:]))
	require.NoError(t, err)
	source, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "I have a broker account"})
	require.NoError(t, err)
	state, err = store.ApplyState(ctx, conv.ID, state.Version, StatePatch{ReportedFacts: map[string]ReportedFact{"account": {Value: "I have a broker account", SourceMessageID: source.ID}}, OpenLoops: map[string]OpenLoop{"access": {Question: "Can I join?", Status: "open", SourceMessageID: source.ID}}})
	require.NoError(t, err)
	expectedFacts, expectedLoops := state.ReportedFacts, state.OpenLoops
	version := state.Version
	require.NoError(t, os.WriteFile(filepath.Join(dir, "discovery.md"), []byte(current), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unselected.md"), []byte("must not be activated"), 0600))
	calls := 0
	model := runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		got := run.ConversationContext.State
		require.Equal(t, current, got.ActiveSkills["discovery"].Content)
		digest := sha256.Sum256([]byte(current))
		require.Equal(t, hex.EncodeToString(digest[:]), got.ActiveSkills["discovery"].Digest)
		require.Equal(t, version+1, got.Version)
		require.Equal(t, expectedFacts, got.ReportedFacts)
		require.Equal(t, expectedLoops, got.OpenLoops)
		require.True(t, got.AgentEnabled)
		require.NotContains(t, got.ActiveSkills, "unselected")
		return "current procedure used", nil
	})
	rt := NewRuntime(store, &noopHooks{}, model, NewFileDomainProvider(root))
	rt.contextKey = []byte(testRuntimeToken)
	for _, id := range []string{"refresh-1", "refresh-2"} {
		reply, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: conv.AgentName, Channel: conv.Channel, ExternalID: conv.ExternalID, Messages: []InboundMessage{{Content: "Continue", ChannelMessageID: id}}})
		require.NoError(t, err)
		require.Equal(t, "current procedure used", reply.Text)
	}
	require.Equal(t, 2, calls)
	persisted, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, version+1, persisted.Version, "unchanged content must not write a second state version")
	require.Equal(t, expectedFacts, persisted.ReportedFacts)
	require.Equal(t, expectedLoops, persisted.OpenLoops)
}

func TestPGRuntimeBlocksObsoleteSkillWhenSelectedFileUnavailable(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	old := "old selected procedure"
	sum := sha256.Sum256([]byte(old))
	state, err := store.ActivateSkill(ctx, conv.ID, "discovery", old, hex.EncodeToString(sum[:]))
	require.NoError(t, err)
	root := t.TempDir()
	dir := filepath.Join(root, "agents", conv.AgentName, "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	calls := 0
	model := runFunc(func(context.Context, AgentRunRequest) (string, error) { calls++; return "must not run", nil })
	rt := NewRuntime(store, &noopHooks{}, model, NewFileDomainProvider(root))
	rt.contextKey = []byte(testRuntimeToken)
	req := MessageRequest{AgentName: conv.AgentName, Channel: conv.Channel, ExternalID: conv.ExternalID, Messages: []InboundMessage{{Content: "Continue", ChannelMessageID: "refresh-blocked"}}}
	_, err = rt.ProcessMessage(ctx, req)
	require.ErrorContains(t, err, "refresh active skill discovery")
	require.Zero(t, calls)
	unchanged, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, state, unchanged)
	pending, err := store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "blocked input must remain available for retry")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "discovery.md"), []byte("restored current procedure"), 0600))
	_, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}
