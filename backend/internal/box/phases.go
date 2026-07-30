package box

import (
	"fmt"
	"time"
)

// Phase accounting for time to ready.
//
// T0 is the instant the spawn request is admitted server-side — the first
// statement after auth, quota and the spend-cap precheck. Not when the user typed
// the command (unmeasurable) and not after the pool pick (that would hide
// queueing, which is exactly what breaks under load).
//
// T1 is the instant the orchestrator receives the exit status of CanaryCommand
// from inside the box. See readiness.go for why that, and not something cheaper.
//
// Phases are measured separately so a regression names its own culprit instead of
// hiding behind a fast total.

// Phase is one segment of the critical path.
type Phase string

const (
	// PhaseAdmit covers auth, quota and the spend-cap precheck, ending when a
	// placement decision has been made.
	PhaseAdmit Phase = "admit"
	// PhasePoolPop covers claiming a warm instance. Near zero on a hit; on a miss
	// the whole cold boot lands here, which is why misses are counted separately.
	PhasePoolPop Phase = "pool_pop"
	// PhaseBoot covers binding tenant identity onto the claimed instance.
	PhaseBoot Phase = "boot"
	// PhaseNet covers programming the network out of quarantine into tenant egress.
	PhaseNet Phase = "net"
	// PhaseAuth covers the exec channel accepting the customer's key. Key
	// injection is the classic hidden second.
	PhaseAuth Phase = "auth"
	// PhaseCanary covers running the canary and receiving its exit status.
	PhaseCanary Phase = "canary"
)

// CriticalPath is the ordered set of phases every spawn passes through. The
// timeline requires completion in exactly this order, so a reordering or a skipped
// phase is an error rather than a silently short total.
var CriticalPath = []Phase{
	PhaseAdmit,
	PhasePoolPop,
	PhaseBoot,
	PhaseNet,
	PhaseAuth,
	PhaseCanary,
}

// PhaseTimeline accumulates orchestrator-observed phase durations.
//
// There is no method that accepts a caller-supplied timestamp. That is the whole
// design: the only way to record a phase boundary is to ask the orchestrator's
// clock at the moment it happens, so a guest-reported time cannot reach the
// measurement even by accident.
type PhaseTimeline struct {
	clock     Clock
	start     time.Time
	last      time.Time
	completed []Phase
	durations map[Phase]time.Duration
}

// StartTimeline marks T0 and returns a timeline ready to accumulate phases.
func StartTimeline(clock Clock) *PhaseTimeline {
	now := clock.Now()
	return &PhaseTimeline{
		clock:     clock,
		start:     now,
		last:      now,
		durations: make(map[Phase]time.Duration, len(CriticalPath)),
	}
}

// Complete closes phase p at the current instant. Phases must be completed in
// CriticalPath order; completing out of order, twice, or with an unknown phase is
// an error, because each of those would produce a total that does not equal the
// sum of its parts and a dashboard nobody can reason about.
func (t *PhaseTimeline) Complete(p Phase) error {
	next := len(t.completed)
	if next >= len(CriticalPath) {
		return fmt.Errorf("box: phase %q completed after the critical path ended", p)
	}
	if want := CriticalPath[next]; p != want {
		return fmt.Errorf("box: phase %q completed out of order, expected %q", p, want)
	}
	now := t.clock.Now()
	t.durations[p] = now.Sub(t.last)
	t.last = now
	t.completed = append(t.completed, p)
	return nil
}

// Total is the T0..T1 span: the span the product is sold on.
func (t *PhaseTimeline) Total() time.Duration { return t.last.Sub(t.start) }

// Durations returns the per-phase durations keyed by phase name, ready to hand to
// metrics.RecordBoxReady.
func (t *PhaseTimeline) Durations() map[string]time.Duration {
	out := make(map[string]time.Duration, len(t.durations))
	for p, d := range t.durations {
		out[string(p)] = d
	}
	return out
}

// Complete reports whether every phase on the critical path has been closed.
func (t *PhaseTimeline) Completed() bool { return len(t.completed) == len(CriticalPath) }

// Validate checks the two properties every consumer of these numbers assumes:
// the whole critical path was walked, and the phases are disjoint and sum to the
// total. A timeline that fails this must not be reported as a measurement.
func (t *PhaseTimeline) Validate() error {
	if !t.Completed() {
		return fmt.Errorf("box: timeline incomplete, %d of %d phases closed", len(t.completed), len(CriticalPath))
	}
	var sum time.Duration
	for _, p := range CriticalPath {
		d, ok := t.durations[p]
		if !ok {
			return fmt.Errorf("box: phase %q missing from timeline", p)
		}
		if d < 0 {
			return fmt.Errorf("box: phase %q has negative duration %s (clock went backwards)", p, d)
		}
		sum += d
	}
	if sum != t.Total() {
		return fmt.Errorf("box: phases sum to %s but total is %s; phases must be disjoint and exhaustive", sum, t.Total())
	}
	return nil
}
