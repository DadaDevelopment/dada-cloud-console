package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var testLinkAllowlist = ParseLinkAllowlist("direct-fxpro.com, https://docs.google.com/spreadsheets/,t.me/robinhoodmetals")

const partnerLink = "https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web"

func TestLinkLeakReasonCatchesFabricatedLinks(t *testing.T) {
	cases := map[string]string{
		"S431 signup ref":      "Регистрируйтесь по ссылке https://fxpro.com/signup?ref=group, пополняйте от 300 и куратор подключит вас",
		"S20 register ref":     "Ссылка на регистрацию: https://fxpro.com/register?ref=group",
		"bare domain path":     "Регистрация тут: fxpro.com/register?ref=group, дальше пополнение",
		"lookalike host":       "Регистрируйтесь: https://direct-fxpro.com.promo.ru/ref/777",
		"other telegram chat":  "Вступайте в канал https://t.me/+etTZwj4zQNowZWI6, там сигналы",
		"http scheme":          "Ссылка http://fxpro-bonus-registration.ru/ref/777",
		"link in parentheses":  "Регистрируйтесь (https://fxpro.com/ref) и пишите",
		"link before question": "Открывайте счёт по https://fxpro.com/signup?ref=group. Готово?",
	}
	for name, reply := range cases {
		reason := linkLeakReason(reply, testLinkAllowlist, false)
		require.NotEmpty(t, reason, name)
		require.Contains(t, reason, "link outside allowlist ", name)
	}
	require.Equal(t, "link outside allowlist https://fxpro.com/signup?ref=group", linkLeakReason(cases["link before question"], testLinkAllowlist, false))
}

func TestLinkLeakReasonPassesKnowledgeBaseLinks(t *testing.T) {
	cases := map[string]string{
		"partner link":              "Регистрируйтесь по этой ссылке: " + partnerLink + ", напишите, когда кабинет откроется",
		"partner link before comma": "Ссылка: " + partnerLink + ", открывайте кабинет",
		"stats sheet":               "Статистика группы: https://docs.google.com/spreadsheets/d/1tU4ZDuGIkwqWHrOVtzaOKvVsLDHf5THVZyahu-y3QTE/edit?usp=sharing",
		"support mail":              "По спорным платежам пишите на support@fxpro.com. Депозит уже виден?",
		"partner mail":              "Привязку вы уже запросили у FxPro на cis@fxpro.com, остаётся ждать ответ",
		"brand without link":        "Счёт у FxPro уже открыт? Регистрация занимает пару минут",
		"amount with dot":           "Баланс 500.52 доллара после прогрева это штатно",
		"handle":                    "Куратор напишет вам с аккаунта @fxpro_curator",
		"empty":                     "",
	}
	for name, reply := range cases {
		require.Empty(t, linkLeakReason(reply, testLinkAllowlist, false), name)
	}
}

// Design 2026-09-24 §3: under AGENT_RUNTIME_HANDOFF_TRIGGERS the
// robinhoodmetals channel is refused by a constant denylist even when the env
// allowlist admits it, and even with no allowlist; the reason says it is a
// denied link. With the flag off the allowlist alone decides, as before.
func TestLinkLeakReasonDenylistBeatsAllowlist(t *testing.T) {
	require.Contains(t, testLinkAllowlist, "t.me/robinhoodmetals", "the fixture must admit the channel for this test to mean anything")
	for _, reply := range []string{
		"Обзор здесь: https://t.me/robinhoodmetals/58",
		"Канал: t.me/robinhoodmetals/1",
		"Подписывайтесь http://T.me/RobinhoodMetals/58.",
	} {
		reason := linkLeakReason(reply, testLinkAllowlist, true)
		require.True(t, strings.HasPrefix(reason, linkReasonDenied), reply)
		require.Contains(t, strings.ToLower(reason), "t.me/robinhoodmetals", reply)
		require.NotEmpty(t, linkLeakReason(reply, nil, true), "denylist applies without an allowlist: "+reply)
		require.Empty(t, linkLeakReason(reply, testLinkAllowlist, false), "flag off: the allowlist admits it, as before: "+reply)
		require.Empty(t, linkLeakReason(reply, nil, false), "flag off, no allowlist: nothing is checked, as before: "+reply)
		hint := leakRepairMessage(reason)
		require.Contains(t, strings.ToLower(hint), "t.me/robinhoodmetals", "the repair names the link, not a monologue")
		require.NotContains(t, hint, linkReasonDenied)
	}
	require.Empty(t, linkLeakReason("Обзор: https://t.me/robinhood/58", nil, true))
	require.Empty(t, linkLeakReason("Регистрация: "+partnerLink, testLinkAllowlist, true))
}

