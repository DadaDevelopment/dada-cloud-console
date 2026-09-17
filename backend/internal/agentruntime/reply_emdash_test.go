package agentruntime

import "testing"

func TestStripEmDash_S17LinkClausePause(t *testing.T) {
	in := "Регистрируйтесь по нашей партнёрской ссылке: https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web — откройте счёт у FxPro прямо на этом сайте. Как кабинет откроется, напишите, дальше зайдём на ваши 800 и подберём шаги к цели в 1500 в месяц"
	want := "Регистрируйтесь по нашей партнёрской ссылке: https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web, откройте счёт у FxPro прямо на этом сайте. Как кабинет откроется, напишите, дальше зайдём на ваши 800 и подберём шаги к цели в 1500 в месяц"
	if got := stripEmDash(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStripEmDash_S428Copula(t *testing.T) {
	in := "Свой счёт у FxPro — отлично. Дальше проходите верификацию в «Профиле», если ещё не сделана, и пополняете счёт из кабинета на свою сумму, деньги идут на ваш собственный счёт у брокера. Верификация уже пройдена?"
	want := "Свой счёт у FxPro, отлично. Дальше проходите верификацию в «Профиле», если ещё не сделана, и пополняете счёт из кабинета на свою сумму, деньги идут на ваш собственный счёт у брокера. Верификация уже пройдена?"
	if got := stripEmDash(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStripEmDash_NoDashLeavesTextUntouched(t *testing.T) {
	text := "Открываете счёт у FxPro по этой ссылке, после подтверждения появится кабинет"
	if got := stripEmDash(text); got != text {
		t.Fatalf("got %q, want unchanged %q", got, text)
	}
}

func TestStripEmDash_DashBeforeSentenceEndCollapsesComma(t *testing.T) {
	if got := stripEmDash("Гарантий нет —."); got != "Гарантий нет." {
		t.Fatalf("got %q", got)
	}
}

func TestStripEmDash_LeadingDashStripped(t *testing.T) {
	if got := stripEmDash("— отлично, продолжаем"); got != "отлично, продолжаем" {
		t.Fatalf("got %q", got)
	}
}

func TestStripEmDash_MultipleDashesAllFixed(t *testing.T) {
	if got := stripEmDash("Счёт есть — отлично, ссылка не нужна — сразу верификация"); got != "Счёт есть, отлично, ссылка не нужна, сразу верификация" {
		t.Fatalf("got %q", got)
	}
}

func TestStripEmDash_AppliesBeforeLeakCheck(t *testing.T) {
	reply := stripEmDash("Свой счёт у FxPro — отлично, дальше верификация")
	if reason := leakReason(reply); reason != "" {
		t.Fatalf("unexpected leak reason after em dash strip: %s", reason)
	}
}

// A hyphen between words is the same canned-line tell as the em dash it
// replaces once core.md bans the dash itself.
func TestStripEmDash_SpacedHyphenBetweenWordsBecomesComma(t *testing.T) {
	if got := stripEmDash("Развод - это когда деньги уходят"); got != "Развод, это когда деньги уходят" {
		t.Fatalf("got %q", got)
	}
}

func TestStripEmDash_SpacedHyphenAfterSmileyBracket(t *testing.T) {
	if got := stripEmDash("Счёт есть) - отлично, дальше верификация"); got != "Счёт есть), отлично, дальше верификация" {
		t.Fatalf("got %q", got)
	}
}

func TestStripEmDash_EverySpacedHyphenIsFixed(t *testing.T) {
	if got := stripEmDash("Счёт есть - отлично, ссылка не нужна - сразу верификация"); got != "Счёт есть, отлично, ссылка не нужна, сразу верификация" {
		t.Fatalf("got %q", got)
	}
}

// The cases the rewrite must leave alone: a numeric range, a compound word and
// a doubled particle.
func TestStripEmDash_HyphenKeptWhereItIsNotADash(t *testing.T) {
	for _, text := range []string{
		"Депозит 300 - 500 USD",
		"Напишите на e-mail, ответим в тот же день",
		"На вопрос про плечо ответ не-не, настройки по умолчанию",
		"Порог 300-500 долларов",
		"Спишитесь с куратором 1 - 2 раза",
	} {
		if got := stripEmDash(text); got != text {
			t.Fatalf("got %q, want unchanged %q", got, text)
		}
	}
}

func TestStripEmDash_EmDashAndHyphenInOneReply(t *testing.T) {
	in := "Счёт есть — отлично, депозит 300 - 500, верификация - следующий шаг"
	want := "Счёт есть, отлично, депозит 300 - 500, верификация, следующий шаг"
	if got := stripEmDash(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
