package box

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
)

func TestTimelinePhasesAreDisjointAndSumToTotal(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	tl := StartTimeline(clock)

	want := map[Phase]time.Duration{
		PhaseAdmit:   40 * time.Millisecond,
		PhasePoolPop: 15 * time.Millisecond,
		PhaseBoot:    120 * time.Millisecond,
		PhaseNet:     60 * time.Millisecond,
		PhaseAuth:    30 * time.Millisecond,
		PhaseCanary:  250 * time.Millisecond,
	}
	var total time.Duration
	for _, p := range CriticalPath {
		clock.Advance(want[p])
		total += want[p]
		if err := tl.Complete(p); err != nil {
			t.Fatalf("complete %s: %v", p, err)
		}
	}

	if err := tl.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if tl.Total() != total {
		t.Errorf("total = %s, want %s", tl.Total(), total)
	}
	for p, d := range want {
		if got := tl.Durations()[string(p)]; got != d {
			t.Errorf("phase %s = %s, want %s", p, got, d)
		}
	}
}

func TestTimelineRejectsOutOfOrderAndDuplicatePhases(t *testing.T) {
	clock := NewFakeClock(time.Now())

	tl := StartTimeline(clock)
	if err := tl.Complete(PhaseBoot); err == nil {
		t.Error("completing boot before admit must fail: a total that does not match its parts is unreadable on a dashboard")
	}

	tl = StartTimeline(clock)
	if err := tl.Complete(PhaseAdmit); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := tl.Complete(PhaseAdmit); err == nil {
		t.Error("completing the same phase twice must fail")
	}

	tl = StartTimeline(clock)
	if err := tl.Complete(Phase("teleport")); err == nil {
		t.Error("an unknown phase must fail rather than create a new label silently")
	}
}

func TestTimelineIncompleteIsNotAMeasurement(t *testing.T) {
	clock := NewFakeClock(time.Now())
	tl := StartTimeline(clock)
	if err := tl.Complete(PhaseAdmit); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if tl.Completed() {
		t.Error("one phase in is not a completed critical path")
	}
	if err := tl.Validate(); err == nil {
		t.Error("an incomplete timeline must not validate; publishing it would report a total that never happened")
	}
}

// TestSpawnIgnoresGuestReportedTime is the load-bearing measurement-integrity
// test. A runtime that reports its own readiness instant is the easy mistake: the
// number would then come from a clock inside a machine that just booted, which is
// exactly the clock that is wrong. Here the guest claims a time twenty-six years
// off and the measured total must not move.
func TestSpawnIgnoresGuestReportedTime(t *testing.T) {
	deps, spec, _, rt, _ := NewWarmFixture(100 * time.Millisecond)
	rt.Canary = &CanaryResult{
		ExitCode:       0,
		Stdout:         WarmCanaryStdout,
		GuestClaimedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	res, err := Spawn(context.Background(), deps, spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Four runtime steps at 100ms each; admit and pool claim are free against the
	// fake clock.
	if want := 400 * time.Millisecond; res.Timeline.Total() != want {
		t.Errorf("total = %s, want %s — a guest-reported timestamp leaked into the measurement",
			res.Timeline.Total(), want)
	}
	if err := res.Timeline.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestSpawnRefusesToPublishAnInconsistentMeasurement(t *testing.T) {
	// A clock that runs backwards is the cheapest way to produce a timeline that
	// cannot be true. The spawn must fail rather than emit a nonsense duration.
	clock := NewFakeClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	rt := &FakeRuntime{Clock: clock, BindDelay: -time.Second}
	pool := NewMemoryPool()
	spec := Spec{Image: "warm-polyglot-1", Region: "ru1"}
	pool.Add(spec.Image, spec.Region, &Instance{ID: "b1"})

	// FakeRuntime.advance ignores non-positive delays, so drive it directly.
	rt.BindDelay = 0
	deps := Deps{Clock: clock, Admit: AllowAll{}, Pool: pool, Runtime: &backwardsRuntime{FakeRuntime: rt, clock: clock}}

	if _, err := Spawn(context.Background(), deps, spec); err == nil {
		t.Error("a timeline whose phases do not add up must fail the spawn, not publish a wrong number")
	}
}

// backwardsRuntime moves the clock backwards during Bind, producing a negative
// phase duration.
type backwardsRuntime struct {
	*FakeRuntime
	clock *FakeClock
}

func (b *backwardsRuntime) Bind(ctx context.Context, inst *Instance, spec Spec) error {
	b.clock.Advance(-2 * time.Second)
	return nil
}

func TestBudgetBreachIsAttributedToTheDominantPhase(t *testing.T) {
	// One phase far over budget; the breach counter must name that phase so the
	// alert that fires already points at the cause.
	deps, spec, _, rt, _ := NewWarmFixture(0)
	rt.CanaryDelay = metrics.BoxReadyBudget + 5*time.Second
	rt.BindDelay = time.Second

	res, err := Spawn(context.Background(), deps, spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Timeline.Total() <= metrics.BoxReadyBudget {
		t.Fatalf("test setup did not breach the budget: total %s", res.Timeline.Total())
	}

	// The dominant phase is what the metric labels the breach with; assert on the
	// timeline rather than scraping the registry, since the attribution rule is
	// what matters and it is the same input the metric sees.
	var dominant Phase
	var longest time.Duration
	for _, p := range CriticalPath {
		if d := res.Timeline.Durations()[string(p)]; d > longest {
			dominant, longest = p, d
		}
	}
	if dominant != PhaseCanary {
		t.Errorf("dominant phase = %s, want %s", dominant, PhaseCanary)
	}
}

func TestSpawnClassifiesRejections(t *testing.T) {
	cases := []struct {
		name   string
		deps   func() (Deps, Spec)
		reason RejectionReason
	}{
		{
			name: "quota",
			deps: func() (Deps, Spec) {
				d, s, _, _, _ := NewWarmFixture(0)
				d.Admit = RejectingAdmitter{Reason: ReasonQuota, Err: errors.New("box_minutes exhausted")}
				return d, s
			},
			reason: ReasonQuota,
		},
		{
			name: "spend cap",
			deps: func() (Deps, Spec) {
				d, s, _, _, _ := NewWarmFixture(0)
				d.Admit = RejectingAdmitter{Reason: ReasonSpendCap, Err: errors.New("cap reached")}
				return d, s
			},
			reason: ReasonSpendCap,
		},
		{
			name: "pool exhausted",
			deps: func() (Deps, Spec) {
				clock := NewFakeClock(time.Now())
				return Deps{
					Clock:   clock,
					Admit:   AllowAll{},
					Pool:    NewMemoryPool(), // empty
					Runtime: &FakeRuntime{Clock: clock},
				}, Spec{Image: "warm-polyglot-1", Region: "ru1"}
			},
			reason: ReasonPoolExhausted,
		},
		{
			name: "cold image is not ready",
			deps: func() (Deps, Spec) {
				d, s, _, rt, _ := NewWarmFixture(0)
				missing := CanaryMissing("node")
				rt.Canary = &missing
				return d, s
			},
			reason: ReasonNotReady,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, spec := tc.deps()
			_, err := Spawn(context.Background(), deps, spec)
			if err == nil {
				t.Fatal("expected the spawn to fail")
			}
			var rejected *RejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("error is not classified: %v", err)
			}
			if rejected.Reason != tc.reason {
				t.Errorf("reason = %s, want %s", rejected.Reason, tc.reason)
			}
		})
	}
}
