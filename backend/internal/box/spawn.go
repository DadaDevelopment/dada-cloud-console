package box

import (
	"context"
	"errors"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/metrics"
)

// The ready path.
//
// This is the function the whole product is measured on, so it is deliberately
// short and deliberately flat. Every step it takes is recorded in order, and that
// ordered list is pinned by a golden file (readypath_golden_test.go).
//
// The reason for the golden: every latency regression in this class comes from
// adding a serial step — one more provisioning call, a synchronous DNS wait, a
// second database round trip before ready. Seconds cannot be measured in a CI pod
// without a hypervisor, but the *shape* of the critical path can be, and a PR that
// adds a step fails with a diff a reviewer understands instantly.
//
// So: think hard before adding a Step here. If work can happen after the box is
// handed over, it belongs after the box is handed over.

// Step names one action on the critical path.
type Step string

const (
	StepAdmit          Step = "admit"
	StepPoolClaim      Step = "pool.claim"
	StepBind           Step = "runtime.bind"
	StepProgramNetwork Step = "runtime.program_network"
	StepUnfreeze       Step = "runtime.unfreeze"
	StepExecCanary     Step = "runtime.exec_canary"
	StepEvaluateReady  Step = "readiness.evaluate"
	StepRecordMetrics  Step = "metrics.record"
)

// RejectionReason is the bounded set of reasons a spawn did not produce a body.
// It is also the `reason` label on dada_box_spawns_total, so it must stay small.
type RejectionReason string

const (
	ReasonNone          RejectionReason = "none"
	ReasonQuota         RejectionReason = "quota"
	ReasonSpendCap      RejectionReason = "spend_cap"
	ReasonPoolExhausted RejectionReason = "pool_exhausted"
	ReasonRuntimeError  RejectionReason = "runtime_error"
	ReasonImagePull     RejectionReason = "image_pull"
	// ReasonNotReady folds into runtime_error on purpose: from the customer's
	// side "the box came up but was not usable" is the same failure as "the box
	// did not come up", and splitting the label would split the alert.
	ReasonNotReady RejectionReason = "runtime_error"
)

// RejectedError carries a classified rejection so the metric label and the API
// response derive from the same decision instead of two independent guesses.
type RejectedError struct {
	Reason RejectionReason
	Err    error
}

func (e *RejectedError) Error() string { return fmt.Sprintf("box rejected (%s): %v", e.Reason, e.Err) }
func (e *RejectedError) Unwrap() error { return e.Err }

// Reject builds a classified rejection.
func Reject(reason RejectionReason, err error) *RejectedError {
	return &RejectedError{Reason: reason, Err: err}
}

// Admitter is the gate in front of the pool: auth, quota, spend-cap precheck. It
// runs before T0's phase closes, and it must be fast — it is on the critical path.
type Admitter interface {
	Admit(ctx context.Context, spec Spec) error
}

// Deps are the seams Spawn needs. Every one of them is faked in tests.
type Deps struct {
	Clock   Clock
	Admit   Admitter
	Pool    WarmPool
	Runtime BoxRuntime
}

// SpawnResult is a claimed, ready box plus the measurement of getting there.
type SpawnResult struct {
	Instance *Instance
	Timeline *PhaseTimeline
	PoolHit  bool
	// Steps is the ordered critical path actually walked. Exposed so the golden
	// test can pin it; also useful in a failure log.
	Steps []Step
}

// poolLabel renders the pool label used on every box metric.
func poolLabel(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

// Spawn claims a pre-warmed box, binds the tenant to it, and returns once a
// command has executed inside it successfully.
//
// It does not create a box. Creation is what costs minutes, so it happens ahead of
// demand in the pool controller; a spawn is a claim. On a pool miss this function
// reports the miss and fails rather than silently paying a multi-minute cold
// start on the caller's request — the cold path is the pool controller's job, and
// hiding it here is how the headline number becomes a lie.
func Spawn(ctx context.Context, d Deps, spec Spec) (*SpawnResult, error) {
	res := &SpawnResult{Steps: make([]Step, 0, 8)}
	timeline := StartTimeline(d.Clock)
	res.Timeline = timeline

	step := func(s Step) { res.Steps = append(res.Steps, s) }

	// --- admit -------------------------------------------------------------
	step(StepAdmit)
	if err := d.Admit.Admit(ctx, spec); err != nil {
		return res, recordFailure(res, err)
	}
	if err := timeline.Complete(PhaseAdmit); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	// --- claim a warm body -------------------------------------------------
	step(StepPoolClaim)
	inst, hit, err := d.Pool.Claim(ctx, spec.Image, spec.Region)
	res.PoolHit = hit
	if !hit {
		metrics.RecordBoxPoolMiss(spec.Image)
	}
	if err != nil {
		reason := ReasonRuntimeError
		if errors.Is(err, ErrPoolExhausted) {
			reason = ReasonPoolExhausted
		}
		return res, recordFailure(res, Reject(reason, err))
	}
	res.Instance = inst
	if err := timeline.Complete(PhasePoolPop); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	// --- bind tenant identity ----------------------------------------------
	step(StepBind)
	if err := d.Runtime.Bind(ctx, inst, spec); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}
	if err := timeline.Complete(PhaseBoot); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	// --- out of quarantine into tenant egress ------------------------------
	step(StepProgramNetwork)
	if err := d.Runtime.ProgramNetwork(ctx, inst); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}
	if err := timeline.Complete(PhaseNet); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	// --- thaw and wait for the exec channel --------------------------------
	step(StepUnfreeze)
	if err := d.Runtime.Unfreeze(ctx, inst); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}
	if err := timeline.Complete(PhaseAuth); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	// --- prove it by running something inside it ---------------------------
	step(StepExecCanary)
	canary, err := d.Runtime.Exec(ctx, inst, CanaryCommand)
	if err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}
	// T1 is here: the exit status arrived. Note that canary.GuestClaimedAt is not
	// consulted — the clock inside a freshly booted box is exactly the thing that
	// is wrong, and this is the number the product is sold on.
	if err := timeline.Complete(PhaseCanary); err != nil {
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}

	step(StepEvaluateReady)
	if err := EvaluateReadiness(canary); err != nil {
		return res, recordFailure(res, Reject(ReasonNotReady, err))
	}

	// --- measure -----------------------------------------------------------
	step(StepRecordMetrics)
	if err := timeline.Validate(); err != nil {
		// A timeline that does not add up must not be reported as a measurement.
		// The box is fine; the instrumentation is not, and quietly publishing a
		// wrong number is worse than publishing none.
		return res, recordFailure(res, Reject(ReasonRuntimeError, err))
	}
	metrics.RecordBoxReady(poolLabel(hit), spec.Region, timeline.Total(), timeline.Durations())
	metrics.RecordBoxSpawnOutcome("ready", poolLabel(hit), string(ReasonNone))
	return res, nil
}

// recordFailure classifies and counts a failed spawn, then returns the error
// unchanged so callers can still inspect it.
func recordFailure(res *SpawnResult, err error) error {
	reason := ReasonRuntimeError
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		reason = rejected.Reason
	}

	result := "failed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		result = "timeout"
	} else if reason == ReasonQuota || reason == ReasonSpendCap {
		result = "rejected"
	}

	metrics.RecordBoxSpawnOutcome(result, poolLabel(res.PoolHit), string(reason))
	return err
}