// A draft that twice carries a denied link fails the turn with an honest
// reason; with the flag off the same draft goes out.
func TestPGDeniedLinkFailsHonestlyUnderTriggers(t *testing.T) {
	draft := "Обзор здесь: https://t.me/robinhoodmetals/58"
	run := func(t *testing.T, env map[string]string) (MessageResponse, error, int) {
		for k, v := range env {
			t.Setenv(k, v)
		}
		store := setupTestStore(t).(*pgStore)
		calls := 0
		rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
			calls++
			return draft, nil
		}), nil)
		rt.contextKey = []byte(testRuntimeToken)
		rt.linkAllowlist = testLinkAllowlist
		out, err := rt.ProcessMessage(context.Background(), MessageRequest{AgentName: "deny-" + uuid.NewString(), Channel: "telegram", ExternalID: "58",
			Messages: []InboundMessage{{Content: "где обзоры?", ChannelMessageID: uuid.NewString()}}})
		return out, err, calls
	}
	t.Run("off", func(t *testing.T) {
		out, err, calls := run(t, nil)
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, draft, out.Text)
	})
	t.Run("on", func(t *testing.T) {
		_, err, calls := run(t, map[string]string{"AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"})
		require.Error(t, err)
		require.Equal(t, 2, calls)
		require.Contains(t, err.Error(), "denied link")
		require.NotContains(t, err.Error(), "internal reasoning")
	})
}

func TestLinkLeakReasonDisabledWithoutAllowlist(t *testing.T) {
	require.Empty(t, linkLeakReason("Ссылка https://fxpro.com/signup?ref=group", nil, false))
	require.Empty(t, ParseLinkAllowlist(" , "))
}

func TestLeakRepairMessageNamesTheFabricatedLink(t *testing.T) {
	hint := leakRepairMessage(linkLeakReason("Регистрируйтесь: https://fxpro.com/signup?ref=group", testLinkAllowlist, false))
	require.Contains(t, hint, "https://fxpro.com/signup?ref=group")
	require.Contains(t, hint, "ref_link")
	require.NotContains(t, hint, "плейсхолдер")
}

func TestPGFabricatedLinkIsRepairedOnce(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	calls := 0
	agent := "link-" + uuid.NewString()
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		if calls == 1 {
			require.Empty(t, run.ConversationContext.ReplyError)
			return "Регистрируйтесь по ссылке https://fxpro.com/signup?ref=group и пополняйте от 300", nil
		}
		require.Contains(t, run.ConversationContext.ReplyError, "https://fxpro.com/signup?ref=group")
		require.Contains(t, run.ConversationContext.ReplyError, "ref_link")
		return "Регистрируйтесь по этой ссылке: " + partnerLink + ", напишите, когда кабинет откроется", nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.linkAllowlist = testLinkAllowlist
	out, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "431", Messages: []InboundMessage{{Content: "счёта нет, как регистрироваться?", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Contains(t, out.Text, partnerLink)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "431", Actor{})
	require.NoError(t, err)
	history, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, out.Text, history[1].Content)
}
