package agentruntime

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func readLeakFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLeakReasonCatchesMonologues(t *testing.T) {
	cases := map[string]string{
		"S204 reasoning with rule quotes":        readLeakFixture(t, "leak_s204.txt"),
		"S412 KB and english fillers":            readLeakFixture(t, "leak_s412.txt"),
		"S250 tool intent as text":               "Загрузите price skill? Цель 800 в месяц требует не половины той суммы, что уже отложена, стартуйте с 2000. С 2000 стартуем?",
		"S251 token fragment":                    "С опытом в три года_signals группе будет даже проще освоить, ссылку выше уже отправила",
		"tool name":                              "Сейчас вызову update_conversation_state и отвечу: счёт у FxPro уже есть?",
		"runtime field":                          "expected_version не совпала, повторю. Счёт у FxPro уже есть?",
		"english reasoning inside russian reply": "The user asks about leverage. Отдельных требований к плечу нет, оставляйте настройки FxPro по умолчанию.",
		"placeholder":                            "placeholder: уточнить у куратора. Депозит виден в кабинете?",
		"mixed script":                           "Okay so client wants to know about leverage settings, но в базе этого нет, so I will answer that defaults are fine and move on to deposit. Отдельных требований нет.",
		"long reply":                             strings.Repeat("Депозит заводите из личного кабинета FxPro, сумму выбираете сами. ", 12),
	}
	for name, reply := range cases {
		if reason := leakReason(reply); reason == "" {
			t.Errorf("%s: expected leak, got clean", name)
		}
	}
}

func TestLeakReasonPassesClientReplies(t *testing.T) {
	cases := map[string]string{
		"link and brand":        "С 600 доступных стартуем сразу, регистрация у FxPro идёт по нашей ссылке: https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web , открывайте кабинет и напишите, когда он создан",
		"MT5 and platforms":     "MT5 ставится на iOS и Android из App Store и Google Play, логин и пароль от торгового счёта приходят на почту после открытия счёта в кабинете FxPro. Установили?",
		"english client":        "Registering through our partner link takes a couple of minutes, then verification in the profile: https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web — open it and start the FxPro signup, write here when the account is created?",
		"handle":                "Куратор напишет вам с аккаунта @fxpro_curator в течение дня, ждите сообщение",
		"short question":        "Счёт у FxPro уже открыт?",
		"quote of client":       "Вы написали «не понравится, смогу вывести?»: да, деньги остаются на вашем счёте у брокера, вывод в любой момент из кабинета",
		"skills word natural":   "Навыки торговли не нужны, куратор ведёт с нуля. Счёт у FxPro уже есть?",
		"several questions":     "По порядку: 1) плечо оставляйте по умолчанию, 2) тип счёта тоже стандартный, 3) MT5 для iPhone есть в App Store. Кабинет уже открыт?",
		"empty":                 "",
		"english client filler": "Okay, let me be clear: the account is opened at FxPro through our link, the deposit stays on your own broker account. Fine to continue to the deposit step?",
		"P2P and support mail":  "Пополнение через P2P у FxPro нет, есть карта и SBP; по спорным платежам пишите на support@fxpro.com. Депозит уже виден?",
	}
	for name, reply := range cases {
		if reason := leakReason(reply); reason != "" {
			t.Errorf("%s: expected clean, got %q", name, reason)
		}
	}
}

func TestPGLeakedMonologueIsRepairedOnce(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	agent := "leak-" + uuid.NewString()
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		if calls == 1 {
			require.Empty(t, run.ConversationContext.ReplyError)
			return readLeakFixture(t, "leak_s412.txt"), nil
		}
		require.Contains(t, run.ConversationContext.ReplyError, "черновик")
		return "Отдельных требований к плечу и типу счёта у группы нет, оставляйте настройки FxPro по умолчанию. Депозит уже виден в кабинете?", nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	out, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "412", Messages: []InboundMessage{{Content: "какое плечо ставить?", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "Отдельных требований к плечу и типу счёта у группы нет, оставляйте настройки FxPro по умолчанию. Депозит уже виден в кабинете?", out.Text)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "412", Actor{})
	require.NoError(t, err)
	history, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, out.Text, history[1].Content)
}

func TestPGLeakedMonologueTwiceIsNeverDelivered(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	leaking := true
	agent := "leak-" + uuid.NewString()
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		if leaking {
			return readLeakFixture(t, "leak_s204.txt"), nil
		}
		return "Счёт у FxPro уже открыт?", nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "204", Messages: []InboundMessage{{Content: "привет, опыт нет, свободно 500, цель 300", ChannelMessageID: "1"}}}
	_, err := rt.ProcessMessage(ctx, req)
	require.Error(t, err)
	require.Equal(t, 2, calls)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "204", Actor{})
	require.NoError(t, err)
	pending, err := store.PendingRuntimeMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	leaking = false
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "Счёт у FxPro уже открыт?", out.Text)
	history, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	for _, m := range history {
		if m.Role == "assistant" {
			require.Equal(t, out.Text, m.Content)
		}
	}
}
