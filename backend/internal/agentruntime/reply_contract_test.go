package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestReplyPlanUsesStateNotModelQuestion(t *testing.T) {
	state := RuntimeState{ReportedFacts: map[string]ReportedFact{"target": {Value: "Хочу 5000"}}}
	out, err := renderReplyPlan(`{"kind":"qualification"}`, state)
	require.NoError(t, err)
	require.Equal(t, "У вас уже есть опыт торговли на форексе?", out)
	state.ReportedFacts["experience"] = ReportedFact{Value: "Я новичок"}
	out, err = renderReplyPlan(`{"kind":"qualification"}`, state)
	require.NoError(t, err)
	require.Equal(t, "Что вам сейчас нужно, чтобы двигаться дальше?", out)
	state.ReportedFacts["blocker"] = ReportedFact{Value: "Не знаю как начать"}
	_, err = renderReplyPlan(`{"kind":"qualification"}`, state)
	require.Error(t, err)
	for _, raw := range []string{`Цель зафиксировал`, `{"kind":"qualification","paragraphs":["Цель зафиксирована"]}`, `{"kind":"answer","paragraphs":["Ответ. А какой у вас счёт?"]}`, `{"kind":"answer","paragraphs":[]}`, `{"kind":"unknown"}`, `{"kind":"answer","paragraphs":["x"],"secret":"x"}`, `{"kind":"qualification"} trailing`, fmt.Sprintf(`{"kind":"answer","paragraphs":[%q]}`, strings.Repeat("я", 351))} {
		_, err := renderReplyPlan(raw, state)
		require.Error(t, err, raw)
	}
	out, err = renderReplyPlan(`{"kind":"answer","paragraphs":["Первое — факт.","Второй абзац."]}`, state)
	require.NoError(t, err)
	require.Equal(t, "Первое - факт.\n\nВторой абзац.", out)
}
func TestExplicitStopCommandsAndCounterexamples(t *testing.T) {
	for _, text := range []string{"Больше мне не отвечайте. Остановите ответы в этом чате.", "Пожалуйста, не пишите мне!", "Прекратите писать"} {
		require.True(t, explicitStop([]InboundMessage{{Content: text}}), text)
	}
	for _, text := range []string{"Не прекращайте отвечать", "Напишу позже", "Что означает:\nне пишите мне", "Он сказал: не пишите мне", `"не пишите мне"`, "Не пишите мне завтра, сегодня хочу разобраться", "Я подумаю"} {
		require.False(t, explicitStop([]InboundMessage{{Content: text}}), text)
	}
}
func TestPGStopBeforeInferenceAndCRMFailure(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	crm := 0
	rt := NewRuntime(store, testHooks{}, runFunc(func(context.Context, AgentRunRequest) (string, error) { calls++; return "wrong", nil }), nil)
	rt.contextKey = []byte(testRuntimeToken)
	agent := "optout-" + uuid.NewString()
	rt.courtesyAgents = map[string]bool{agent: true}
	rt.syncPause = func(context.Context, Conversation) error { crm++; return fmt.Errorf("CRM unavailable") }
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "4242", Messages: []InboundMessage{{Content: "Больше мне не отвечайте. Остановите ответы в этом чате.", ChannelMessageID: "1"}}, OnProcessing: func() { t.Error("opt out must not type") }}
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.True(t, out.Suppressed)
	require.Zero(t, calls)
	require.Equal(t, 1, crm)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{})
	require.NoError(t, err)
	state, err := store.GetState(ctx, conv.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	req.Messages = []InboundMessage{{Content: "Привет", ChannelMessageID: "2"}}
	out, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.True(t, out.Suppressed)
	require.Zero(t, calls)
}
func TestPGStructuredContractSavesRenderedReplyAndKeepsBadInputPending(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "contract-" + uuid.NewString()
	bad := true
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		require.Equal(t, structuredReplyFormat, run.ConversationContext.ReplyFormat)
		if bad {
			return "unstructured forbidden text", nil
		}
		return `{"kind":"qualification"}`, nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.structuredAgents = map[string]bool{agent: true}
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "42", Messages: []InboundMessage{{Content: "Хочу вступить", ChannelMessageID: "1"}}}
	_, err := rt.ProcessMessage(ctx, req)
	require.Error(t, err)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "42", Actor{})
	require.NoError(t, err)
	pending, err := store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	bad = false
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "У вас уже есть опыт торговли на форексе?", out.Text)
	history, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, out.Text, history[1].Content)
}

func TestReplyPlanURLQueryIsNotAQuestion(t *testing.T) {
	out, err := renderReplyPlan(`{"kind":"instruction","paragraphs":["Откройте https://partner.example.test/register?ref=demo ."]}`, RuntimeState{})
	require.NoError(t, err)
	require.Contains(t, out, "?ref=demo")
}
func TestPGProtocolRepairIsBounded(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	agent := "repair-" + uuid.NewString()
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		if calls == 1 {
			require.Empty(t, run.ConversationContext.ReplyError)
			return "bad reply", nil
		}
		require.NotEmpty(t, run.ConversationContext.ReplyError)
		return `{"kind":"qualification"}`, nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.structuredAgents = map[string]bool{agent: true}
	out, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "42", Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "У вас уже есть опыт торговли на форексе?", out.Text)
}
