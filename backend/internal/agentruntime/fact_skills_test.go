package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFactSkills(t *testing.T) {
	require.Empty(t, ParseFactSkills(""))
	require.Equal(t, map[string]string{"amount": "price", "account": "registration"},
		ParseFactSkills(" amount=price, account = registration ,broken,=x,y= "))
}

func writeTestDomains(t *testing.T, root, agent string, domains map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "agents", agent, "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	for name, content := range domains {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0600))
	}
}

func TestPGRuntimeActivatesProceduresMandatoryForRecordedFacts(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	root := t.TempDir()
	price := "price procedure: one question per message"
	writeTestDomains(t, root, conv.AgentName, map[string]string{"price": price, "registration": "registration procedure"})
	source, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "есть 1000"})
	require.NoError(t, err)
	state, err := store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{"amount": {Value: "есть 1000", SourceMessageID: source.ID}}})
	require.NoError(t, err)
	require.Empty(t, state.ActiveSkills, "the model recorded the fact without loading the procedure")
	calls := 0
	model := runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		got := run.ConversationContext.State
		require.Equal(t, price, got.ActiveSkills["price"].Content)
		sum := sha256.Sum256([]byte(price))
		require.Equal(t, hex.EncodeToString(sum[:]), got.ActiveSkills["price"].Digest)
		require.NotContains(t, got.ActiveSkills, "registration", "no account fact recorded yet")
		return "one question", nil
	})
	rt := NewRuntime(store, &noopHooks{}, model, NewFileDomainProvider(root))
	rt.contextKey = []byte(testRuntimeToken)
	rt.factSkills = ParseFactSkills("amount=price,account=registration,deposit=missing")
	for _, id := range []string{"fact-1", "fact-2"} {
		reply, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: conv.AgentName, Channel: conv.Channel, ExternalID: conv.ExternalID, Messages: []InboundMessage{{Content: "Продолжаем", ChannelMessageID: id}}})
		require.NoError(t, err)
		require.Equal(t, "one question", reply.Text)
	}
	require.Equal(t, 2, calls)
	persisted, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, state.Version+1, persisted.Version, "the second turn must not rewrite an already active procedure")
	require.Contains(t, persisted.ActiveSkills, "price")
}

func TestPGRuntimeFactSkillMappedToMissingProcedureDoesNotBlockTheTurn(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	root := t.TempDir()
	writeTestDomains(t, root, conv.AgentName, map[string]string{})
	source, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "пополнил"})
	require.NoError(t, err)
	_, err = store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "пополнил", SourceMessageID: source.ID}}})
	require.NoError(t, err)
	calls := 0
	model := runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		require.Empty(t, run.ConversationContext.State.ActiveSkills)
		return "handled", nil
	})
	rt := NewRuntime(store, &noopHooks{}, model, NewFileDomainProvider(root))
	rt.contextKey = []byte(testRuntimeToken)
	rt.factSkills = ParseFactSkills("deposit=deposit")
	reply, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: conv.AgentName, Channel: conv.Channel, ExternalID: conv.ExternalID, Messages: []InboundMessage{{Content: "Продолжаем", ChannelMessageID: "fact-missing"}}})
	require.NoError(t, err)
	require.Equal(t, "handled", reply.Text)
	require.Equal(t, 1, calls)
}

func TestPGRuntimeUpdateStateReturnsTheProcedureTheFactMakesMandatory(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	root := t.TempDir()
	price := "price procedure: refusal is one sentence"
	writeTestDomains(t, root, conv.AgentName, map[string]string{"price": price})
	source, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "свободно 2000"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	t.Setenv("AGENT_FACT_SKILLS", "amount=price")
	server := httptest.NewServer(NewServer(store.pool, root).Handler())
	defer server.Close()
	endUser := conv.Channel + ":" + conv.ExternalID
	patch := StatePatch{ReportedFacts: map[string]ReportedFact{"amount": {Value: "свободно 2000", SourceMessageID: source.ID}}}
	status, out := postRuntimeIdentity(t, server.URL, "/tools/update-state",
		map[string]any{"context_token": "", "expected_version": 0, "patch": patch}, conv.AgentName, endUser)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["updated"])
	skills := out["state"].(map[string]any)["active_skills"].(map[string]any)
	require.Equal(t, price, skills["price"].(map[string]any)["content"], "the model reads the procedure in the same turn it recorded the fact")
	persisted, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, price, persisted.ActiveSkills["price"].Content)
}
