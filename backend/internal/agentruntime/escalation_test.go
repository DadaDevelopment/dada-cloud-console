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

	"github.com/dada-tuda/console/backend/internal/agentjudge"
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
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	signals, _ := fresh.Metadata["escalation_signals"].(map[string]any)
	require.Contains(t, signals, "E_DISTRUST", "a plain signal claims the bare reason key")
	require.NotContains(t, signals, narrowSignalKey("E_DISTRUST"))

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

// AGENT_RUNTIME_HANDOFF_TRIGGERS makes E_LEGAL_TAX hands-off; off, it stays
// a signal. E_CHECK_AND_RETURN is known and a signal under both states: the
// model's tool enum always carries it (plan 2026-09-25, Q2), and an unknown
// code would leave "Уточню и вернусь" without an operator.
func TestEscalationHandoffTriggersFlag(t *testing.T) {
	off := &Server{runtime: &Runtime{flags: runtimeFlagsFromEnv()}}
	require.False(t, off.handsOff("E_LEGAL_TAX"))
	require.True(t, off.escalationReasonKnown("E_CHECK_AND_RETURN"))
	require.False(t, off.handsOff("E_CHECK_AND_RETURN"))
	require.Equal(t, "E_CHECK_AND_RETURN, E_DEPOSIT_HANDOFF, E_DISTRUST, E_GUARANTEE_DEMAND, E_LEGAL_TAX, E_LOST_MONEY, E_OTHER, E_PAYMENT_UNCONFIRMED, E_SECOND_PERSON, E_TECH_BLOCKED, E_TERMS_OFF_LADDER, E_WITHDRAW",
		strings.Join(off.escalationReasonCodes(), ", "))

	t.Setenv("AGENT_RUNTIME_HANDOFF_TRIGGERS", "1")
	on := &Server{runtime: &Runtime{flags: runtimeFlagsFromEnv()}}
	require.True(t, on.handsOff("E_LEGAL_TAX"))
	require.False(t, on.handsOff("E_CHECK_AND_RETURN"), "plan 2026-09-25 Q2: a signal, the bot keeps the script")
	require.True(t, on.escalationReasonKnown("E_CHECK_AND_RETURN"))
	require.Contains(t, on.escalationReasonCodes(), "E_CHECK_AND_RETURN")
	for _, reason := range []string{"E_DISTRUST", "E_WITHDRAW", "E_TECH_BLOCKED"} {
		require.False(t, on.handsOff(reason), reason)
	}
	for _, reason := range []string{"E_DEPOSIT_HANDOFF", "E_OTHER"} {
		require.True(t, on.handsOff(reason), reason)
	}
}

// escalateTestServer is one conversation plus the operator's, a server with
// recording outbound and a no-op CRM pause.
func escalateTestServer(t *testing.T, env map[string]string) (*pgStore, *Server, *recordingOutbound, Conversation, string, func()) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "1001", Actor{ExternalID: "1001", Username: "client"})
	require.NoError(t, err)
	_, _, err = store.GetOrCreateConversation(ctx, agent, "telegram", "4242", Actor{ExternalID: "4242", Username: "java_er"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.operator = &OperatorNotifier{username: "java_er", resolve: pgOperatorChat(store.pool), outbound: out}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)
	return store, srv, out, client, httpServer.URL + "|" + token, httpServer.Close
}

