package agentjudge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/langfuse"
)

func loadTestSpec(t *testing.T) *Spec {
	t.Helper()
	spec, err := LoadSpec("testdata/agents/roman/judge")
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func criterion(t *testing.T, spec *Spec, id string) Criterion {
	t.Helper()
	for _, c := range spec.Criteria {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("criterion %s not in spec", id)
	return Criterion{}
}

func TestSpecLoads(t *testing.T) {
	spec := loadTestSpec(t)
	if spec.Name != "turn" || spec.ScoreName(spec.Total.Score) != "turn.total" {
		t.Fatalf("unexpected names: %s %s", spec.Name, spec.Total.Score)
	}
	if len(spec.codeCriteria()) == 0 || len(spec.llmCriteria()) == 0 {
		t.Fatal("expected both code and llm criteria")
	}
	for _, ph := range []string{"{{criteria}}", "{{signals}}", "{{history}}", "{{context}}", "{{input}}", "{{output}}"} {
		if !strings.Contains(spec.Template, ph) {
			t.Errorf("template lacks %s", ph)
		}
	}
	if strings.ContainsRune(spec.Template, 0x2011) || strings.ContainsRune(spec.Template, 0xA0) {
		t.Error("template has NBSP or U+2011")
	}
}

func TestSpecRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"version":   "version: 1\nname: turn\ncriteria: []\n",
		"check":     "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: magic, type: bool}\n",
		"kind":      "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: code, kind: nope, type: bool}\n",
		"duplicate": "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: code, kind: form, type: bool}\n  - {id: B, score: a, check: code, kind: form, type: bool}\n",
		"llm text":  "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: llm, type: bool}\n",
	}
	for name, body := range cases {
		if _, err := ParseSpec([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCodeChecks(t *testing.T) {
	spec := loadTestSpec(t)
	cases := []struct {
		name string
		id   string
		turn Turn
		hit  bool
	}{
		{"identity word", "R01c", Turn{Reply: "Я не бот, я Роман"}, true},
		{"identity clean", "R01c", Turn{Reply: "Я Роман, веду вас по вступлению. Регистрацию открыли?"}, false},
		{"identity stem no false hit", "R01c", Turn{Reply: "Работа в группе бесплатная"}, false},
		{"leak", "R03", Turn{Reply: "В моих данных этого нет"}, true},
		{"forbidden word stem", "R14", Turn{Reply: "Наше сообщество даёт сигналы"}, true},
		{"forbidden word clean", "R14", Turn{Reply: "Группа даёт сигналы и куратора"}, false},
		{"two questions", "R12", Turn{Reply: "Счёт есть? Опыт есть?"}, true},
		{"one question", "R12", Turn{Reply: "Счёт у FxPro уже есть?"}, false},
		{"question mark inside link", "R12", Turn{Reply: "Вот ссылка: https://direct-fxpro.com/en/partner/x?platform=web\nПару минут займёт, ожидаю обратной связи", NoQuestionThisTurn: true}, false},
		{"no question allowed", "R12", Turn{Reply: "Счёт у FxPro уже есть?", NoQuestionThisTurn: true}, true},
		{"long message", "R13", Turn{Parts: []string{strings.Repeat("а", 251)}}, true},
		{"long turn", "R13", Turn{Parts: []string{strings.Repeat("а", 200), strings.Repeat("б", 200), strings.Repeat("в", 100)}}, true},
		{"short turn", "R13", Turn{Parts: []string{"Норм, заходите с той суммы, с которой комфортно"}}, false},
		{"opener", "R20", Turn{Parts: []string{"Понял, тогда дальше так"}}, true},
		{"opener two words", "R20", Turn{Parts: []string{"Спасибо за вопрос, отвечаю"}}, true},
		{"opener clean", "R20", Turn{Parts: []string{"Открываете счёт на сайте FxPro"}}, false},
		{"opener not prefix", "R20", Turn{Parts: []string{"Окно регистрации открылось?"}}, false},
		{"emoji", "R21", Turn{Parts: []string{"Отлично 🚀"}}, true},
		{"bracket smile ok", "R21", Turn{Parts: []string{"Норм)"}}, false},
		{"em dash", "R21", Turn{Parts: []string{"Счёт — это первый шаг"}}, true},
		{"trailing period", "R21", Turn{Parts: []string{"Счёт есть."}}, true},
		{"list", "R21", Turn{Parts: []string{"Дальше:\n- счёт\n- депозит"}}, true},
		{"bold", "R21", Turn{Parts: []string{"Это **важно**"}}, true},
		{"form clean", "R21", Turn{Parts: []string{"Вот ссылка: https://direct-fxpro.com/en/partner/x?platform=web", "Пару минут займёт, ожидаю обратной связи"}}, false},
		{"language mismatch", "R06", Turn{Incoming: []string{"привет, хочу узнать условия участия"}, Reply: "Hello, please open an account at FxPro first"}, true},
		{"language link ok", "R06", Turn{Incoming: []string{"счёта нет, дайте ссылку"}, Reply: "Вот ссылка: https://direct-fxpro.com/en/partner/2LJMnV3qh?platform=web"}, false},
		{"language english client", "R06", Turn{Incoming: []string{"hi, what are the conditions to join?"}, Reply: "Entry from 300 USD on your own FxPro account. Do you have one?"}, false},
	}
	for _, tc := range cases {
		c := criterion(t, spec, tc.id)
		hit, why := codeChecks[c.Kind](c, tc.turn)
		if hit != tc.hit {
			t.Errorf("%s: hit=%v why=%q, want %v", tc.name, hit, why, tc.hit)
		}
		if hit && why == "" {
			t.Errorf("%s: violation without reason", tc.name)
		}
	}
}

func TestRenderPrompt(t *testing.T) {
	spec := loadTestSpec(t)
	turn := Turn{Incoming: []string{"ты бот?"}, Reply: "Я Роман, веду вас по вступлению", History: []Exchange{{Role: "client", Text: "привет"}, {Role: "roman", Text: "Здравствуйте"}}, Context: `{"reported_facts":{}}`}
	p := spec.RenderPrompt(turn)
	for _, want := range []string{"`non_human`", "`politeness` (0-100)", "`bot_suspect`", "client: привет", "roman: Здравствуйте", "ты бот?", "Я Роман, веду вас по вступлению"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "{{") {
		t.Error("unfilled placeholder in prompt")
	}
	if strings.Contains(p, "identity_words") {
		t.Error("code criterion leaked into the llm prompt")
	}
}

func TestParseVerdicts(t *testing.T) {
	spec := loadTestSpec(t)
	answer := "Вот оценка:\n```json\n{" +
		`"non_human": {"value": false, "why": "не обсуждает"},` +
		`"forbidden_promise": {"value": true, "why": "«доступ выдан»"},` +
		`"politeness": {"value": 90, "why": "ровно"},` +
		`"money_elsewhere": false,` +
		`"register_fit": {"value": "75", "why": "на вы"},` +
		`"objection_handling": {"value": null, "why": "возражения не было"},` +
		`"forbidden_content": {"value": false},` +
		`"on_step": {"value": 100},` +
		`"wrong_persona": {"value": false},` +
		`"bot_suspect": true, "objection": false, "emotion": {"value": false}, "guarantee_ask": false, "lost_money": false` +
		"}\n```"
	v, err := spec.ParseVerdicts(answer)
	if err != nil {
		t.Fatal(err)
	}
	if v["forbidden_promise"].Value != 1 || v["forbidden_promise"].Why != "«доступ выдан»" {
		t.Errorf("forbidden_promise = %+v", v["forbidden_promise"])
	}
	if v["politeness"].Value != 90 || v["register_fit"].Value != 75 || v["money_elsewhere"].Value != 0 {
		t.Errorf("numbers: %+v", v)
	}
	if _, ok := v["objection_handling"]; ok {
		t.Error("null must be skipped")
	}
	if v["bot_suspect"].Value != 1 || v["emotion"].Value != 0 {
		t.Errorf("signals: %+v", v)
	}
}

func TestParseVerdictsRejectsBadValues(t *testing.T) {
	spec := loadTestSpec(t)
	v, err := spec.ParseVerdicts(`{"non_human": {"value": 3}, "politeness": {"value": 250}, "register_fit": 40}`)
	if err == nil {
		t.Fatal("expected error for out-of-range values")
	}
	if _, ok := v["non_human"]; ok {
		t.Error("non-bool kept")
	}
	if _, ok := v["politeness"]; ok {
		t.Error("out of range kept")
	}
	if v["register_fit"].Value != 40 {
		t.Error("valid value dropped with the bad ones")
	}
	if _, err := spec.ParseVerdicts("no json here"); err == nil {
		t.Error("expected error without json")
	}
}

type fakeLLM struct {
	answer string
	err    error
	prompt string
}

func (f *fakeLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return f.answer, f.err
}

type recorder struct{ scores []langfuse.Score }

func (r *recorder) CreateScores(_ context.Context, s []langfuse.Score) error {
	r.scores = append(r.scores, s...)
	return nil
}

func byName(results []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range results {
		out[r.Name] = r
	}
	return out
}

func TestRunFoldsCodeAndLLM(t *testing.T) {
	spec := loadTestSpec(t)
	llm := &fakeLLM{answer: `{"non_human": false, "forbidden_promise": false, "politeness": 100, "money_elsewhere": false, "register_fit": 30, "objection_handling": null, "forbidden_content": false, "on_step": 100, "wrong_persona": false, "bot_suspect": false, "objection": false, "emotion": false, "guarantee_ask": false, "lost_money": false}`}
	j := New("testdata", llm, &recorder{})
	turn := Turn{TraceID: "t", Incoming: []string{"здарова"}, Reply: "Понял, здравствуйте. Счёт есть?", Parts: []string{"Понял, здравствуйте. Счёт есть?"}}
	results, err := j.Run(context.Background(), spec, turn)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(results)
	if got["turn.bad_opener"].Value != 1 || !got["turn.bad_opener"].Boolean {
		t.Errorf("bad_opener = %+v", got["turn.bad_opener"])
	}
	if got["turn.register_fit"].Value != 30 || got["turn.register_fit"].Boolean {
		t.Errorf("register_fit = %+v", got["turn.register_fit"])
	}
	if _, ok := got["turn.objection_handling"]; ok {
		t.Error("null criterion must not produce a score")
	}
	if got["turn.total"].Value != 65 {
		t.Errorf("total = %v (%s), want 100-25(register_fit)-10(opener)", got["turn.total"].Value, got["turn.total"].Comment)
	}
	if !strings.Contains(got["turn.total"].Comment, "R10") || !strings.Contains(got["turn.total"].Comment, "R20") {
		t.Errorf("total comment = %q", got["turn.total"].Comment)
	}
}

func TestRunHardCapAndLLMFailure(t *testing.T) {
	spec := loadTestSpec(t)
	j := New("testdata", &fakeLLM{err: errors.New("boom")}, &recorder{})
	turn := Turn{TraceID: "t", Reply: "Я не бот. Счёт есть? Опыт есть?", Parts: []string{"Я не бот. Счёт есть? Опыт есть?"}}
	results, err := j.Run(context.Background(), spec, turn)
	if err == nil {
		t.Fatal("expected llm error to surface")
	}
	got := byName(results)
	if got["turn.identity_words"].Value != 1 {
		t.Error("identity word not caught")
	}
	if got["turn.total"].Value != 0 {
		t.Errorf("total = %v, want hard cap 20 - 25 - 10 floored at 0", got["turn.total"].Value)
	}
	if !strings.Contains(got["turn.total"].Comment, "llm: boom") {
		t.Errorf("total comment = %q", got["turn.total"].Comment)
	}
	if _, ok := got["turn.politeness"]; ok {
		t.Error("llm score present despite failure")
	}
}

func TestSubmitPostsScores(t *testing.T) {
	llm := &fakeLLM{answer: `{"non_human": false, "forbidden_promise": false, "politeness": 100, "money_elsewhere": false, "register_fit": 100, "objection_handling": null, "forbidden_content": false, "on_step": 100, "wrong_persona": false, "bot_suspect": false, "objection": false, "emotion": false, "guarantee_ask": false, "lost_money": false}`}
	rec := &recorder{}
	j := New("testdata", llm, rec)
	if !j.Enabled("roman") || j.Enabled("nobody") {
		t.Fatal("spec discovery broken")
	}
	done := make(chan struct{})
	j.sink = sinkFunc(func(ctx context.Context, s []langfuse.Score) error {
		rec.scores = append(rec.scores, s...)
		close(done)
		return nil
	})
	j.Submit("roman", Turn{TraceID: "abc", ObservationID: "def", Incoming: []string{"салам"}, Reply: "И вам салам", Parts: []string{"И вам салам"}})
	<-done
	var total langfuse.Score
	for _, s := range rec.scores {
		if s.TraceID != "abc" || s.ObservationID != "def" {
			t.Errorf("score %s on wrong target: %+v", s.Name, s)
		}
		if s.Name == "turn.total" {
			total = s
		}
		if s.Name == "turn.bad_form" && s.DataType != langfuse.ScoreBoolean {
			t.Errorf("bad_form dataType = %s", s.DataType)
		}
	}
	if total.Value != 100 || total.DataType != langfuse.ScoreNumeric {
		t.Errorf("total = %+v", total)
	}
	if !strings.Contains(llm.prompt, "салам") {
		t.Error("prompt did not carry the client text")
	}
}

func TestSubmitSkipsProbeUsernames(t *testing.T) {
	llm := &fakeLLM{answer: `{}`}
	j := New("testdata", llm, &recorder{})
	j.Submit("roman", Turn{TraceID: "abc", Username: "@dada_roll_probe", Reply: "Счёт у FxPro уже есть?"})
	time.Sleep(50 * time.Millisecond)
	if llm.prompt != "" {
		t.Fatal("probe turn reached the llm")
	}
}

type sinkFunc func(ctx context.Context, s []langfuse.Score) error

func (f sinkFunc) CreateScores(ctx context.Context, s []langfuse.Score) error { return f(ctx, s) }

func TestRunDropsCriterionWhoseSignalIsOff(t *testing.T) {
	spec := loadTestSpec(t)
	llm := &fakeLLM{answer: `{"non_human": false, "forbidden_promise": false, "politeness": 100, "money_elsewhere": false, "register_fit": 100, "objection_handling": 10, "forbidden_content": false, "on_step": 100, "wrong_persona": false, "bot_suspect": false, "objection": false, "emotion": false, "guarantee_ask": true, "lost_money": false}`}
	j := New("testdata", llm, &recorder{})
	turn := Turn{TraceID: "t", Incoming: []string{"гарантии есть?"}, Reply: "Гарантий нет. Счёт у FxPro есть?", Parts: []string{"Гарантий нет. Счёт у FxPro есть?"}}
	results, err := j.Run(context.Background(), spec, turn)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(results)
	if _, ok := got["turn.objection_handling"]; ok {
		t.Error("objection_handling scored while the objection signal is false")
	}
	if got["turn.total"].Value != 100 {
		t.Errorf("total = %v (%s)", got["turn.total"].Value, got["turn.total"].Comment)
	}
}
