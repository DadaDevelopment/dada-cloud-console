package agentruntime

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	leakMaxRunes      = 700
	leakLatinShare    = 0.2
	leakLatinMinCount = 12
)

var leakMarkers = []string{
	"kb_search", "load_skill", "update_conversation_state", "escalate_to_operator", "stop_agent", "ask_user",
	"runtime_context", "incoming_messages", "incoming_text", "expected_version", "source_message_id",
	"reported_facts", "open_loops", "active_skills", "tool_call", "function_call",
	"итоговый ответ", "правила промпта", "по правилам промпта", "следующий вопрос о",
	"в kb нет", "нет в kb", "из kb", "kb ", "черновик",
}

var leakFramePattern = regexp.MustCompile(`(?i)(^|[^\pL])(к клиенту|итоговое|итог|правило|отвечаю|скажу|заметка|мысли|рассуждение):`)

var leakEnglishFillers = []string{"continue to", "hmm", "okay,", "fine.", "let me ", "the user ", "the client "}

var leakMarkerPattern = regexp.MustCompile(`(?i)\b(kb|skill|placeholder|internal)\b`)

var leakIdentifierPattern = regexp.MustCompile(`(?i:\b(discovery|price|offer|objection|continuity|registration|signals|learning|such)\b)|\bdeposit\b`)

var leakLinks = regexp.MustCompile(`https?:\/\/\S+|\S+@\S+\.\S+|@\w+`)

var leakTokenFragment = regexp.MustCompile(`\pL_|_\pL`)

var leakPlaceholder = regexp.MustCompile(`[\[{<]\s*\pL[\pL ]{1,30}\s*[\]}>]`)

var leakBrandTokens = regexp.MustCompile(`(?i)\b(fxpro|mt5|metatrader|usd|eur|rub|kyc|vpn|p2p|id|ok|pdf|ios|android|telegram|whatsapp|app ?store|google ?play|sbp|swift|iban|cvc|cvv|otp|sms|qr|api|url|pin)\b`)

func leakReason(reply string) string {
	text := strings.TrimSpace(reply)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, marker := range leakMarkers {
		if strings.Contains(lower, marker) {
			return "internal marker " + strings.TrimSpace(marker)
		}
	}
	if m := leakMarkerPattern.FindString(text); m != "" {
		return "internal marker " + strings.ToLower(m)
	}
	if m := leakFramePattern.FindStringSubmatch(text); m != nil {
		return "reasoning frame " + strings.ToLower(m[2]) + ":"
	}
	share, latin := latinShare(text)
	if share > 0 {
		for _, filler := range leakEnglishFillers {
			if strings.Contains(lower, filler) {
				return "english filler " + strings.TrimSpace(filler)
			}
		}
		if m := leakIdentifierPattern.FindString(leakLinks.ReplaceAllString(text, " ")); m != "" {
			return "internal identifier " + strings.ToLower(m)
		}
	}
	if leakTokenFragment.MatchString(leakLinks.ReplaceAllString(text, " ")) {
		return "token fragment with underscore"
	}
	if m := leakPlaceholder.FindString(text); m != "" {
		return "placeholder " + m
	}
	if utf8.RuneCountInString(text) > leakMaxRunes {
		return "reply longer than a client message"
	}
	if strings.Count(text, "\n\n") >= 3 && strings.Count(text, "«") >= 2 && strings.Contains(text, "?") && strings.Contains(text, "…") {
		return "several drafts in one reply"
	}
	if latin >= leakLatinMinCount && share > leakLatinShare {
		return "mixed latin and cyrillic script"
	}
	return ""
}

func latinShare(text string) (float64, int) {
	cleaned := leakBrandTokens.ReplaceAllString(leakLinks.ReplaceAllString(text, " "), " ")
	var latin, cyrillic int
	for _, r := range cleaned {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		}
	}
	if cyrillic == 0 || latin+cyrillic == 0 {
		return 0, 0
	}
	return float64(latin) / float64(latin+cyrillic), latin
}

const leakPlaceholderHint = "Предыдущий черновик клиенту не отправлен: вместо значения в нём стоял плейсхолдер %s. Подставь настоящее значение (ссылку и факты бери из kb_search, партнёрская ссылка в статье ref_link) или перепиши ответ без этого места. Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."

func leakRepairMessage(reason string) string {
	if strings.HasPrefix(reason, "link outside allowlist ") {
		return leakLinkRepairMessage(reason)
	}
	if strings.HasPrefix(reason, "placeholder ") {
		return fmt.Sprintf(leakPlaceholderHint, strings.TrimPrefix(reason, "placeholder "))
	}
	return fmt.Sprintf(leakRepairHint, reason)
}

const leakRepairHint = "Предыдущий черновик клиенту не отправлен: в нём были внутренние рассуждения или служебные слова (%s). Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски, без размышлений, черновиков, названий инструментов и правил."
