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
	CreateScore(ctx context.Context, score langfuse.Score) error
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
// <basePath>/agents/<agent>/judge.
type Judge struct {
	basePath string
	llm      LLM
	sink     ScoreSink
	timeout  time.Duration
	mu       sync.Mutex
	specs    map[string]*Spec
	missing  map[string]bool
}

// New wires a judge; basePath is the same gitops root the runtime reads
// domains from.
func New(basePath string, llm LLM, sink ScoreSink) *Judge {
	return &Judge{basePath: basePath, llm: llm, sink: sink, timeout: 2 * time.Minute, specs: map[string]*Spec{}, missing: map[string]bool{}}
}

func (j *Judge) spec(agent string) *Spec {
	j.mu.Lock()
	defer j.mu.Unlock()
	if s, ok := j.specs[agent]; ok {
		return s
	}
	if j.missing[agent] {
		return nil
	}
	dir := filepath.Join(j.basePath, "agents", agent, "judge")
	s, err := LoadSpec(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Str("agent", agent).Msg("agentjudge: judge spec unreadable")
		}
		j.missing[agent] = true
		return nil
	}
	j.specs[agent] = s
	log.Info().Str("agent", agent).Int("criteria", len(s.Criteria)).Msg("agentjudge: judge spec loaded")
	return s
}

// Enabled reports whether the agent ships a judge spec.
func (j *Judge) Enabled(agent string) bool {
	return j != nil && j.spec(agent) != nil
}

// Submit scores the turn on its own goroutine and never reports back.
func (j *Judge) Submit(agent string, t Turn) {
	if j == nil || t.TraceID == "" {
		return
	}
	spec := j.spec(agent)
	if spec == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Str("trace", t.TraceID).Msg("agentjudge: run panicked")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
		defer cancel()
		results, err := j.Run(ctx, spec, t)
		if err != nil {
			log.Warn().Err(err).Str("agent", agent).Str("trace", t.TraceID).Msg("agentjudge: llm part failed, code scores only")
		}
		for _, r := range results {
			score := langfuse.Score{TraceID: t.TraceID, ObservationID: t.ObservationID, Name: r.Name, Value: r.Value, Comment: r.Comment, DataType: langfuse.ScoreNumeric}
			if r.Boolean {
				score.DataType = langfuse.ScoreBoolean
			}
			if err := j.sink.CreateScore(ctx, score); err != nil {
				log.Warn().Err(err).Str("trace", t.TraceID).Str("score", r.Name).Msg("agentjudge: score not stored")
			}
		}
	}()
}

// Run evaluates the code criteria, makes the one LLM call and folds both into
// the total. The code scores and the total are returned even when the LLM
// call fails; the error then says why the LLM criteria are absent.
func (j *Judge) Run(ctx context.Context, spec *Spec, t Turn) ([]Result, error) {
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
	var llmErr error
	if j.llm != nil && len(spec.llmCriteria()) > 0 {
		answer, err := j.llm.Complete(ctx, spec.RenderPrompt(t))
		if err != nil {
			llmErr = err
		} else {
			verdicts, perr := spec.ParseVerdicts(answer)
			if perr != nil {
				llmErr = perr
			}
			for _, c := range spec.llmCriteria() {
				v, ok := verdicts[c.Score]
				if !ok {
					continue
				}
				r := Result{Name: spec.ScoreName(c.Score), Value: v.Value, Comment: v.Why, Severity: c.Severity}
				if c.Type == TypeBool {
					r.Boolean = true
					if v.Value == 1 {
						violations[c.ID] = c
					}
				} else if v.Value < spec.Total.FailBelow {
					violations[c.ID] = c
				}
				results = append(results, r)
			}
			for _, sg := range spec.Signals {
				if v, ok := verdicts[sg.ID]; ok {
					results = append(results, Result{Name: spec.ScoreName(sg.ID), Value: v.Value, Boolean: true})
				}
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
