package agentjudge

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Exchange is one earlier message of the dialog shown to the LLM as history.
type Exchange struct {
	Role string
	Text string
}

// Turn is everything a judge run needs about one delivered turn.
type Turn struct {
	TraceID            string
	ObservationID      string
	Username           string
	Incoming           []string
	Reply              string
	Parts              []string
	History            []Exchange
	Context            string
	NoQuestionThisTurn bool
	ReplyError         bool
}

func (t Turn) messages() []string {
	if len(t.Parts) > 0 {
		return t.Parts
	}
	return []string{t.Reply}
}

type codeCheck func(c Criterion, t Turn) (bool, string)

var codeChecks = map[string]codeCheck{
	"forbid_words":   checkForbidWords,
	"forbid_opener":  checkForbidOpener,
	"max_questions":  checkMaxQuestions,
	"max_length":     checkMaxLength,
	"form":           checkForm,
	"language_match": checkLanguageMatch,
	"register":       checkRegister,
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// findWord reports the first stop word that starts a word of text; words are
// matched as case-insensitive prefixes so a stem covers its inflections.
func findWord(text string, words []string) string {
	lower := strings.ToLower(text)
	runes := []rune(lower)
	for _, w := range words {
		needle := []rune(strings.ToLower(strings.TrimSpace(w)))
		if len(needle) == 0 {
			continue
		}
		for i := 0; i+len(needle) <= len(runes); i++ {
			if i > 0 && isWordRune(runes[i-1]) {
				continue
			}
			if string(runes[i:i+len(needle)]) == string(needle) {
				end := i + len(needle)
				for end < len(runes) && isWordRune(runes[end]) {
					end++
				}
				return string(runes[i:end])
			}
		}
	}
	return ""
}

func checkForbidWords(c Criterion, t Turn) (bool, string) {
	if hit := findWord(t.Reply, c.Words); hit != "" {
		return true, "«" + hit + "»"
	}
	return false, ""
}

func firstWords(text string, n int) string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isWordRune(r) })
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func checkForbidOpener(c Criterion, t Turn) (bool, string) {
	for _, m := range t.messages() {
		for _, w := range c.Words {
			w = strings.ToLower(strings.TrimSpace(w))
			n := len(strings.Fields(w))
			if n > 0 && firstWords(m, n) == w {
				return true, "начинается с «" + w + "»"
			}
		}
	}
	return false, ""
}

func checkMaxQuestions(c Criterion, t Turn) (bool, string) {
	count := strings.Count(reURL.ReplaceAllString(t.Reply, " "), "?")
	limit := c.Max
	if limit <= 0 {
		limit = 1
	}
	if t.NoQuestionThisTurn {
		limit = 0
	}
	if count > limit {
		return true, fmt.Sprintf("%d вопросительных знаков при лимите %d", count, limit)
	}
	return false, ""
}

func checkMaxLength(c Criterion, t Turn) (bool, string) {
	total := 0
	for _, m := range t.messages() {
		n := len([]rune(strings.TrimSpace(m)))
		total += n
		if c.Message > 0 && n > c.Message {
			return true, fmt.Sprintf("сообщение %d знаков при лимите %d", n, c.Message)
		}
	}
	if c.Turn > 0 && total > c.Turn {
		return true, fmt.Sprintf("ход %d знаков при лимите %d", total, c.Turn)
	}
	return false, ""
}

var (
	reListMarker = regexp.MustCompile(`(?m)^\s*([-*•]|\d+[.)])\s+`)
	reHeader     = regexp.MustCompile(`(?m)^\s*#{1,6}\s`)
	reBold       = regexp.MustCompile(`\*\*[^*\n]+\*\*|__[^_\n]+__`)
)

func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x1F000 && r <= 0x1F2FF, r == 0xFE0F, r == 0x200D:
		return true
	}
	return false
}

// checkForm flags the delivery form the script forbids; “allow“ on the
// criterion lists exceptions: emoji as themselves («👍») and the word «list»
// for bullet lists, both of which the lead's verbatim replies carry.
func checkForm(c Criterion, t Turn) (bool, string) {
	allowed := map[string]bool{}
	for _, a := range c.Allow {
		allowed[strings.TrimSpace(a)] = true
	}
	for _, m := range t.messages() {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		for _, r := range m {
			if isEmoji(r) && !allowed[string(r)] && r != 0xFE0F && r != 0x200D {
				return true, "эмодзи " + string(r)
			}
		}
		if strings.ContainsRune(m, '—') {
			return true, "длинное тире"
		}
		if strings.HasSuffix(m, ".") && !strings.HasSuffix(m, "...") {
			return true, "точка в конце сообщения"
		}
		if mk := reListMarker.FindStringSubmatch(m); mk != nil && !allowed[mk[1]] {
			return true, "маркированный список"
		}
		if reHeader.MatchString(m) {
			return true, "заголовок"
		}
		if reBold.MatchString(m) {
			return true, "жирный текст"
		}
	}
	return false, ""
}

var reURL = regexp.MustCompile(`https?:\/\/\S+|@\w+`)

func scriptShare(text string) (cyr, lat int) {
	for _, r := range reURL.ReplaceAllString(text, " ") {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	return
}

func checkLanguageMatch(c Criterion, t Turn) (bool, string) {
	if len(t.Incoming) == 0 {
		return false, ""
	}
	inCyr, inLat := scriptShare(t.Incoming[len(t.Incoming)-1])
	if inCyr+inLat < 12 {
		return false, ""
	}
	outCyr, outLat := scriptShare(t.Reply)
	if outCyr+outLat < 12 {
		return false, ""
	}
	clientCyrillic := inCyr > inLat*2
	clientLatin := inLat > inCyr*2
	replyCyrillic := outCyr > outLat*2
	replyLatin := outLat > outCyr*2
	if clientCyrillic && replyLatin {
		return true, "клиент пишет кириллицей, ответ латиницей"
	}
	if clientLatin && replyCyrillic {
		return true, "клиент пишет латиницей, ответ кириллицей"
	}
	return false, ""
}