// W4 "done when": /tools/escalate answers old codes exactly as before, with
// the triggers flag off and on. The expected bodies are the literal maps the
// handler wrote before handOff was extracted.
func TestPGEscalate_OldCodesAnswerBitForBitWithTriggersOffAndOn(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		_, _, _, _, target, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": flag})
		base, token, _ := strings.Cut(target, "|")

		status, signal := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_DISTRUST", "summary": "Хочет пруфы."}, testRuntimeToken)
		require.Equal(t, 200, status, signal)
		require.Equal(t, map[string]any{"agent_enabled": true, "mode": "signal", "client_notified": false, "operator_notified": true,
			"already_signalled": false, "next": escalationSignalNext, "state_version": signal["state_version"]}, signal, "flag=%q", flag)

		status, legal := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_LEGAL_TAX", "summary": "Спрашивает про НДФЛ."}, testRuntimeToken)
		require.Equal(t, 200, status, legal)
		if flag == "" {
			require.Equal(t, "signal", legal["mode"], "flag off: E_LEGAL_TAX stays a signal")
			require.Equal(t, true, legal["agent_enabled"])
			require.Equal(t, escalationSignalNext, legal["next"])
		}

		status, check := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_CHECK_AND_RETURN", "summary": "Уточнить комиссию."}, testRuntimeToken)
		require.Equal(t, 200, status, check)
		require.Equal(t, "signal", check["mode"], "flag=%q: E_CHECK_AND_RETURN is a signal either way", flag)
		require.Equal(t, true, check["agent_enabled"])
		require.Equal(t, true, check["operator_notified"])
		closeServer()
	}

	for _, flag := range []string{"", "1"} {
		_, _, _, _, target, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": flag})
		base, token, _ := strings.Cut(target, "|")
		status, handoff := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_PAYMENT_UNCONFIRMED", "summary": "Деньги ушли, зачисления нет.", "client_message": "Принял, проверяем платёж"}, testRuntimeToken)
		require.Equal(t, 200, status, handoff)
		require.Equal(t, map[string]any{"agent_enabled": false, "client_notified": true, "operator_notified": true,
			"crm_status_sync": handoff["crm_status_sync"], "state_version": handoff["state_version"]}, handoff, "flag=%q", flag)
		require.NotEmpty(t, handoff["crm_status_sync"])
		closeServer()
	}
}

// Flag on: E_LEGAL_TAX from the model pauses, the client gets the tool's
// line, the operator gets the hand-off card, and there is no "answer the
// customer yourself" instruction in the response.
func TestPGEscalate_LegalTaxIsHandsOffUnderTriggers(t *testing.T) {
	store, _, out, client, target, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"})
	defer closeServer()
	base, token, _ := strings.Cut(target, "|")

	status, result := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_LEGAL_TAX", "summary": "Спрашивает, как платить налог с дохода.", "client_message": "По налогам подскажет коллега, он напишет здесь"}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, false, result["agent_enabled"])
	require.Equal(t, true, result["client_notified"])
	require.Equal(t, true, result["operator_notified"])
	require.NotContains(t, result, "next")
	require.Equal(t, []string{"1001", "4242"}, out.chats)
	require.Equal(t, "По налогам подскажет коллега, он напишет здесь", out.texts[0])
	require.Contains(t, out.texts[1], "E_LEGAL_TAX")
	require.Contains(t, out.texts[1], "Агент на паузе")
	state, err := store.GetState(context.Background(), client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "escalated: E_LEGAL_TAX", state.PauseReason)
}

// A runtime hand-off with ForcePause (refusal threshold, check verdict)
// bypasses narrow mode, delivers ClientLine, sends the card and drains the
// pending input.
func TestPGHandOff_ForcePauseBypassesNarrowAndDrainsPending(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": "1", "AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	ctx := context.Background()
	_, err := store.SaveMessage(ctx, client.ID, SaveMessageInput{Role: "user", Content: "не верю я вам"})
	require.NoError(t, err)
	pending, err := store.PendingRuntimeMessages(ctx, client.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	res, err := srv.handOff(ctx, client, HandoffRequest{Code: "E_DISTRUST", Summary: "refusal_distrust x2", ClientLine: "line from the spec", ForcePause: true})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.True(t, res.Paused)
	require.True(t, res.ClientNotified)
	require.True(t, res.OperatorNotified)
	require.Equal(t, []string{"1001", "4242"}, out.chats)
	require.Equal(t, "line from the spec", out.texts[0])
	require.Contains(t, out.texts[1], "E_DISTRUST")
	require.Contains(t, out.texts[1], "refusal_distrust x2")

	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled, "a forced pause is a pause, not narrow mode")
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	_, inNarrow := narrowSince(fresh)
	require.False(t, inNarrow)
	pending, err = store.PendingRuntimeMessages(ctx, client.ID)
	require.NoError(t, err)
	require.Empty(t, pending, "silence recovery must not replay the handed-off input")
}

