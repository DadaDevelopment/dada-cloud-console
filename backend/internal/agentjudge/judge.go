package agentjudge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/rs/zerolog/log"
)

// ScoreSink receives the produced scores; the Langfuse client satisfies it.
type ScoreSink interface {
	CreateScores(ctx context.Context, scores []langfuse.Score) error
}

// Result is one score ready for the sink.
type Result struct {
	Name     string
	Value    float64
	Boolean  bool
	Comment  string
	Severity string
}

// Judge scores turns of the agents whose package ships a judge spec under
// <basePath>/agents/<agent>/judge. Each judge posts one Langfuse score per
// turn (its total, criteria and signals in the score metadata): Langfuse
// bills a unit per score, and the 30-odd per-criterion scores of the first
// version were more than half of the project's monthly quota.
type Judge struct {
	basePath  string
	llm       LLM
	sink      ScoreSink
	timeout   time.Duration
	storeWait time.Duration
	queue     chan scoreJob
	mu        sync.Mutex
	specs     map[string][]*Spec
	missing   map[string]bool
	broken    map[string]error
	muted     func() bool
}

type scoreJob struct {
	trace  string
	scores []langfuse.Score
}

const queueSize = 512

// New wires a judge; basePath is the same gitops root the runtime reads
// domains from.
func New(basePath string, llm LLM, sink ScoreSink) *Judge {
	j := &Judge{basePath: basePath, llm: llm, sink: sink, timeout: 4 * time.Minute, storeWait: 10 * time.Minute, queue: make(chan scoreJob, queueSize), specs: map[string][]*Spec{}, missing: map[string]bool{}, broken: map[string]error{}}
	go j.store()
	return j
}

// store is the single worker that posts queued scores. Langfuse takes scores
// one request at a time under a per-minute budget, so a burst of turns queues
// here instead of timing out in parallel.
func (j *Judge) store() {
	for job := range j.queue {
		ctx, cancel := context.WithTimeout(context.Background(), j.storeWait)
		if err := j.sink.CreateScores(ctx, job.scores); err != nil {
			log.Warn().Err(err).Str("trace", job.trace).Int("scores", len(job.scores)).Int("queued", len(j.queue)).Msg("agentjudge: scores not stored")
		}
		cancel()
	}
}

// agentSpecs returns the agent's judge specs. An agent without a judge dir
// (or without *.yaml in it) has none and no error; a judge dir whose specs
// fail to load or validate returns the load error on every call, so the
// precheck can fail closed instead of passing the reply unchecked.
func (j *Judge) agentSpecs(agent string) ([]*Spec, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if s, ok := j.specs[agent]; ok {
		return s, nil
	}
	if err, ok := j.broken[agent]; ok {
		return nil, err
	}
	if j.missing[agent] {
		return nil, nil
	}
	dir := filepath.Join(j.basePath, "agents", agent, "judge")
	specs, err := LoadSpecs(dir)
	// LoadSpecs returns the bare os.ErrNotExist sentinel only for a dir
	// without specs; a missing prompt file is a *PathError and stays broken.
	if err == os.ErrNotExist {
		j.missing[agent] = true
		return nil, nil
	}
	if err != nil {
		err = fmt.Errorf("agentjudge: judge spec of %s unreadable: %w", agent, err)
		log.Warn().Err(err).Str("agent", agent).Msg("agentjudge: judge spec unreadable")
		j.broken[agent] = err
		return nil, err
	}
	j.specs[agent] = specs
	for _, s := range specs {
		log.Info().Str("agent", agent).Str("judge", s.Name).Int("criteria", len(s.Criteria)).Msg("agentjudge: judge spec loaded")
	}
	return specs, nil
}

// Enabled reports whether the agent ships at least one judge spec.
// A broken spec disables the async judge with a warning.
func (j *Judge) Enabled(agent string) bool {
	if j == nil {
		return false
	}
	specs, err := j.agentSpecs(agent)
	if err != nil {
		log.Warn().Err(err).Str("agent", agent).Msg("agentjudge: judge disabled, spec broken")
	}
	return len(specs) > 0
}

