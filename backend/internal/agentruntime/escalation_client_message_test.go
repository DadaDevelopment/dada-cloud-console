package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFixClientMessageGender_S92RewritesFeminineVerbToMasculine(t *testing.T) {
	got := fixClientMessageGender("Номер счёта и почту получила, дальше подключением занимается куратор")
	require.Equal(t, "Номер счёта и почту получил, дальше подключением занимается куратор", got)
}

func TestFixClientMessageGender_CapitalizedSentenceStart(t *testing.T) {
	require.Equal(t, "Понял, спасибо", fixClientMessageGender("Поняла, спасибо"))
}

func TestFixClientMessageGender_AdjacentWordsBothFixed(t *testing.T) {
	require.Equal(t, "понял и записал реквизиты", fixClientMessageGender("поняла и записала реквизиты"))
}

func TestFixClientMessageGender_LeavesMasculineAndUnrelatedWordsAlone(t *testing.T) {
	text := "Деньги на счёте, дальше подключением к группе занимается куратор, напишет вам здесь"
	require.Equal(t, text, fixClientMessageGender(text))
}

func TestClientMessageEchoesSummary_S374ThirdPersonNarrativeFlagged(t *testing.T) {
	summary := "Вернувшийся клиент с июля, куратор Василий уже присылал ссылку на канал через коллегу, ждёт продолжения."
	clientMessage := "Приветствую, вернувшийся участник с июля, куратор Василий, прислал ссылку на канал через коллегу"
	require.True(t, clientMessageEchoesSummary(clientMessage, summary))
}

func TestClientMessageEchoesSummary_GenuineDepositHandoffNotFlagged(t *testing.T) {
	summary := "Пополнил 1000 по своим словам, счёт по ссылке, MT5 не ставил."
	clientMessage := "Деньги на счёте, дальше подключением к группе занимается куратор, напишет вам здесь"
	require.False(t, clientMessageEchoesSummary(clientMessage, summary))
}

func TestClientMessageEchoesSummary_ShortClientMessageNeverFlagged(t *testing.T) {
	require.False(t, clientMessageEchoesSummary("Куратор напишет здесь", "Клиент недоволен, куратор напишет здесь позже."))
}

func TestPGEscalate_FixesFeminineVerbInClientMessage(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "2001", Actor{ExternalID: "2001", Username: "client"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{
		"context_token":  token,
		"reason_code":    "E_DEPOSIT_HANDOFF",
		"summary":        "Пополнил 500, реквизиты дал сам.",
		"client_message": "Номер счёта и почту получила, дальше подключением занимается куратор",
	}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["client_notified"])
	require.Len(t, out.texts, 1)
	require.Equal(t, "Номер счёта и почту получил, дальше подключением занимается куратор", out.texts[0])
}

func TestPGEscalate_RejectsClientMessageEchoingSummary(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "2002", Actor{ExternalID: "2002", Username: "client"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{
		"context_token":  token,
		"reason_code":    "E_OTHER",
		"summary":        "Вернувшийся клиент с июля, куратор Василий уже присылал ссылку на канал через коллегу, ждёт продолжения.",
		"client_message": "Приветствую, вернувшийся участник с июля, куратор Василий, прислал ссылку на канал через коллегу",
	}, testRuntimeToken)
	require.Equal(t, 400, status, result)
	require.Equal(t, "client_message_echoes_summary", result["error_code"])
	require.Empty(t, out.texts)

	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
}
