package agentjudge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/langfuse"
)

const blockSpecYAML = `version: 2
name: turn
skip:
  usernames: ["qa_*"]
total:
  hard_cap: 20
  weights: {major: 25, minor: 10}
  fail_below: 50
signals:
  - id: distress
    when: client is grieving
criteria:
  - id: B1
    score: fact_unbacked
    check: llm
    type: bool
    severity: hard
    title: fact
    text: fact not in kb
    block: handoff
    ask: ask-fact
    code: E_CHECK_AND_RETURN
    line: line-fact
    source: conversations.jsonl#1
  - id: B2
    score: script_repeat
    check: llm
    type: bool
    severity: minor
    title: repeat
    text: repeats the script question
    block: rewrite
    ask: ask-repeat
  - id: B3
    score: sell_during_distress
    check: llm
    type: bool
    severity: hard
    applies: distress
    title: sell
    text: sells while the client grieves
    block: rewrite
    ask: ask-sell
  - id: C1
    score: bad_form
    check: code
    kind: forbid_words
    words: [forbidden]
    type: bool
    severity: hard
    title: code block
    block: rewrite
    ask: ask-code
  - id: S1
    score: politeness
    check: llm
    type: score
    severity: major
    title: polite
    text: politeness 0-100
`

const blockSpecMD = "CRIT\n{{criteria}}SIG\n{{signals}}KB\n{{kb}}\nOUT {{output}}\n"