// MuteWhen installs the budget check: while it reports true no turn is
// judged, so no score is posted.
func (j *Judge) MuteWhen(muted func() bool) {
	if j != nil {
		j.muted = muted
	}
}

// Submit scores the turn on its own goroutine and never reports back.
func (j *Judge) Submit(agent string, t Turn) {
	if j == nil || t.TraceID == "" || (j.muted != nil && j.muted()) {
		return
	}
	specs, err := j.agentSpecs(agent)
	if err != nil {
		log.Warn().Err(err).Str("agent", agent).Str("trace", t.TraceID).Msg("agentjudge: turn not judged, spec broken")
	}
	for _, spec := range specs {
		if !spec.Skips(t.Username) {
			j.submitSpec(agent, spec, t)
		}
	}
}

func (j *Judge) submitSpec(agent string, spec *Spec, t Turn) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Str("judge", spec.Name).Str("trace", t.TraceID).Msg("agentjudge: run panicked")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
		defer cancel()
		var reuse *Precheck
		if spec.Name == precheckSpec && t.PrecheckVerdicts != nil && t.PrecheckVerdicts.complete && t.Reply == t.PrecheckReply {
			reuse = t.PrecheckVerdicts
		}
		results, err := j.run(ctx, spec, t, reuse)
		if err != nil {
			log.Warn().Err(err).Str("agent", agent).Str("judge", spec.Name).Str("trace", t.TraceID).Msg("agentjudge: llm part failed, code scores only")
		}
		scores := []langfuse.Score{spec.fold(t, results)}
		select {
		case j.queue <- scoreJob{trace: t.TraceID, scores: scores}:
		default:
			log.Warn().Str("judge", spec.Name).Str("trace", t.TraceID).Int("scores", len(scores)).Msg("agentjudge: score queue full, turn dropped")
		}
	}()
}

// fold packs the run into the one score Langfuse gets: the total, with every
// criterion and signal under metadata.checks (value, and the judge's reason
// when it gave one) and the violated ids under metadata.violations.
func (s *Spec) fold(t Turn, results []Result) langfuse.Score {
	checks := map[string]any{}
	violations := []string{}
	total := langfuse.Score{TraceID: t.TraceID, ObservationID: t.ObservationID, Name: s.ScoreName(s.Total.Score), DataType: langfuse.ScoreNumeric}
	for _, r := range results {
		if r.Name == total.Name {
			total.Value, total.Comment = r.Value, r.Comment
			continue
		}
		id := strings.TrimPrefix(r.Name, s.Name+".")
		entry := map[string]any{"value": r.Value}
		if r.Comment != "" {
			entry["why"] = r.Comment
		}
		checks[id] = entry
		if r.Severity != "" && (r.Boolean && r.Value == 1 || !r.Boolean && r.Value < s.Total.FailBelow) {
			violations = append(violations, id)
		}
	}
	sort.Strings(violations)
	total.Metadata = map[string]any{"checks": checks, "violations": violations}
	if s.Name == precheckSpec && len(t.Precheck) > 0 {
		total.Metadata["precheck"] = t.Precheck
	}
	return total
}

// Run evaluates the code criteria, makes the one LLM call and folds both into
// the total. The code scores and the total are returned even when the LLM
// call fails; the error then says why the LLM criteria are absent.
func (j *Judge) Run(ctx context.Context, spec *Spec, t Turn) ([]Result, error) {
	return j.run(ctx, spec, t, nil)
}

