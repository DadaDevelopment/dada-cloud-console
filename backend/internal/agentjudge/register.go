package agentjudge

import (
	"regexp"
	"strings"
)

// Register is the form of address a Russian speaker uses: RegisterTy for the
// familiar «ты», RegisterVy for the polite «вы». Empty means undetermined.
const (
	RegisterTy = "ty"
	RegisterVy = "vy"
)

// Word boundaries are spelled out because RE2's \b is ASCII-only and does not
// stop at Cyrillic letters.
var (
	tyPronounPattern = regexp.MustCompile(`(?i)(^|[^\pL])(ты|тебя|тебе|тобой|твой|твоя|твои|твоё|твое|твоего|твоей|твою|твоим|твоём|твоем)($|[^\pL])`)
	vyPronounPattern = regexp.MustCompile(`(?i)(^|[^\pL])(вы|вас|вам|вами|ваш|ваша|ваши|ваше|вашего|вашей|вашу|вашим|вашем|ваших|вашими)($|[^\pL])`)

	tyVerbPattern = regexp.MustCompile(`(?i)(^|[^\pL])(\pL{2,}(ешь|ёшь|ишь)|привет|здарова|дарова|давай|подскажи|напиши|скажи|расскажи|смотри|посмотри|пиши|держи|отправь|скинь|попробуй|заходи|пройди|зарегистрируйся|пополни|напомни|спрашивай|обращайся|подумай|решай|сообщи|дай|знай|поверь|учти|имей)($|[^\pL])`)
	vyVerbPattern = regexp.MustCompile(`(?i)(^|[^\pL])(\pL{3,}(йте|йтесь)|здравствуйте|можете|хотите|будете|сможете|знаете|понимаете|планируете|торгуете|пополняете|решите|сообщите|дайте|решайте|учтите|имейте)($|[^\pL])`)

	clientTyPattern = regexp.MustCompile(`(?i)(^|[^\pL])(привет|здарова|дарова|здаров|хай|чо|чё|тыщ|хз|канеш|спс|дай|скинь|слушай|давай|подскажи|скажи|расскажи|напиши|кинь|отправь|смотри)($|[^\pL])`)
	clientVyPattern = regexp.MustCompile(`(?i)(^|[^\pL])(здравствуйте|\pL{3,}(йте|йтесь)|подскажите|скажите|напишите|расскажите|скиньте|отправьте|помогите|объясните|пришлите|ответьте)($|[^\pL])`)
)

// ClientRegister derives how the client addresses Roman from the client's own
// messages, newest first: the first message with an unambiguous marker wins.
// The markers follow the core.md rule: «вы» by default, «ты» once the client
// writes «ты», slang or a singular imperative («привет, дай ссылку»). Second
// person verbs are not read on the client side because «сделаешь» may be about
// a third person.
func ClientRegister(clientTexts []string) string {
	for i := len(clientTexts) - 1; i >= 0; i-- {
		text := clientTexts[i]
		ty := tyPronounPattern.MatchString(text) || clientTyPattern.MatchString(text)
		vy := vyPronounPattern.MatchString(text) || clientVyPattern.MatchString(text)
		switch {
		case ty && !vy:
			return RegisterTy
		case vy && !ty:
			return RegisterVy
		}
	}
	return ""
}

// RegisterMismatch reports the first word of reply that addresses the client
// in the other register. Quoted fragments count too: the client reads them.
func RegisterMismatch(reply, register string) string {
	switch register {
	case RegisterTy:
		if m := vyPronounPattern.FindStringSubmatch(reply); m != nil {
			return strings.ToLower(m[2])
		}
		if m := vyVerbPattern.FindStringSubmatch(reply); m != nil {
			return strings.ToLower(m[2])
		}
	case RegisterVy:
		if m := tyPronounPattern.FindStringSubmatch(reply); m != nil {
			return strings.ToLower(m[2])
		}
		if m := tyVerbPattern.FindStringSubmatch(reply); m != nil {
			return strings.ToLower(m[2])
		}
	}
	return ""
}

func checkRegister(c Criterion, t Turn) (bool, string) {
	var client []string
	for _, e := range t.History {
		if e.Role == "client" {
			client = append(client, e.Text)
		}
	}
	client = append(client, t.Incoming...)
	register := ClientRegister(client)
	if register == "" {
		return false, ""
	}
	if word := RegisterMismatch(t.Reply, register); word != "" {
		if register == RegisterTy {
			return true, "клиент на «ты», в ответе «" + word + "»"
		}
		return true, "клиент на «вы», в ответе «" + word + "»"
	}
	return false, ""
}
