package agentruntime

import (
	"context"
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
		reason := linkLeakReason(reply, testLinkAllowlist)
		require.NotEmpty(t, reason, name)
		require.Contains(t, reason, "link outside allowlist ", name)
	}
	require.Equal(t, "link outside allowlist https://fxpro.com/signup?ref=group", linkLeakReason(cases["link before question"], testLinkAllowlist))
}

func TestLinkLeakReasonPassesKnowledgeBaseLinks(t *testing.T) {
	cases := map[string]string{
		"partner link":              "Регистрируйтесь по этой ссылке: " + partnerLink + ", напишите, когда кабинет откроется",
		"partner link before comma": "Ссылка: " + partnerLink + ", открывайте кабинет",
		"stats sheet":               "Статистика группы: https://docs.google.com/spreadsheets/d/1tU4ZDuGIkwqWHrOVtzaOKvVsLDHf5THVZyahu-y3QTE/edit?usp=sharing",
		"channel post":              "Обзор здесь: https://t.me/robinhoodmetals/58",
		"support mail":              "По спорным платежам пишите на support@fxpro.com. Депозит уже виден?",
		"partner mail":              "Привязку вы уже запросили у FxPro на cis@fxpro.com, остаётся ждать ответ",
		"brand without link":        "Счёт у FxPro уже открыт? Регистрация занимает пару минут",
		"amount with dot":           "Баланс 500.52 доллара после прогрева это штатно",
		"handle":                    "Куратор напишет вам с аккаунта @fxpro_curator",
		"empty":                     "",
	}
	for name, reply := range cases {
		require.Empty(t, linkLeakReason(reply, testLinkAllowlist), name)
	}
}

func TestLinkLeakReasonDisabledWithoutAllowlist(t *testing.T) {
	require.Empty(t, linkLeakReason("Ссылка https://fxpro.com/signup?ref=group", nil))
	require.Empty(t, ParseLinkAllowlist(" , "))
}

func TestLeakRepairMessageNamesTheFabricatedLink(t *testing.T) {
	hint := leakRepairMessage(linkLeakReason("Регистрируйтесь: https://fxpro.com/signup?ref=group", testLinkAllowlist))
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
