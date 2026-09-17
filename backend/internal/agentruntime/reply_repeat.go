package agentruntime

import (
	"fmt"
	"strings"
	"unicode"
)

const repeatHookMinWords = 3

// closingQuestion returns the hook of a reply, normalised for comparison:
// lower case, letters and digits only. The hook is the last sentence of the
// last blank-line part that ends with a question mark, so a series that
// closes with a link or a button after its question still carries the hook.
func closingQuestion(text string) string {
	text = strings.TrimSpace(text)
	for strings.Contains(text, "\n\n") && !strings.HasSuffix(text, "?") && !strings.HasSuffix(text, "？") {
		text = strings.TrimSpace(text[:strings.LastIndex(text, "\n\n")])
	}
	if !strings.HasSuffix(text, "?") && !strings.HasSuffix(text, "？") {
		return ""
	}
	start := strings.LastIndexAny(strings.TrimRight(text, "?？"), ".!?？\n")
	sentence := text[start+1:]
	words := 0
	var b strings.Builder
	inWord := false
	for _, r := range sentence {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			if !inWord {
				words++
				inWord = true
			}
			continue
		}
		inWord = false
	}
	if words < repeatHookMinWords {
		return ""
	}
	return b.String()
}

// repeatedHookReason reports a reply whose closing question is the same
// question, word for word (an added lead-in word does not count), that any
// assistant message in the recent history already ended with. Operators never
// ask a slot twice in the same words within one dialog; the model does, and
// clients read that as a script.
func repeatedHookReason(reply string, history []Message) string {
	question := closingQuestion(reply)
	if question == "" {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		if earlier := closingQuestion(history[i].Content); earlier != "" && (strings.HasSuffix(question, earlier) || strings.HasSuffix(earlier, question)) {
			return "closing question repeats an earlier message word for word"
		}
	}
	return ""
}

const repeatRepairHint = "Предыдущий черновик клиенту не отправлен: он заканчивался тем же вопросом, что и твоё прошлое сообщение, слово в слово. Тот же слот спроси другими словами из ряда phrasing, а если клиент этот вопрос уже дважды пропустил - ответь на его реплику без вопроса и вернись к слоту через ход. Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."

// languageMismatchReason reports a reply with no Cyrillic at all when the
// client's own batch was written in Cyrillic. The reply language follows the
// latest client message, not an earlier branch of the conversation.
func languageMismatchReason(reply string, pending []Message) string {
	var clientCyrillic, clientLatin int
	for _, m := range pending {
		for _, r := range m.Content {
			switch {
			case unicode.Is(unicode.Cyrillic, r):
				clientCyrillic++
			case unicode.Is(unicode.Latin, r):
				clientLatin++
			}
		}
	}
	if clientCyrillic == 0 || clientCyrillic < clientLatin {
		return ""
	}
	cleaned := leakBrandTokens.ReplaceAllString(leakLinks.ReplaceAllString(reply, " "), " ")
	var latin, cyrillic int
	for _, r := range cleaned {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	if cyrillic == 0 && latin >= leakLatinMinCount {
		return fmt.Sprintf("reply has no cyrillic (%d latin letters) while the client wrote in russian", latin)
	}
	return ""
}

const languageRepairHint = "Предыдущий черновик клиенту не отправлен: клиент пишет по-русски, а черновик был на другом языке. Язык ответа - язык последнего сообщения клиента, о смене языка не сообщай. Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."
