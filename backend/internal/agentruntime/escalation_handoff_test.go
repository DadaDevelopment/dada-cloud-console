package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEscalationTitle_DepositHandoffIsNotAnIncident(t *testing.T) {
	require.Equal(t, "🤝 Передача куратору", escalationTitle("E_DEPOSIT_HANDOFF"))
	for _, reason := range []string{"E_DISTRUST", "E_WITHDRAW", "E_OTHER", ""} {
		require.Equal(t, "🔺 Эскалация", escalationTitle(reason), reason)
	}
}

func TestEscalationCard_DepositHandoffCarriesLabel(t *testing.T) {
	conv := Conversation{Channel: "telegram", ActorExternalID: "778", ActorUsername: "client"}
	state := RuntimeState{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "пополнил 1000"}, "account": {Value: "счёт у FxPro по вашей ссылке"}}}
	card := escalationCard(escalationTitle("E_DEPOSIT_HANDOFF"), conv, "E_DEPOSIT_HANDOFF", "Пополнил 1000, MT5 поставил, ждёт куратора.", state)
	for _, want := range []string{
		"🤝 Передача куратору",
		"Причина: E_DEPOSIT_HANDOFF (клиент сообщил о пополнении, дальше ведёт куратор)",
		"- deposit: пополнил 1000",
		"Агент на паузе",
	} {
		require.Contains(t, card, want)
	}
	require.NotContains(t, card, "🔺")
}

func TestPGEscalate_DepositHandoffPausesAndTellsClient(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1003", Actor{ExternalID: "1003", Username: "client"})
	require.NoError(t, err)
	_, _, err = store.GetOrCreateConversation(ctx, agent, "telegram", "4243", Actor{ExternalID: "4243", Username: "Java_Er"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	var crmReason string
	srv.pauseCRM = pauseFunc(func(_ context.Context, _ Conversation, reason string) error {
		crmReason = reason
		return nil
	})
	out := &recordingOutbound{}
	srv.outbound = out
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_DEPOSIT_HANDOFF", "summary": "Пополнил 1000 по своим словам, счёт по ссылке, MT5 не ставил.", "client_message": "Деньги на счёте, дальше подключением к группе занимается куратор, напишет вам здесь"}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, false, result["agent_enabled"])
	require.Equal(t, true, result["client_notified"])
	require.Equal(t, true, result["operator_notified"])
	require.Equal(t, "escalated: E_DEPOSIT_HANDOFF", crmReason)
	require.Equal(t, []string{"1003", "4243"}, out.chats)
	require.Contains(t, out.texts[0], "куратор")
	require.Contains(t, out.texts[1], "🤝 Передача куратору")
	require.Contains(t, out.texts[1], "E_DEPOSIT_HANDOFF")
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "escalated: E_DEPOSIT_HANDOFF", state.PauseReason)
}
