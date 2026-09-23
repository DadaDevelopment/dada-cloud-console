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

// emDashPattern matches an em dash together with its surrounding spaces.
// core.md bans em dashes several times over (they read as a canned line), but
// the ban does not reliably hold across every reply (S17, S428 n43: 2/27).
// A comma is the closest natural substitute for both uses seen in practice -
// a pause before a clause and a copula ("X - Y") - so this is a mechanical
// backstop rather than a reject-and-reprompt, the same tradeoff as the
// client_message gender fix in escalation.go: a safe rewrite beats added
// round-trip latency for a purely cosmetic defect.
var emDashPattern = regexp.MustCompile(`\s*—\s*`)

func stripEmDash(text string) string {
	if !strings.Contains(text, "—") {
		return text
	}
	replaced := emDashPattern.ReplaceAllString(text, ", ")
	replaced = strings.TrimLeft(replaced, ", ")
	for _, pair := range [][2]string{
		{", .", "."}, {", ,", ","}, {", ?", "?"}, {", !", "!"}, {",  ", ", "},
	} {
		replaced = strings.ReplaceAll(replaced, pair[0], pair[1])
	}
	return strings.TrimSpace(replaced)
}

var leakMarkers = []string{
	"kb_search", "load_skill", "update_conversation_state", "escalate_to_operator", "stop_agent", "ask_user",
	"runtime_context", "incoming_messages", "incoming_text", "expected_version", "source_message_id",
	"reported_facts", "open_loops", "active_skills", "tool_call", "function_call",
	"итоговый ответ", "правила промпта", "по правилам промпта", "следующий вопрос о",
	"в kb нет", "нет в kb", "из kb", "kb ", "черновик",
}

var leakFramePattern = regexp.MustCompile(`(?i)(^|[^\pL])(к клиенту|итоговое|итог|правило|отвечаю|скажу|заметка|мысли|рассуждение|по s\d{1,2}[a-zа-я]?|этап s\d{1,2}[a-zа-я]?|s\d{1,2}[a-zа-я]?):`)

var leakStagePattern = regexp.MustCompile(`(?i)(^|[^\pL])(по|этап|этапу|шаг|ход|дальше|реплика|переход к)\s+S\d{1,2}[a-zа-я]?($|[^\pL\d])`)

// leakStageChainPattern catches stage codes without a leading word: a route
// written as a chain («S5 → S6 → S7», t14 native.71 r1 sent it as its own
// message) or a code standing alone on a line.
var leakStageChainPattern = regexp.MustCompile(`(?im)(^|[^\pL\d])S\d{1,2}[a-zа-я]?\s*(→|->|=>|⇒)|^\s*S\d{1,2}[a-zа-я]?\s*$`)

var leakThirdPersonPattern = regexp.MustCompile(`(?i)(^|[^\pL])((он|она|клиент)\s+((уже|сам|сама|тоже|ранее|раньше|только что)\s+)?(назвал|назвала|сказал|сказала|написал|написала|спросил|спросила|ответил|ответила|прислал|прислала|пополнил|пополнила|зарегистрировался|зарегистрировалась|хочет|готов|готова|решил|решила|торговал|торговала))($|[^\pL])`)

// leakPlanningPattern catches the model narrating its own decision instead of
// talking to the client (t10 native.70 r1: «500 выше порога, вопрос о счёте не
// задаём (ссылка уже у него): «Получилось пройти регистрацию? »»). Three
// tells: threshold talk, a negated first-person-plural plan verb, or a
// parenthesised aside followed by a quoted script line.
var leakPlanningPattern = regexp.MustCompile(`(?i)(^|[^\pL])((выше|ниже)\s+порога|не\s+(задаём|задаю)|у\s+(него|неё)\s+уже)($|[^\pL])|\)\s*:\s*«`)

// leakTierPattern catches the internal deposit-tier bucket read out to the
// client instead of the scripted line for it (P0-4, эвал 20.09: «300к = 300
// 000 ₽/мес, тир 200-500 тыс.», «Это тир от 200 тыс. до 500 тыс.:»). The tier
// math stays in the model's head; the client only ever gets Roman's line for
// that bracket. \b does not follow Cyrillic in Go's RE2, so both boundaries
// are spelled out explicitly instead of relying on it.
var leakTierPattern = regexp.MustCompile(`(?i)(^|[^\pL])тир(ы|а|е|ов)?\s+(от\s+)?\d[\d\s\-]*\s*(тыс|к|000)($|[^\pL])`)

var leakEnglishFillers = []string{"continue to", "hmm", "okay,", "fine.", "let me ", "the user ", "the client "}

var leakMarkerPattern = regexp.MustCompile(`(?i)\b(kb|skill|placeholder|internal)\b`)

var leakIdentifierPattern = regexp.MustCompile(`(?i:\b(discovery|price|offer|objection|continuity|registration|phrasing|signals|learning|such)\b)|\bdeposit\b`)

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
	if m := leakStagePattern.FindString(text); m != "" {
		return "script stage code " + strings.TrimSpace(m)
	}
	if m := leakStageChainPattern.FindString(text); m != "" {
		return "script stage code " + strings.TrimSpace(m)
	}
	if m := leakThirdPersonPattern.FindStringSubmatch(text); m != nil {
		return "third person about the client «" + strings.ToLower(m[2]) + "»"
	}
	if m := leakPlanningPattern.FindString(text); m != "" {
		return "planning talk «" + strings.TrimSpace(m) + "»"
	}
	if m := leakTierPattern.FindString(text); m != "" {
		return "internal deposit tier " + strings.TrimSpace(m)
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
	if longestReplyPart(text) > leakMaxRunes {
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

// longestReplyPart measures the longest message the client would actually
// receive: a series is cut on separator lines before delivery, so the length
// guard applies per delivered message, not to the whole draft.
func longestReplyPart(text string) int {
	parts := capReplyParts(splitReplyParts(text))
	if len(parts) == 0 {
		return utf8.RuneCountInString(text)
	}
	longest := 0
	for _, part := range parts {
		if n := utf8.RuneCountInString(part); n > longest {
			longest = n
		}
	}
	return longest
}
