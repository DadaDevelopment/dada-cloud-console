package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func testGuidedPlaybook() *GuidedPlaybook {
	return &GuidedPlaybook{Version: "test-v1", AgentName: "test-guided", MaxReplyChars: 400,
		Qualification: []GuidedSlot{{"experience", "experience"}, {"target", "target"}},
		Routes:        []GuidedRoute{{ID: "offer_account", MissingFacts: []string{"account"}, CardIDs: []string{"offer", "account"}}, {ID: "account_next", RequiredFacts: []string{"account"}, CardIDs: []string{"account_next"}}},
		AnswerCardIDs: []string{"cost", "account_next"}, Cards: []GuidedCard{
			{"experience", "Ask experience", []string{"У вас есть опыт торговли?"}, []string{"private research"}},
			{"target", "Ask target", []string{"Какой доход вас интересует?"}, []string{"private research"}},
			{"offer", "Offer summary", []string{"В группе есть обучение и разборы."}, []string{"private research"}},
			{"account", "Ask account", []string{"У вас уже есть счёт?"}, []string{"private research"}},
			{"cost", "Direct cost question", []string{"Деньги остаются на вашем счёте."}, []string{"private research"}},
			{"account_next", "Clarify account", []string{"В каком разделе кабинета вы сейчас?"}, []string{"private research"}},
		}}
}
func TestGuidedUpdatedFactsAndDirectAnswer(t *testing.T) {
	p := testGuidedPlaybook()
	require.NoError(t, p.validate())
	state := RuntimeState{ReportedFacts: map[string]ReportedFact{}}
	out, err := p.render(`{"kind":"advance","playbook_version":"test-v1"}`, state)
	require.NoError(t, err)
	require.Equal(t, "У вас есть опыт торговли?", out)
	state.ReportedFacts["experience"] = ReportedFact{Value: "новичок"}
	state.ReportedFacts["target"] = ReportedFact{Value: "2000"}
	out, err = p.render(`{"kind":"advance","playbook_version":"test-v1"}`, state)
	require.NoError(t, err)
	require.Equal(t, "В группе есть обучение и разборы.\n\nУ вас уже есть счёт?", out)
	out, err = p.render(`{"kind":"answer","playbook_version":"test-v1","card_ids":["cost"]}`, state)
	require.NoError(t, err)
	require.Equal(t, "Деньги остаются на вашем счёте.", out)
	state.ReportedFacts["account"] = ReportedFact{Value: "уже есть"}
	step, _ := p.next(state)
	require.Equal(t, "account_next", step)
	data, err := json.Marshal(p.context(state, nil))
	require.NoError(t, err)
	require.NotContains(t, string(data), "private research")
	require.NotContains(t, string(data), "source_refs")
}
func TestGuidedRejectsUntrustedReply(t *testing.T) {
	p := testGuidedPlaybook()
	for _, raw := range []string{
		`{"kind":"answer","playbook_version":"old","card_ids":["cost"]}`,
		`{"kind":"answer","playbook_version":"test-v1","card_ids":["invented"]}`,
		`{"kind":"answer","playbook_version":"test-v1","card_ids":["experience"]}`,
		`{"kind":"answer","playbook_version":"test-v1","card_ids":["cost","cost"]}`,
		`{"kind":"answer","playbook_version":"test-v1","card_ids":["cost"],"paragraphs":["3000 USD"]}`,
		`{"kind":"advance","playbook_version":"test-v1","card_ids":["cost"]}`,
		`{"kind":"qualification","playbook_version":"test-v1"}`,
		`{"kind":"advance","playbook_version":"test-v1"} trailing`,
	} {
		_, err := p.render(raw, RuntimeState{})
		require.Error(t, err, raw)
	}
}
func TestGuidedRejectsInvalidConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*GuidedPlaybook){
		"route reference":     func(p *GuidedPlaybook) { p.Routes[0].CardIDs = []string{"missing"} },
		"answer reference":    func(p *GuidedPlaybook) { p.AnswerCardIDs = []string{"missing"} },
		"oversize":            func(p *GuidedPlaybook) { p.Cards[0].Paragraphs = []string{strings.Repeat("я", 401)} },
		"combined oversize":   func(p *GuidedPlaybook) { p.Cards[2].Paragraphs = []string{strings.Repeat("я", 390)} },
		"two questions":       func(p *GuidedPlaybook) { p.Cards[2].Paragraphs = []string{"Первый?"} },
		"contradictory route": func(p *GuidedPlaybook) { p.Routes[0].RequiredFacts = []string{"account"} },
	} {
		t.Run(name, func(t *testing.T) { p := testGuidedPlaybook(); mutate(p); require.Error(t, p.validate()) })
	}
}
func TestGuidedExactScopeAndInlineConfig(t *testing.T) {
	p := testGuidedPlaybook()
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	t.Setenv("AGENT_GUIDED_PLAYBOOK_JSON", string(raw))
	t.Setenv("AGENT_GUIDED_PLAYBOOK_PATH", "")
	t.Setenv("AGENT_GUIDED_CHAT_IDS", "-900123")
	rt := &Runtime{guided: guidedConfigFromEnv()}
	require.NoError(t, rt.guided.err)
	for _, c := range []Conversation{{AgentName: p.AgentName, Channel: "telegram", ExternalID: "-9001234"}, {AgentName: "other", Channel: "telegram", ExternalID: "-900123"}, {AgentName: p.AgentName, Channel: "web", ExternalID: "-900123"}} {
		got, err := rt.guidedFor(c)
		require.NoError(t, err)
		require.Nil(t, got)
	}
	conv := Conversation{AgentName: p.AgentName, Channel: "telegram", ExternalID: "-900123"}
	got, err := rt.guidedFor(conv)
	require.NoError(t, err)
	require.NotNil(t, got)
	t.Setenv("AGENT_GUIDED_PLAYBOOK_PATH", "also-set")
	rt.guided = guidedConfigFromEnv()
	_, err = rt.guidedFor(conv)
	require.Error(t, err)
	conv.ExternalID = "customer"
	got, err = rt.guidedFor(conv)
	require.NoError(t, err)
	require.Nil(t, got)
	t.Setenv("AGENT_GUIDED_CHAT_IDS", "*")
	require.Error(t, guidedConfigFromEnv().err)
}
func TestPGGuidedRepairAndScope(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	p := testGuidedPlaybook()
	p.AgentName = "guided-" + uuid.NewString()
	calls := 0
	var contextID string
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		if run.ConversationContext.ExternalID != "-900123" {
			require.Nil(t, run.ConversationContext.GuidedPlaybook)
			require.Empty(t, run.ConversationContext.ReplyFormat)
			return "Existing customer path", nil
		}
		require.Equal(t, guidedReplyFormat, run.ConversationContext.ReplyFormat)
		require.NotNil(t, run.ConversationContext.GuidedPlaybook)
		if calls == 1 {
			contextID = run.ContextID
			require.Empty(t, run.ConversationContext.ReplyError)
			return "Forbidden generated draft", nil
		}
		require.Equal(t, contextID, run.ContextID)
		require.NotEmpty(t, run.ConversationContext.ReplyError)
		return `{"choice":{"card_ids":[],"kind":"advance","playbook_version":"test-v1"}}`, nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.guided = &guidedConfig{playbook: p, chats: map[string]bool{"-900123": true}}
	req := MessageRequest{AgentName: p.AgentName, Channel: "telegram", ExternalID: "-900123", Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}}
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "У вас есть опыт торговли?", out.Text)
	conv, _, err := store.GetOrCreateConversation(ctx, p.AgentName, "telegram", "-900123", Actor{})
	require.NoError(t, err)
	history, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, out.Text, history[1].Content)
	req.ExternalID = "-9001234"
	out, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "Existing customer path", out.Text)
}