func writeAgent(t *testing.T, yamlBody, md string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "bot", "judge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "turn.yaml"), []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "turn.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// seqLLM answers the calls in order and records every prompt.
type seqLLM struct {
	mu      sync.Mutex
	answers []string
	err     error
	prompts []string
}

func (s *seqLLM) Complete(_ context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	if s.err != nil {
		return "", s.err
	}
	a := s.answers[0]
	if len(s.answers) > 1 {
		s.answers = s.answers[1:]
	}
	return a, nil
}

func TestSpecBlockFields(t *testing.T) {
	spec, err := ParseSpec([]byte(blockSpecYAML))
	if err != nil {
		t.Fatal(err)
	}
	c := spec.Criteria[0]
	if c.Block != BlockHandoff || c.Ask != "ask-fact" || c.Code != "E_CHECK_AND_RETURN" || c.Line != "line-fact" || c.Source != "conversations.jsonl#1" {
		t.Errorf("handoff criterion = %+v", c)
	}
	if spec.Criteria[1].Block != BlockRewrite || spec.Criteria[4].Block != "" {
		t.Errorf("blocks = %q %q", spec.Criteria[1].Block, spec.Criteria[4].Block)
	}
	head := "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: llm, type: bool, text: t, "
	bad := map[string]string{
		"unknown block":     head + "block: stop, ask: x}\n",
		"rewrite no ask":    head + "block: rewrite}\n",
		"handoff no ask":    head + "block: handoff, code: C, line: L, source: S}\n",
		"handoff no code":   head + "block: handoff, ask: x, line: L, source: S}\n",
		"handoff no line":   head + "block: handoff, ask: x, code: C, source: S}\n",
		"handoff no source": head + "block: handoff, ask: x, code: C, line: L}\n",
	}
	for name, body := range bad {
		if _, err := ParseSpec([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := ParseSpec([]byte(head + "block: rewrite, ask: x}\n")); err != nil {
		t.Errorf("rewrite with ask: %v", err)
	}
}

func TestOldSpecsLoadWithoutBlocks(t *testing.T) {
	for _, dir := range []string{"testdata/agents/roman/judge", "testdata/agents/duo/judge"} {
		specs, err := LoadSpecs(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, s := range specs {
			for _, c := range s.Criteria {
				if c.Block != "" || c.Ask != "" || c.Code != "" || c.Line != "" || c.Source != "" {
					t.Errorf("%s/%s: %s has block fields", dir, s.Name, c.ID)
				}
			}
		}
	}
	j := New("testdata", &fakeLLM{answer: `{}`}, &recorder{})
	pc, err := j.Check(context.Background(), "roman", Turn{Reply: "x"})
	if err != nil || len(pc.Violations) != 0 || pc.Signals != nil {
		t.Errorf("spec without block criteria: %+v %v", pc, err)
	}
	if pc, err := j.Check(context.Background(), "nobody", Turn{Reply: "x"}); err != nil || len(pc.Violations) != 0 {
		t.Errorf("agent without spec: %+v %v", pc, err)
	}
	var nilJudge *Judge
	if _, err := nilJudge.Check(context.Background(), "roman", Turn{}); err != nil {
		t.Error(err)
	}
	if _, ok := nilJudge.Criterion("roman", "R01"); ok {
		t.Error("nil judge found a criterion")
	}
}

func TestCheckUsesOnlyBlockCriteria(t *testing.T) {
	root := writeAgent(t, blockSpecYAML, blockSpecMD)
	llm := &seqLLM{answers: []string{`{"fact_unbacked": {"value": true, "why": "rate not in kb"}, "script_repeat": false, "sell_during_distress": {"value": true, "why": "offer"}, "distress": true}`}}
	j := New(root, llm, &recorder{})
	j.MuteWhen(func() bool { return true })
	pc, err := j.Check(context.Background(), "bot", Turn{Username: "qa_probe", Reply: "a forbidden reply"})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("mute or skip or empty trace stopped the check: %d calls", len(llm.prompts))
	}
	p := llm.prompts[0]
	for _, want := range []string{"`fact_unbacked`", "`script_repeat`", "`sell_during_distress`", "`distress`"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %s", want)
		}
	}
	if strings.Contains(p, "`politeness`") {
		t.Error("non-block criterion asked in Check")
	}
	got := map[string]Violation{}
	var order []string
	for _, v := range pc.Violations {
		got[v.ID] = v
		order = append(order, v.ID)
	}
	if strings.Join(order, ",") != "B1,B3,C1" {
		t.Fatalf("violations = %+v", pc.Violations)
	}
	if v := got["B1"]; v.Block != BlockHandoff || v.Ask != "ask-fact" || v.Why != "rate not in kb" || v.Code != "E_CHECK_AND_RETURN" {
		t.Errorf("B1 = %+v", v)
	}
	if v := got["C1"]; v.Block != BlockRewrite || v.Ask != "ask-code" || v.Why == "" {
		t.Errorf("C1 = %+v", v)
	}
	if !pc.Signals["distress"] || len(pc.Signals) != 1 {
		t.Errorf("signals = %v", pc.Signals)
	}
	c, ok := j.Criterion("bot", "B1")
	if !ok || c.Line != "line-fact" || c.Source != "conversations.jsonl#1" {
		t.Errorf("Criterion = %+v %v", c, ok)
	}
	if _, ok := j.Criterion("bot", "nope"); ok {
		t.Error("unknown id found")
	}
}

func TestCheckAppliesAndErrors(t *testing.T) {
	root := writeAgent(t, blockSpecYAML, blockSpecMD)
	llm := &seqLLM{answers: []string{`{"fact_unbacked": false, "script_repeat": true, "sell_during_distress": true, "distress": false}`}}
	j := New(root, llm, &recorder{})
	pc, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Violations) != 1 || pc.Violations[0].ID != "B2" || pc.Signals["distress"] {
		t.Errorf("sell_during_distress must not fire without distress: %+v", pc)
	}
	j = New(root, &seqLLM{err: errors.New("boom")}, &recorder{})
	if _, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("llm error = %v", err)
	}
	j = New(root, &seqLLM{answers: []string{`{"fact_unbacked": 7}`}}, &recorder{})
	if _, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"}); err == nil {
		t.Error("parse error not returned")
	}
	j = New(root, nil, &recorder{})
	if _, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"}); err == nil {
		t.Error("block llm criteria without llm must be an error")
	}
}

