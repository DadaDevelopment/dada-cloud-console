package box

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Fakes for the box seams.
//
// They live in a non-test file so packages above this one (the API handlers, the
// meter) can exercise the ready path without a hypervisor. Everything a test
// needs to steer — per-phase delay, per-call failure, canary output — is a field,
// so a test reads as a statement about behaviour rather than a mock script.

// FakeClock is a manually advanced Clock. The ready path takes every timestamp
// from the orchestrator's clock, so driving this clock drives the measurement
// exactly, with no sleeps and no flakes.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock starts a clock at a fixed instant.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// AllowAll is an Admitter that admits everything.
type AllowAll struct{}

// Admit always succeeds.
func (AllowAll) Admit(context.Context, Spec) error { return nil }

// RejectingAdmitter refuses every spawn with a fixed classified reason.
type RejectingAdmitter struct {
	Reason RejectionReason
	Err    error
}

// Admit always fails with the configured rejection.
func (r RejectingAdmitter) Admit(context.Context, Spec) error {
	err := r.Err
	if err == nil {
		err = fmt.Errorf("admission refused")
	}
	return Reject(r.Reason, err)
}

// WarmCanaryStdout is the output a correctly warmed box produces. Tests that want
// a healthy box use this; tests about readiness mutate it.
const WarmCanaryStdout = "dada-ready\n" +
	"node=v22.11.0\n" +
	"python=Python 3.12.7\n" +
	"go=go version go1.23.4 linux/amd64\n" +
	"git=git version 2.43.0\n" +
	"docker=27.3.1\n"

// FakeRuntime is a BoxRuntime that costs whatever the test says it costs.
//
// Delays are applied by advancing Clock rather than by sleeping, so a test that
// describes a 40-second boot runs instantly and deterministically.
type FakeRuntime struct {
	Clock *FakeClock

	// Delay per operation, applied by advancing the clock.
	BindDelay     time.Duration
	NetworkDelay  time.Duration
	UnfreezeDelay time.Duration
	CanaryDelay   time.Duration

	// Errors to return instead of succeeding.
	BindErr     error
	NetworkErr  error
	UnfreezeErr error
	ExecErr     error

	// Canary is what Exec reports. Zero value means a healthy warm box.
	Canary *CanaryResult

	mu       sync.Mutex
	execCmds []string
	destroys int
}

func (f *FakeRuntime) advance(d time.Duration) {
	if f.Clock != nil && d > 0 {
		f.Clock.Advance(d)
	}
}

// Bind binds tenant identity.
func (f *FakeRuntime) Bind(context.Context, *Instance, Spec) error {
	f.advance(f.BindDelay)
	return f.BindErr
}

// ProgramNetwork moves the box into tenant egress.
func (f *FakeRuntime) ProgramNetwork(context.Context, *Instance) error {
	f.advance(f.NetworkDelay)
	return f.NetworkErr
}

// Unfreeze thaws the box.
func (f *FakeRuntime) Unfreeze(context.Context, *Instance) error {
	f.advance(f.UnfreezeDelay)
	return f.UnfreezeErr
}

// Exec records the command and returns the configured canary result.
func (f *FakeRuntime) Exec(_ context.Context, _ *Instance, cmd string) (CanaryResult, error) {
	f.advance(f.CanaryDelay)
	f.mu.Lock()
	f.execCmds = append(f.execCmds, cmd)
	f.mu.Unlock()

	if f.ExecErr != nil {
		return CanaryResult{}, f.ExecErr
	}
	if f.Canary != nil {
		return *f.Canary, nil
	}
	return CanaryResult{ExitCode: 0, Stdout: WarmCanaryStdout}, nil
}

// Destroy counts destructions.
func (f *FakeRuntime) Destroy(context.Context, *Instance) error {
	f.mu.Lock()
	f.destroys++
	f.mu.Unlock()
	return nil
}

// ExecutedCommands returns the commands Exec was asked to run.
func (f *FakeRuntime) ExecutedCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.execCmds...)
}

// Destroys returns how many times Destroy was called.
func (f *FakeRuntime) Destroys() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.destroys
}

// NewWarmFixture wires a fake clock, a one-instance pool and a healthy runtime —
// the arrangement most tests want. phaseDelay is charged to each runtime step, so
// the resulting total is a round multiple and easy to assert on.
func NewWarmFixture(phaseDelay time.Duration) (Deps, Spec, *FakeClock, *FakeRuntime, *MemoryPool) {
	clock := NewFakeClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	rt := &FakeRuntime{
		Clock:         clock,
		BindDelay:     phaseDelay,
		NetworkDelay:  phaseDelay,
		UnfreezeDelay: phaseDelay,
		CanaryDelay:   phaseDelay,
	}
	spec := Spec{Image: "warm-polyglot-1", Profile: "box-small", Region: "ru1"}
	pool := NewMemoryPool()
	pool.SetTarget(spec.Image, spec.Region, 4)
	pool.Add(spec.Image, spec.Region, &Instance{
		ID:          "box-fixture-1",
		InstanceRef: "fc-fixture-1",
		Image:       spec.Image,
		Region:      spec.Region,
	})

	return Deps{Clock: clock, Admit: AllowAll{}, Pool: pool, Runtime: rt}, spec, clock, rt, pool
}

// CanaryMissing returns warm canary output with one toolchain key blanked, for
// testing that a fast box with a cold image is not "ready".
func CanaryMissing(tool string) CanaryResult {
	lines := strings.Split(strings.TrimRight(WarmCanaryStdout, "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, tool+"=") {
			lines[i] = tool + "="
		}
	}
	return CanaryResult{ExitCode: 0, Stdout: strings.Join(lines, "\n") + "\n"}
}