func TestPGGuidedUsesFactsSavedDuringNativeCall(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	p := testGuidedPlaybook()
	p.AgentName = "guided-" + uuid.NewString()
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		require.Equal(t, "qualification_experience", run.ConversationContext.GuidedPlaybook.NextStep)
		id, err := uuid.Parse(run.ConversationContext.ConversationID)
		require.NoError(t, err)
		state, err := store.ApplyState(ctx, id, run.ConversationContext.State.Version, StatePatch{ReportedFacts: map[string]ReportedFact{
			"experience": {Value: "новичок", SourceMessageID: run.Messages[0].ID},
			"target":     {Value: "2000", SourceMessageID: run.Messages[0].ID},
			"account":    {Value: "счёт уже есть", SourceMessageID: run.Messages[0].ID},
		}})
		require.NoError(t, err)
		require.Equal(t, "счёт уже есть", state.ReportedFacts["account"].Value)
		return `{"kind":"advance","playbook_version":"test-v1"}`, nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.guided = &guidedConfig{playbook: p, chats: map[string]bool{"-900123": true}}
	out, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: p.AgentName, Channel: "telegram", ExternalID: "-900123", Messages: []InboundMessage{{Content: "Я новичок, хочу 2000, счёт уже есть", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Equal(t, "В каком разделе кабинета вы сейчас?", out.Text)
}

func TestPGGuidedNextTurnSeesPersistedRenderedQuestionAfterRestart(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	p := testGuidedPlaybook()
	p.AgentName = "guided-history-" + uuid.NewString()
	createRuntime := func(model A2AClient) *Runtime {
		rt := NewRuntime(store, testHooks{}, model, nil)
		rt.contextKey = []byte(testRuntimeToken)
		rt.guided = &guidedConfig{playbook: p, chats: map[string]bool{"-900123": true}}
		return rt
	}
	rt := createRuntime(runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		require.Empty(t, run.ConversationContext.GuidedPlaybook.PreviousReply)
		return `{"kind":"advance","playbook_version":"test-v1"}`, nil
	}))
	req := MessageRequest{AgentName: p.AgentName, Channel: "telegram", ExternalID: "-900123", Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}}
	first, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "У вас есть опыт торговли?", first.Text)
	calls := 0
	// New Runtime instance has no in-memory turn history. Read-back must come
	// from PostgreSQL, and a malformed second-turn plan cannot replace it.
	rt = createRuntime(runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		require.Equal(t, first.Text, run.ConversationContext.GuidedPlaybook.PreviousReply)
		require.NotContains(t, run.ConversationContext.GuidedPlaybook.PreviousReply, "playbook_version")
		require.Contains(t, renderAgentRun(run), `"previous_reply":"У вас есть опыт торговли?"`)
		if calls == 1 {
			require.Empty(t, run.ConversationContext.ReplyError)
			id, parseErr := uuid.Parse(run.ConversationContext.ConversationID)
			require.NoError(t, parseErr)
			_, stateErr := store.ApplyState(ctx, id, run.ConversationContext.State.Version, StatePatch{ReportedFacts: map[string]ReportedFact{"experience": {Value: "Нет", SourceMessageID: run.Messages[0].ID}}})
			require.NoError(t, stateErr)
			return "Unrendered rejected draft", nil
		}
		require.NotEmpty(t, run.ConversationContext.ReplyError)
		require.Equal(t, "qualification_target", run.ConversationContext.GuidedPlaybook.NextStep)
		return `{"kind":"advance","playbook_version":"test-v1"}`, nil
	}))
	req.Messages = []InboundMessage{{Content: "Нет", ChannelMessageID: "2"}}
	second, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "Какой доход вас интересует?", second.Text)
}

