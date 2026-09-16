package agentruntime

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type recordingOutbound struct {
	mu    sync.Mutex
	chats []string
	texts []string
}

func (r *recordingOutbound) SendOutbound(_ context.Context, _ string, chatExternalID, text, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chats = append(r.chats, chatExternalID)
	r.texts = append(r.texts, text)
	return nil
}

func TestEscalationCard_CarriesReasonSummaryFactsAndOpenLoops(t *testing.T) {
	conv := Conversation{Channel: "telegram", ActorExternalID: "777", ActorUsername: "client", ActorMetadata: map[string]any{"first_name": "Иван"}}
	state := RuntimeState{
		ReportedFacts: map[string]ReportedFact{"amount": {Value: "200к"}, "experience": {Value: "торговал год"}},
		OpenLoops:     map[string]OpenLoop{"account_link": {Question: "Счёт открыт по ссылке?", Status: "open"}, "deposit_status": {Question: "Пополнил?", Status: "resolved"}},
	}
	card := escalationCard("🔺 Эскалация", conv, "E_DISTRUST", "Хочет доказательств доходности.", state)
	for _, want := range []string{
		"Клиент: @client · Иван · tg://user?id=777",
		"Причина: E_DISTRUST (недоверие, требует доказательств)",
		"Хочет доказательств доходности.",
		"- amount: 200к",
		"- experience: торговал год",
		"- account_link: Счёт открыт по ссылке?",
		"Агент на паузе",
	} {
		require.Contains(t, card, want)
	}
	require.NotContains(t, card, "Пополнил?")
	require.Less(t, strings.Index(card, "- amount"), strings.Index(card, "- experience"))
}

func TestPGEscalate_HandoffPausesNotifiesOperatorAndSyncsCRM(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1001", Actor{ExternalID: "1001", Username: "client", Metadata: map[string]any{"first_name": "Иван"}})
	require.NoError(t, err)
	_, _, err = store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{ExternalID: "4242", Username: "Java_Er"})
	require.NoError(t, err)
	_, err = store.GetState(ctx, client.ID)
	require.NoError(t, err)

	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	paused := 0
	srv.pauseCRM = pauseFunc(func(_ context.Context, got Conversation, reason string) error {
		paused++
		require.Equal(t, client.ID, got.ID)
		require.Equal(t, "escalated: E_SECOND_PERSON", reason)
		return nil
	})
	out := &recordingOutbound{}
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "e_second_person", "summary": "Пишет другой человек, просит перевести деньги.", "client_message": "Иван, по этому вопросу вам напишет коллега"}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, false, result["agent_enabled"])
	require.Equal(t, true, result["client_notified"])
	require.Equal(t, true, result["operator_notified"])
	require.Equal(t, "completed", result["crm_status_sync"])
	require.Equal(t, 1, paused)
	require.Equal(t, []string{"1001", "4242"}, out.chats)
	require.Equal(t, "Иван, по этому вопросу вам напишет коллега", out.texts[0])
	require.Contains(t, out.texts[1], "🔺 Эскалация")
	require.Contains(t, out.texts[1], "@client · Иван · tg://user?id=1001")
	require.Contains(t, out.texts[1], "E_SECOND_PERSON")
	require.Contains(t, out.texts[1], "Пишет другой человек, просит перевести деньги.")
	history, err := store.GetRecentMessages(ctx, client.ID, 1)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "assistant", history[0].Role)
	require.Equal(t, out.texts[0], history[0].Content)

	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "escalated: E_SECOND_PERSON", state.PauseReason)

	status, _ = postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_FANTASY", "summary": "x"}, testRuntimeToken)
	require.Equal(t, 400, status)
}

func TestPGEscalate_SignalKeepsAgentLiveAndPagesOperatorOnce(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1001", Actor{ExternalID: "1001", Username: "client", Metadata: map[string]any{"first_name": "Иван"}})
	require.NoError(t, err)
	_, _, err = store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{ExternalID: "4242", Username: "Java_Er"})
	require.NoError(t, err)

	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error {
		t.Fatal("signal reasons must not pause")
		return nil
	})
	out := &recordingOutbound{}
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "e_distrust", "summary": "Хочет пруфы доходности, злится.", "client_message": "Иван, по цифрам вам напишет коллега, он ведёт этот вопрос"}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["agent_enabled"])
	require.Equal(t, "signal", result["mode"])
	require.Equal(t, false, result["client_notified"])
	require.Equal(t, true, result["operator_notified"])
	require.Equal(t, false, result["already_signalled"])
	require.Contains(t, result["next"], "Do not promise a colleague")
	require.Equal(t, []string{"4242"}, out.chats, "client_message is not delivered on a signal")
	require.Contains(t, out.texts[0], "🔔 Сигнал оператору")
	require.Contains(t, out.texts[0], "E_DISTRUST")
	require.Contains(t, out.texts[0], "Хочет пруфы доходности, злится.")
	require.Contains(t, out.texts[0], "Агент продолжает диалог сам")
	require.NotContains(t, out.texts[0], "Агент на паузе")
	history, err := store.GetRecentMessages(ctx, client.ID, 5)
	require.NoError(t, err)
	require.Empty(t, history)
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
	require.Empty(t, state.PauseReason)

	status, result = postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_DISTRUST", "summary": "Снова требует стейтмент."}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["agent_enabled"])
	require.Equal(t, true, result["already_signalled"])
	require.Equal(t, false, result["operator_notified"])
	require.Len(t, out.chats, 1, "same reason inside the window does not page the operator again")

	status, result = postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_WITHDRAW", "summary": "Спрашивает про вывод."}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, false, result["already_signalled"])
	require.Len(t, out.chats, 2)
	require.Contains(t, out.texts[1], "E_WITHDRAW")
}

func TestPGEscalate_UnknownOperatorStillPausesHandoff(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1002", Actor{ExternalID: "1002", Username: "client"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_PAYMENT_UNCONFIRMED", "summary": "Деньги ушли, зачисления нет."}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, false, result["agent_enabled"])
	require.Equal(t, false, result["operator_notified"])
	require.Empty(t, out.chats)
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
}

func TestEscalationHandsOff_SplitsHandoffFromSignal(t *testing.T) {
	for _, reason := range []string{"E_DEPOSIT_HANDOFF", "E_PAYMENT_UNCONFIRMED", "E_SECOND_PERSON", "E_OTHER"} {
		require.True(t, escalationHandsOff[reason], reason)
	}
	for _, reason := range []string{"E_DISTRUST", "E_GUARANTEE_DEMAND", "E_TERMS_OFF_LADDER", "E_TECH_BLOCKED", "E_LEGAL_TAX", "E_LOST_MONEY", "E_WITHDRAW"} {
		require.False(t, escalationHandsOff[reason], reason)
		_, known := escalationReasons[reason]
		require.True(t, known, reason)
	}
}

func TestPGStopAgent_NotifiesOperator(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1003", Actor{ExternalID: "1003", Username: "client"})
	require.NoError(t, err)
	_, _, err = store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{ExternalID: "4242", Username: "java_er"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, _ := postRuntime(t, httpServer.URL, "/tools/stop-agent", map[string]any{"context_token": token, "reason": "client declined"}, testRuntimeToken)
	require.Equal(t, 200, status)
	require.Equal(t, []string{"4242"}, out.chats)
	require.Contains(t, out.texts[0], "⏸ Клиент попросил не писать")
	require.Contains(t, out.texts[0], "client declined")
}