func TestRenderPromptKB(t *testing.T) {
	spec, err := ParseSpec([]byte(blockSpecYAML))
	if err != nil {
		t.Fatal(err)
	}
	spec.Template = blockSpecMD
	p := spec.RenderPrompt(Turn{Reply: "r", KB: []KBResult{{Query: " rate ", Text: "rate is 5%\n"}, {Query: "fee", Text: "no fee"}}})
	if !strings.Contains(p, "KB\nquery: rate\nrate is 5%\n\nquery: fee\nno fee\nOUT r") {
		t.Errorf("kb block:\n%s", p)
	}
	p = spec.RenderPrompt(Turn{Reply: "r"})
	if !strings.Contains(p, "KB\n\nOUT r") || strings.Contains(p, "{{") {
		t.Errorf("empty kb:\n%s", p)
	}
	old := loadTestSpec(t)
	if old.RenderPrompt(Turn{Reply: "r", KB: []KBResult{{Query: "q", Text: "kbtext"}}}) != old.RenderPrompt(Turn{Reply: "r"}) {
		t.Error("template without {{kb}} changed by KB")
	}
}

const fullAnswer = `{"fact_unbacked": false, "script_repeat": false, "sell_during_distress": false, "politeness": 80, "distress": false}`

func submitOne(t *testing.T, j *Judge, turn Turn) langfuse.Score {
	t.Helper()
	done := make(chan langfuse.Score, 1)
	j.sink = sinkFunc(func(_ context.Context, s []langfuse.Score) error {
		done <- s[0]
		return nil
	})
	j.Submit("bot", turn)
	select {
	case s := <-done:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no score")
	}
	return langfuse.Score{}
}

func TestSubmitReusesPrecheckForSameReply(t *testing.T) {
	root := writeAgent(t, blockSpecYAML, blockSpecMD)
	llm := &seqLLM{answers: []string{
		`{"fact_unbacked": {"value": true, "why": "from check"}, "script_repeat": false, "sell_during_distress": false, "distress": true}`,
		`{"fact_unbacked": false, "script_repeat": false, "sell_during_distress": false, "politeness": 80, "distress": false}`,
	}}
	j := New(root, llm, &recorder{})
	turn := Turn{TraceID: "tr", Reply: "draft"}
	pc, err := j.Check(context.Background(), "bot", turn)
	if err != nil {
		t.Fatal(err)
	}
	turn.PrecheckVerdicts, turn.PrecheckReply = &pc, "draft"
	turn.Precheck = map[string]any{"outcome": "handoff", "duration_ms": 1200}
	score := submitOne(t, j, turn)
	if len(llm.prompts) != 2 {
		t.Fatalf("calls = %d", len(llm.prompts))
	}
	second := llm.prompts[1]
	if strings.Contains(second, "`fact_unbacked`") || strings.Contains(second, "`script_repeat`") || strings.Contains(second, "`sell_during_distress`") {
		t.Errorf("block criteria asked twice:\n%s", second)
	}
	if !strings.Contains(second, "`politeness`") || !strings.Contains(second, "`distress`") {
		t.Errorf("second prompt lost non-block criteria or signals:\n%s", second)
	}
	checks := score.Metadata["checks"].(map[string]any)
	if e, _ := checks["fact_unbacked"].(map[string]any); e == nil || e["value"] != float64(1) || e["why"] != "from check" {
		t.Errorf("reused verdict missing: %v", checks)
	}
	if e, _ := checks["sell_during_distress"].(map[string]any); e == nil || e["value"] != float64(0) {
		t.Errorf("reused applied verdict missing: %v", checks)
	}
	// distress gates the reused sell_during_distress, so its value comes
	// from the precheck call, not from the second call's false.
	if e, _ := checks["distress"].(map[string]any); e == nil || e["value"] != float64(1) {
		t.Errorf("gating signal not taken from check: %v", checks["distress"])
	}
	if score.Value != 20 {
		t.Errorf("total = %v (%s), want hard cap from reused fact_unbacked", score.Value, score.Comment)
	}
	if pre, _ := score.Metadata["precheck"].(map[string]any); pre["outcome"] != "handoff" {
		t.Errorf("metadata.precheck = %v", score.Metadata["precheck"])
	}
}