// Plan 2026-09-25 Q2: E_CHECK_AND_RETURN is a signal without a pause, even
// under narrow mode, and a second one inside the signal window still reaches
// the operator (a plain signal reason is deduplicated as before).
func TestPGHandOff_CheckAndReturnSignalsWithoutPauseAndRepeats(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": "1", "AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	ctx := context.Background()

	res, err := srv.handOff(ctx, client, HandoffRequest{Code: escalationCheckAndReturn, Summary: "commission", ClientLine: "ignored"})
	require.NoError(t, err)
	require.Equal(t, "signal", res.Body["mode"])
	require.False(t, res.Paused)
	require.Equal(t, false, res.Body["already_signalled"])
	res, err = srv.handOff(ctx, client, HandoffRequest{Code: escalationCheckAndReturn, Summary: "withdrawal term"})
	require.NoError(t, err)
	require.Equal(t, "signal", res.Body["mode"])
	require.True(t, res.OperatorNotified, "a repeat inside the window is not dropped")
	require.Equal(t, []string{"4242", "4242"}, out.chats, "the client gets nothing from the runtime")
	require.Contains(t, out.texts[0], "commission")
	require.Contains(t, out.texts[1], "withdrawal term")

	_, err = srv.handOff(ctx, client, HandoffRequest{Code: "E_DISTRUST", Summary: "one"})
	require.NoError(t, err)
	res, err = srv.handOff(ctx, client, HandoffRequest{Code: "E_DISTRUST", Summary: "two"})
	require.NoError(t, err)
	require.False(t, res.OperatorNotified, "other signal reasons keep the window")
	require.Len(t, out.chats, 3)

	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	_, inNarrow := narrowSince(fresh)
	require.False(t, inNarrow)
}

// Without ForcePause and with narrow mode on, a runtime hand-off of a plain
// hands-off code goes to narrow mode exactly like the tool call does.
func TestPGHandOff_WithoutForcePauseFollowsNarrow(t *testing.T) {
	store, srv, _, client, _, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	res, err := srv.handOff(context.Background(), client, HandoffRequest{Code: "E_OTHER", Summary: "Порог отказов."})
	require.NoError(t, err)
	require.Equal(t, "narrow", res.Body["mode"])
	state, err := store.GetState(context.Background(), client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
}

// M1: a check verdict whose code hands off (E_OTHER here) pauses even under
// narrow mode, like the refusal threshold; E_CHECK_AND_RETURN stays a signal
// without a pause (Q2). The model's own tool call still follows narrow mode
// (TestPGHandOff_WithoutForcePauseFollowsNarrow).
func TestPGHandOff_PrecheckViolationPausesUnderNarrow(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{
		"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1", "AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	ctx := context.Background()
	agent := &scriptedAgent{drafts: []string{"сейчас переведу вам"}}
	judge := &fakePrecheck{
		verdicts: []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}},
		criteria: map[string]agentjudge.Criterion{"fact_unbacked": {Line: "Уточню и вернусь"}, "final_refusal": {Line: "final line"}},
	}
	srv.runtime.a2a, srv.runtime.ext.precheck, srv.runtime.judge, srv.runtime.hooks = agent, judge, judge, testHooks{}
	turn := func(text string) MessageResponse {
		resp, err := srv.runtime.ProcessMessage(ctx, MessageRequest{AgentName: client.AgentName, Channel: "telegram", ExternalID: "1001",
			Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
		require.NoError(t, err)
		return resp
	}

	require.Equal(t, "Уточню и вернусь", turn("какая комиссия?").Text)
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled, "E_CHECK_AND_RETURN does not pause")
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	_, inNarrow := narrowSince(fresh)
	require.False(t, inNarrow)

	judge.mu.Lock()
	judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("final_refusal", agentjudge.BlockHandoff, "ask", "E_OTHER")}}}
	judge.mu.Unlock()
	require.True(t, turn("нет, окончательно не интересно").Suppressed)
	state, err = store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled, "a check-decided E_OTHER is a pause, not narrow mode")
	require.Equal(t, "escalated: E_OTHER", state.PauseReason)
	fresh, err = store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	_, inNarrow = narrowSince(fresh)
	require.False(t, inNarrow)
	require.Contains(t, out.texts, "final line")
	pending, err := store.PendingRuntimeMessages(ctx, client.ID)
	require.NoError(t, err)
	require.Empty(t, pending)
}
