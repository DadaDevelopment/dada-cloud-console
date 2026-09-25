package agentjudge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// precheckSpec is the judge whose block criteria gate a reply before delivery.
const precheckSpec = "turn"

// Violation is one block criterion the checked reply breaks. Line is the
// client line of a handoff violation: the criterion's own, else the spec's
// handoff_line, else empty (the runtime then uses its platform line).
type Violation struct {
	ID    string
	Block string
	Ask   string
	Why   string
	Code  string
	Line  string
}

// SignalAction is what the spec tells the runtime to do about a signal the
// check marked: hand the conversation off with Handoff after the turn's
// reply, and/or stop the follow-up ladder.
type SignalAction struct {
	Signal        string
	Handoff       string
	StopFollowups bool
}

// Precheck is the pre-delivery verdict on one draft reply: the broken block
// criteria in spec order, every signal the LLM marked, the actions of the
// marked signals in spec order, and the spec's default hand-off line.
type Precheck struct {
	Violations  []Violation
	Signals     map[string]bool
	Actions     []SignalAction
	HandoffLine string

	// complete is set when the LLM answered every block criterion, so
	// Submit may reuse the verdicts for the same reply.
	complete bool
	asked    map[string]bool
	results  map[string]Result
}

func (j *Judge) precheckSpec(agent string) (*Spec, error) {
	if j == nil {
		return nil, nil
	}
	specs, err := j.agentSpecs(agent)
	if err != nil {
		return nil, err
	}
	for _, s := range specs {
		if s.Name == precheckSpec {
			return s, nil
		}
	}
	return nil, nil
}

// Check runs the block criteria of the agent's turn spec (plus all signals)
// on a draft reply, synchronously and under the caller's deadline. Unlike
// Submit it ignores the budget mute, the skip list and a missing trace id:
// safety does not switch off with the Langfuse quota. An agent without a
// turn spec or without block criteria gets an empty Precheck; an agent whose
// judge dir holds specs that fail to load gets the load error, so the caller
// fails closed. An LLM or parse failure (including a null verdict for a
// block criterion that applies) returns the error together with whatever
// was decided: the Violations of a Precheck returned with an error are real
// findings, and the runtime applies them even though the check is marked as
// failed.
func (j *Judge) Check(ctx context.Context, agent string, t Turn) (Precheck, error) {
	var pc Precheck
	spec, err := j.precheckSpec(agent)
	if err != nil {
		return pc, err
	}
	if spec == nil {
		return pc, nil
	}
	sub := spec.subset(func(c Criterion) bool { return c.Block != "" })
	if len(sub.Criteria) == 0 {
		return pc, nil
	}
	if j.llm == nil && len(sub.llmCriteria()) > 0 {
		return pc, errors.New("agentjudge: block criteria need an llm")
	}
	results, err := j.Run(ctx, sub, t)
	byName := make(map[string]Result, len(results))
	for _, r := range results {
		byName[r.Name] = r
	}
	pc.Signals = map[string]bool{}
	pc.HandoffLine = strings.TrimSpace(spec.HandoffLine)
	pc.asked = map[string]bool{}
	pc.results = map[string]Result{}
	for _, sg := range sub.Signals {
		name := sub.ScoreName(sg.ID)
		if r, ok := byName[name]; ok {
			pc.Signals[sg.ID] = r.Value == 1
			pc.results[name] = r
			if r.Value == 1 && (sg.Handoff != "" || sg.StopFollowups) {
				pc.Actions = append(pc.Actions, SignalAction{Signal: sg.ID, Handoff: strings.TrimSpace(sg.Handoff), StopFollowups: sg.StopFollowups})
			}
		}
	}
	// A block criterion the LLM answered with null although it applies is
	// an unanswered safety check, not a pass. Submit keeps null as skipped.
	var unanswered []string
	for _, c := range sub.Criteria {
		name := sub.ScoreName(c.Score)
		r, ok := byName[name]
		if c.Check == CheckLLM {
			pc.asked[c.Score] = true
			if ok {
				pc.results[name] = r
			} else if err == nil && (c.Applies == "" || pc.Signals[c.Applies]) {
				unanswered = append(unanswered, c.Score)
			}
		}
		if !ok {
			continue
		}
		hit := r.Value == 1
		if c.Check == CheckLLM {
			hit = sub.violated(c, r.Value)
		}
		if hit {
			v := Violation{ID: c.ID, Block: c.Block, Ask: c.Ask, Why: r.Comment, Code: c.Code}
			if c.Block == BlockHandoff {
				v.Line = strings.TrimSpace(c.Line)
				if v.Line == "" {
					v.Line = pc.HandoffLine
				}
			}
			pc.Violations = append(pc.Violations, v)
		}
	}
	if len(unanswered) > 0 {
		err = fmt.Errorf("agentjudge: judge answer: %s null", strings.Join(unanswered, ", "))
	}
	pc.complete = err == nil && len(pc.asked) > 0
	return pc, err
}