func TestSubmitAsksAgainForDifferentReply(t *testing.T) {
	root := writeAgent(t, blockSpecYAML, blockSpecMD)
	llm := &seqLLM{answers: []string{
		`{"fact_unbacked": true, "script_repeat": false, "sell_during_distress": null, "distress": false}`,
		fullAnswer,
	}}
	j := New(root, llm, &recorder{})
	pc, err := j.Check(context.Background(), "bot", Turn{Reply: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	score := submitOne(t, j, Turn{TraceID: "tr", Reply: "rewritten", PrecheckReply: "draft", PrecheckVerdicts: &pc})
	if !strings.Contains(llm.prompts[1], "`fact_unbacked`") {
		t.Error("different reply must be judged in full")
	}
	if score.Value != 100 {
		t.Errorf("total = %v (%s)", score.Value, score.Comment)
	}
	if _, ok := score.Metadata["precheck"]; ok {
		t.Error("empty Turn.Precheck must not add metadata.precheck")
	}
	// A failed check is never reused.
	llm2 := &seqLLM{answers: []string{`{"fact_unbacked": 7}`, fullAnswer}}
	j2 := New(root, llm2, &recorder{})
	pc2, _ := j2.Check(context.Background(), "bot", Turn{Reply: "draft"})
	submitOne(t, j2, Turn{TraceID: "tr", Reply: "draft", PrecheckReply: "draft", PrecheckVerdicts: &pc2})
	if !strings.Contains(llm2.prompts[1], "`fact_unbacked`") {
		t.Error("verdicts of a failed check were reused")
	}
}

func TestScoreUnchangedWithoutNewFields(t *testing.T) {
	plain := strings.NewReplacer(
		"    block: handoff\n    ask: ask-fact\n    code: E_CHECK_AND_RETURN\n    line: line-fact\n    source: conversations.jsonl#1\n", "",
		"    block: rewrite\n    ask: ask-repeat\n", "",
		"    block: rewrite\n    ask: ask-sell\n", "",
		"    block: rewrite\n    ask: ask-code\n", "",
	).Replace(blockSpecYAML)
	if strings.Contains(plain, "block:") {
		t.Fatal("replacer missed a block")
	}
	md := "CRIT\n{{criteria}}SIG\n{{signals}}OUT {{output}}\n"
	turn := Turn{TraceID: "tr", ObservationID: "ob", Reply: "a forbidden reply", Parts: []string{"a forbidden reply"}}
	var prompts []string
	var bodies []string
	for _, body := range []string{plain, blockSpecYAML} {
		llm := &seqLLM{answers: []string{fullAnswer}}
		j := New(writeAgent(t, body, md), llm, &recorder{})
		b, err := json.Marshal(submitOne(t, j, turn))
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(b))
		prompts = append(prompts, llm.prompts[0])
	}
	if bodies[0] != bodies[1] || prompts[0] != prompts[1] {
		t.Errorf("block fields changed the async score:\n%s\n%s", bodies[0], bodies[1])
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &m)
	meta := m["metadata"].(map[string]any)
	if len(meta) != 2 || meta["checks"] == nil || meta["violations"] == nil {
		t.Errorf("metadata keys = %v", meta)
	}
}

func TestCompleteStopsAtContextDeadline(t *testing.T) {
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer limited.Close()
	c := &OpenAIChat{BaseURL: limited.URL, APIKey: "k", Model: "m", RetryPause: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Complete(ctx, "hi")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("rate-limit retries ignored the deadline: err=%v after %v", err, time.Since(start))
	}

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer slow.Close()
	defer close(release)
	c = &OpenAIChat{BaseURL: slow.URL, APIKey: "k", Model: "m"}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	start = time.Now()
	_, err = c.Complete(ctx2, "hi")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("90 s client timeout outlived the deadline: err=%v after %v", err, time.Since(start))
	}
}