func TestGuidedAcceptsObservedChoiceWrapperAndEmptyAdvanceIDs(t *testing.T) {
	p := testGuidedPlaybook()
	p.Version = "referral-guided-2026-09-08-1"
	observed := `{"choice":{"card_ids":[],"kind":"advance","playbook_version":"referral-guided-2026-09-08-1"}}`
	out, err := p.render(observed, RuntimeState{})
	require.NoError(t, err)
	require.Equal(t, "У вас есть опыт торговли?", out)
	for _, raw := range []string{
		`{"kind":"advance","playbook_version":"referral-guided-2026-09-08-1","card_ids":[]}`,
		`{"choice":{"kind":"advance","playbook_version":"referral-guided-2026-09-08-1"}}`,
	} {
		out, err = p.render(raw, RuntimeState{})
		require.NoError(t, err)
		require.Equal(t, "У вас есть опыт торговли?", out)
	}
	out, err = p.render(`{"choice":{"kind":"answer","playbook_version":"referral-guided-2026-09-08-1","card_ids":["cost"]}}`, RuntimeState{})
	require.NoError(t, err)
	require.Equal(t, "Деньги остаются на вашем счёте.", out)
}
func TestGuidedWrapperDoesNotLoosenContract(t *testing.T) {
	p := testGuidedPlaybook()
	for _, raw := range []string{
		`{"choice":{"kind":"advance","playbook_version":"test-v1"},"text":"pay me"}`,
		`{"choice":{"kind":"advance","playbook_version":"test-v1","paragraphs":["pay me"]}}`,
		`{"choice":{"kind":"advance","playbook_version":"test-v1","card_ids":["cost"]}}`,
		`{"choice":{"kind":"advance","playbook_version":"stale"}}`,
		`{"choice":{"kind":"answer","playbook_version":"test-v1","card_ids":["unknown"]}}`,
		`{"choice":{"choice":{"kind":"advance","playbook_version":"test-v1"}}}`,
		`{"choice":[{"kind":"advance","playbook_version":"test-v1"}]}`,
		`{"choice":null}`,
		`{"choice":"{\\"kind\\":\\"advance\\"}"}`,
		`{"choice":{"kind":"advance","playbook_version":"test-v1"},"choice":{"kind":"advance","playbook_version":"test-v1"}}`,
		`{"choice":{"kind":"advance","kind":"answer","playbook_version":"test-v1","card_ids":["cost"]}}`,
		`{"kind":"advance","kind":"answer","playbook_version":"test-v1","card_ids":["cost"]}`,
		`Here is JSON: {"choice":{"kind":"advance","playbook_version":"test-v1"}}`,
		`{"choice":{"kind":"advance","playbook_version":"test-v1"}} trailing`,
	} {
		_, err := p.render(raw, RuntimeState{})
		require.Error(t, err, raw)
	}
}
