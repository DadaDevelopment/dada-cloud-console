package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClosingQuestionNormalisesTheLastSentence(t *testing.T) {
	require.Equal(t, "сколькосейчассвободныхденегназаход", closingQuestion("Понял вас. Сколько сейчас свободных денег на заход?"))
	require.Equal(t, "сколькосейчассвободныхденегназаход", closingQuestion("сколько   сейчас свободных денег на заход?"))
	require.Equal(t, "", closingQuestion("Сколько сейчас свободных денег на заход? Жду"), "question in the middle is not a hook")
	require.Equal(t, "", closingQuestion("Стартуем?"), "short closes are allowed to repeat")
	require.Equal(t, "", closingQuestion("Договорились, жду"))
	require.Equal(t, "сколькосейчассвободныхденегназаход", closingQuestion("Вход от 300 USD.\n\nСколько сейчас свободных денег на заход?\n\n[[REQUEST_LOCATION_BUTTON]]"), "series: hook lives in the part before the button")
	require.Equal(t, "сколькосейчассвободныхденегназаход", closingQuestion("Сколько сейчас свободных денег на заход?\n\nhttps://direct.fxpro.com/ib/ru/usd/ABCDEFGH"), "series: hook before the link")
	require.Equal(t, "", closingQuestion("Вход от 300 USD.\n\nЖду"), "series without a question has no hook")
}

func TestRepeatedHookReasonLooksAtEveryAssistantMessageInHistory(t *testing.T) {
	history := []Message{
		{Role: "assistant", Content: "Порог 300 USD. Сколько сейчас свободных денег на заход?"},
		{Role: "user", Content: "а что за группа"},
		{Role: "assistant", Content: "Закрытый клуб с сигналами куратора. С какой суммой готовы зайти?"},
		{Role: "user", Content: "подумаю"},
	}
	require.Contains(t, repeatedHookReason("Без проблем. Сколько сейчас свободных денег на заход?", history), "word for word")
	require.Contains(t, repeatedHookReason("Хорошо, с какой суммой готовы зайти?", history), "word for word")
	require.Equal(t, "", repeatedHookReason("Без проблем. Какую сумму готовы выделить на первый депозит?", history))
	require.Equal(t, "", repeatedHookReason("Жду", history))
	older := append([]Message{{Role: "assistant", Content: "Был опыт на форексе или с нуля?"}, {Role: "user", Content: "нет"}}, history...)
	require.Contains(t, repeatedHookReason("Был опыт на форексе или с нуля?", older), "word for word", "a hook from three messages back is still the same dialog")
	require.Contains(t, repeatedHookReason("Вход от 300 USD.\n\nСколько сейчас свободных денег на заход?\n\n[[REQUEST_LOCATION_BUTTON]]", history), "word for word", "series hook repeats an earlier hook")
	require.Equal(t, "", repeatedHookReason("Ок, пиши как решишь", history))
}

func TestLanguageMismatchReasonFollowsTheClientBatch(t *testing.T) {
	ru := []Message{{Role: "user", Content: "ок, напишу позже"}}
	en := []Message{{Role: "user", Content: "ok, I will write later"}}
	require.Contains(t, languageMismatchReason("No problem, I will be here. Do you already have an FxPro account?", ru), "no cyrillic")
	require.Equal(t, "", languageMismatchReason("Без проблем, работаю на английском", ru), "russian text about english is a prompt matter, not a script mismatch")
	require.Equal(t, "", languageMismatchReason("No problem, I will be here", en))
	require.Equal(t, "", languageMismatchReason("Жду. https://direct.fxpro.com/ib/ru/usd/ABCDEFGH", ru))
	require.Equal(t, "", languageMismatchReason("Жду", ru))
}

func TestPGRepeatedHookIsRewrittenOnceThenDelivered(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "repeat-" + uuid.NewString()
	hook := "Порог 300 USD. Сколько сейчас свободных денег на заход?"
	calls := 0
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		calls++
		switch calls {
		case 1:
			return hook, nil
		case 2:
			require.Empty(t, run.ConversationContext.ReplyError)
			return "Без проблем. " + hook[strings.Index(hook, "Сколько"):], nil
		default:
			require.Contains(t, run.ConversationContext.ReplyError, "слово в слово")
			return hook, nil
		}
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	first, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "7", Messages: []InboundMessage{{Content: "сколько нужно", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Equal(t, hook, first.Text)
	second, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "7", Messages: []InboundMessage{{Content: "подумаю", ChannelMessageID: "2"}}})
	require.NoError(t, err)
	require.Equal(t, 3, calls, "one rewrite, then the draft is delivered even if it still repeats")
	require.Equal(t, hook, second.Text)
}