func TestCheckBrokenSpecIsAnError(t *testing.T) {
	root := writeAgent(t, "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: llm, type: bool}\n", blockSpecMD)
	llm := &seqLLM{answers: []string{fullAnswer}}
	j := New(root, llm, &recorder{})
	for i := 0; i < 2; i++ {
		pc, err := j.Check(context.Background(), "bot", Turn{Reply: "x"})
		if err == nil || !strings.Contains(err.Error(), "needs text") || len(pc.Violations) != 0 {
			t.Errorf("call %d: broken spec = %+v %v", i, pc, err)
		}
	}
	if j.Enabled("bot") {
		t.Error("broken spec enabled")
	}
	j.Submit("bot", Turn{TraceID: "tr", Reply: "x"})
	if _, ok := j.Criterion("bot", "A"); ok {
		t.Error("criterion of a broken spec")
	}
	if len(llm.prompts) != 0 {
		t.Errorf("broken spec reached the llm: %d calls", len(llm.prompts))
	}

	// A missing prompt file is a broken spec too, not a missing judge.
	root = writeAgent(t, blockSpecYAML, blockSpecMD)
	if err := os.Remove(filepath.Join(root, "agents", "bot", "judge", "turn.md")); err != nil {
		t.Fatal(err)
	}
	j = New(root, llm, &recorder{})
	if _, err := j.Check(context.Background(), "bot", Turn{Reply: "x"}); err == nil {
		t.Error("missing prompt file passed the check")
	}

	// A judge dir without specs is still "no judge".
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "agents", "bot", "judge"), 0o755); err != nil {
		t.Fatal(err)
	}
	j = New(empty, llm, &recorder{})
	if pc, err := j.Check(context.Background(), "bot", Turn{Reply: "x"}); err != nil || pc.Signals != nil {
		t.Errorf("empty judge dir: %+v %v", pc, err)
	}
}

func TestCheckNullBlockVerdictIsAnError(t *testing.T) {
	root := writeAgent(t, blockSpecYAML, blockSpecMD)
	cases := map[string]bool{
		`{"fact_unbacked": null, "script_repeat": false, "sell_during_distress": false, "distress": false}`:                       true,
		`{"fact_unbacked": {"value": null}, "script_repeat": false, "sell_during_distress": false, "distress": false}`:            true,
		`{"fact_unbacked": false, "script_repeat": false, "sell_during_distress": null, "distress": true}`:                        true,
		`{"fact_unbacked": false, "script_repeat": false, "sell_during_distress": null, "distress": false}`:                       false,
		`{"fact_unbacked": false, "script_repeat": false, "sell_during_distress": null}`:                                          false,
		`{"fact_unbacked": {"value": true, "why": "w"}, "script_repeat": null, "sell_during_distress": false, "distress": false}`: true,
	}
	for answer, wantErr := range cases {
		j := New(root, &seqLLM{answers: []string{answer}}, &recorder{})
		pc, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"})
		if (err != nil) != wantErr {
			t.Errorf("%s: err = %v, want error %v", answer, err, wantErr)
		}
		if wantErr && pc.complete {
			t.Errorf("%s: failed check marked complete", answer)
		}
	}
	// Violations found next to a null stay in the Precheck.
	j := New(root, &seqLLM{answers: []string{`{"fact_unbacked": {"value": true, "why": "w"}, "script_repeat": null, "sell_during_distress": false, "distress": false}`}}, &recorder{})
	pc, err := j.Check(context.Background(), "bot", Turn{Reply: "ok"})
	if err == nil || !strings.Contains(err.Error(), "script_repeat") || len(pc.Violations) != 1 || pc.Violations[0].ID != "B1" {
		t.Errorf("violations next to null: %+v %v", pc.Violations, err)
	}
	// Submit keeps null as skipped.
	j = New(root, &seqLLM{answers: []string{`{"fact_unbacked": null, "script_repeat": false, "sell_during_distress": false, "politeness": 80, "distress": false}`}}, &recorder{})
	score := submitOne(t, j, Turn{TraceID: "tr", Reply: "ok"})
	checks := score.Metadata["checks"].(map[string]any)
	if _, ok := checks["fact_unbacked"]; ok || score.Value != 100 || strings.Contains(score.Comment, "llm:") {
		t.Errorf("submit null: %v %v %q", checks, score.Value, score.Comment)
	}
}

func TestSpecBlockAppliesMustBeSignal(t *testing.T) {
	bad := strings.Replace(blockSpecYAML, "applies: distress", "applies: nosuch", 1)
	if _, err := ParseSpec([]byte(bad)); err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("unknown applies on block criterion: %v", err)
	}
	// Non-block criteria keep their old freedom.
	head := "version: 2\nname: turn\ncriteria:\n  - {id: A, score: a, check: llm, type: bool, text: t, applies: nosuch}\n"
	if _, err := ParseSpec([]byte(head)); err != nil {
		t.Errorf("non-block applies: %v", err)
	}
}