// run is Run with the LLM criteria a precheck already answered for the same
// reply taken from it instead of asked again; nil reuse is plain Run.
func (j *Judge) run(ctx context.Context, spec *Spec, t Turn, reuse *Precheck) ([]Result, error) {
	results := make([]Result, 0, len(spec.Criteria)+len(spec.Signals)+1)
	violations := map[string]Criterion{}
	for _, c := range spec.codeCriteria() {
		hit, why := codeChecks[c.Kind](c, t)
		r := Result{Name: spec.ScoreName(c.Score), Boolean: true, Comment: why, Severity: c.Severity}
		if hit {
			r.Value = 1
			violations[c.ID] = c
		}
		results = append(results, r)
	}
	ask := spec
	// fromCheck names the signals that gate a reused criterion. Their values
	// come from the precheck call too, so a reused verdict and the signal it
	// was applied under never disagree within one score; the new call's
	// answer for them is ignored.
	fromCheck := map[string]bool{}
	if reuse != nil {
		ask = spec.subset(func(c Criterion) bool { return c.Check != CheckLLM || !reuse.asked[c.Score] })
		for _, c := range spec.llmCriteria() {
			if reuse.asked[c.Score] && c.Applies != "" {
				fromCheck[c.Applies] = true
			}
		}
	}
	var llmErr error
	var verdicts map[string]Verdict
	answered := false
	if j.llm != nil && len(ask.llmCriteria()) > 0 {
		answer, err := j.llm.Complete(ctx, ask.RenderPrompt(t))
		if err != nil {
			llmErr = err
		} else {
			var perr error
			verdicts, perr = ask.ParseVerdicts(answer)
			if perr != nil {
				llmErr = perr
			}
			answered = true
		}
	}
	signalOn := func(id string) bool {
		if fromCheck[id] {
			r, ok := reuse.results[spec.ScoreName(id)]
			return ok && r.Value == 1
		}
		return verdicts[id].Value == 1
	}
	if answered || reuse != nil {
		for _, c := range spec.llmCriteria() {
			if reuse != nil && reuse.asked[c.Score] {
				if r, ok := reuse.results[spec.ScoreName(c.Score)]; ok {
					if spec.violated(c, r.Value) {
						violations[c.ID] = c
					}
					results = append(results, r)
				}
				continue
			}
			if !answered {
				continue
			}
			v, ok := verdicts[c.Score]
			if !ok {
				continue
			}
			if c.Applies != "" && !signalOn(c.Applies) {
				continue
			}
			r := Result{Name: spec.ScoreName(c.Score), Value: v.Value, Comment: v.Why, Severity: c.Severity, Boolean: c.Type == TypeBool}
			if spec.violated(c, v.Value) {
				violations[c.ID] = c
			}
			results = append(results, r)
		}
		for _, sg := range spec.Signals {
			if answered && !fromCheck[sg.ID] {
				if v, ok := verdicts[sg.ID]; ok {
					results = append(results, Result{Name: spec.ScoreName(sg.ID), Value: v.Value, Boolean: true})
				}
			} else if r, ok := reuse.results[spec.ScoreName(sg.ID)]; ok {
				results = append(results, r)
			}
		}
	}
	total, comment := spec.total(violations)
	if llmErr != nil {
		comment = strings.TrimSpace(comment + "\nllm: " + llmErr.Error())
	}
	results = append(results, Result{Name: spec.ScoreName(spec.Total.Score), Value: total, Comment: comment})
	return results, llmErr
}

// violated reports whether an LLM value breaks the criterion: true for a
// bool flag, below fail_below for a score.
func (s *Spec) violated(c Criterion, value float64) bool {
	if c.Type == TypeBool {
		return value == 1
	}
	return value < s.Total.FailBelow
}

func (s *Spec) total(violations map[string]Criterion) (float64, string) {
	if len(violations) == 0 {
		return 100, "violations: none"
	}
	ids := make([]string, 0, len(violations))
	for id := range violations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	score := 100.0
	soft := 0.0
	for _, id := range ids {
		c := violations[id]
		switch c.Severity {
		case SeverityHard:
			score = s.Total.HardCap
		case SeverityMajor, SeverityMinor:
			soft += float64(s.Total.Weights[c.Severity])
		}
	}
	score = max(score-soft, 0)
	return score, fmt.Sprintf("violations: %s", strings.Join(ids, ", "))
}
