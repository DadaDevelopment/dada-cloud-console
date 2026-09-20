package agentjudge

import "testing"

func TestClientRegister(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  string
	}{
		{"ty pronoun", []string{"ты на связи?"}, RegisterTy},
		{"vy pronoun", []string{"Здравствуйте, вы ещё набираете?"}, RegisterVy},
		{"latest wins", []string{"вы кто?", "ок, а ты сам торгуешь?"}, RegisterTy},
		{"skip neutral", []string{"тебе норм?", "ок", "да"}, RegisterTy},
		{"no markers", []string{"хочу в канал", "300"}, ""},
		{"привет is ty", []string{"Привет, хочу попасть в канал"}, RegisterTy},
		{"здравствуйте is vy", []string{"Здравствуйте, хочу в канал"}, RegisterVy},
		{"подскажите is vy", []string{"подскажите, какой депозит нужен?"}, RegisterVy},
		{"slang is ty", []string{"хз, тыщ 50 наверное"}, RegisterTy},
		{"both in one skipped", []string{"вы или ты?"}, ""},
		{"вывод is not вы", []string{"вывод денег быстрый?"}, ""},
		{"team possessive keeps ty", []string{"Привет, хочу в канал", "Да, по вашей ссылке и регистрировался"}, RegisterTy},
		{"team possessive alone is vy", []string{"хочу в канал", "ваш канал платный?"}, RegisterVy},
		{"person pronoun still flips", []string{"Привет", "Подскажите, вы платите за сигналы?"}, RegisterVy},
	}
	for _, c := range cases {
		if got := ClientRegister(c.texts); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestRegisterMismatch(t *testing.T) {
	cases := []struct {
		name     string
		reply    string
		register string
		want     string
	}{
		{"давайте to ty client", "Ты на связи? Давайте продолжим, я всё объясню", RegisterTy, "давайте"},
		{"вам to ty client", "Скину вам ссылку", RegisterTy, "вам"},
		{"можете to ty client", "Можете написать сумму", RegisterTy, "можете"},
		{"clean ty", "Ты на связи? Давай продолжим, всё объясню", RegisterTy, ""},
		{"ты to vy client", "Подскажи, ты уже торговал?", RegisterVy, "ты"},
		{"davai to vy client", "Давай я расскажу как вступить", RegisterVy, "давай"},
		{"2sg verb to vy client", "Сможешь пополнить сегодня?", RegisterVy, "сможешь"},
		{"привет to vy client", "Привет! Набор ограничен", RegisterVy, "привет"},
		{"clean vy", "Подскажите, вип-канал ещё актуален для вас?", RegisterVy, ""},
		{"nouns with ите are fine", "В личном кабинете на сайте счёт виден, на депозите 300", RegisterTy, ""},
		{"лишь is not a verb", "Нужно лишь пройти регистрацию", RegisterVy, ""},
		{"вывод not вы", "Вывод средств занимает день", RegisterTy, ""},
		{"unknown register never flags", "Давайте, ты", "", ""},
	}
	for _, c := range cases {
		if got := RegisterMismatch(c.reply, c.register); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestCheckRegisterUsesHistory(t *testing.T) {
	turn := Turn{
		History:  []Exchange{{Role: "client", Text: "привет, ты кто?"}, {Role: "roman", Text: "Я Роман"}},
		Incoming: []string{"ок"},
		Reply:    "Давайте продолжим",
	}
	hit, why := checkRegister(Criterion{}, turn)
	if !hit || why == "" {
		t.Fatalf("expected register violation, got %v %q", hit, why)
	}
	turn.Reply = "Давай продолжим"
	if hit, _ := checkRegister(Criterion{}, turn); hit {
		t.Fatalf("clean ty reply flagged")
	}
	if hit, _ := checkRegister(Criterion{}, Turn{Incoming: []string{"ок"}, Reply: "Давайте"}); hit {
		t.Fatalf("unknown register flagged")
	}
}
