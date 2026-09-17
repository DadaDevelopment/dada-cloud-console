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
