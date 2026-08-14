package api

import (
	"math"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/prometheus"
)

// Every tier the composition declares must have a storage limit here, or a
// database on it would silently get no quota at all — the exact failure the
// quota worker exists to prevent.
func TestDatabaseTierLimits_CoverEveryTier(t *testing.T) {
	for tier := range chartTiers {
		if _, ok := databaseTierLimitBytes[tier]; !ok {
			t.Errorf("tier %q has no storage limit: its databases would grow unbounded", tier)
		}
	}
	for tier := range databaseTierLimitBytes {
		if !chartTiers[tier] {
			t.Errorf("databaseTierLimitBytes has stale tier %q", tier)
		}
	}
	if databaseTierLimitBytes["free"] >= databaseTierLimitBytes["starter"] ||
		databaseTierLimitBytes["starter"] >= databaseTierLimitBytes["business"] {
		t.Error("paid tiers must grant strictly more storage than the ones below them")
	}
}

func TestDecideDBQuotaState_Ladder(t *testing.T) {
	cases := []struct {
		name    string
		current string
		ratio   float64
		want    string
	}{
		{"below quota stays free", dbEnforcementNone, 0.5, dbEnforcementNone},
		{"warning zone does not enforce", dbEnforcementNone, 0.95, dbEnforcementNone},
		{"at quota goes read-only", dbEnforcementNone, 1.0, dbEnforcementReadOnly},
		{"far over quota still goes read-only first", dbEnforcementNone, 5.0, dbEnforcementReadOnly},
		{"read-only holds inside the gap", dbEnforcementReadOnly, 0.95, dbEnforcementReadOnly},
		{"read-only that keeps growing never escalates", dbEnforcementReadOnly, 5.0, dbEnforcementReadOnly},
		{"read-only releases below the release line", dbEnforcementReadOnly, 0.5, dbEnforcementNone},
		{"legacy frozen downgrades to read-only", dbEnforcementFrozen, 1.1, dbEnforcementReadOnly},
		{"legacy frozen releases below the release line", dbEnforcementFrozen, 0.8, dbEnforcementNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideDBQuotaState(c.current, c.ratio); got != c.want {
				t.Errorf("decideDBQuotaState(%q, %.2f) = %q, want %q", c.current, c.ratio, got, c.want)
			}
		})
	}
}

// A database parked right at its quota must not alternate between enforced and
// released: each flip costs a git commit and an ALTER ROLE, and the owner would
// get a mail per tick.
func TestDecideDBQuotaState_NoFlapAtTheLimit(t *testing.T) {
	state := dbEnforcementNone
	for i := 0; i < 10; i++ {
		next := decideDBQuotaState(state, 1.001)
		if i > 0 && next != state {
			t.Fatalf("state flapped on tick %d: %q -> %q", i, state, next)
		}
		state = next
	}
	if state != dbEnforcementReadOnly {
		t.Fatalf("settled on %q, want read-only", state)
	}
}

// The ladder must never ask for frozen again: a database whose owner cannot
// connect cannot be archived or emptied, so freezing removes the only ways out
// of the state it punishes. The XRD keeps accepting the value; nothing here
// produces it.
func TestDecideDBQuotaState_NeverFreezes(t *testing.T) {
	for _, current := range []string{dbEnforcementNone, dbEnforcementReadOnly, dbEnforcementFrozen} {
		for _, ratio := range []float64{0, 0.5, 0.95, 1.0, 1.25, 10} {
			if got := decideDBQuotaState(current, ratio); got == dbEnforcementFrozen {
				t.Fatalf("decideDBQuotaState(%q, %.2f) returned frozen", current, ratio)
			}
		}
	}
}

// A database that is already over quota the first time a tier lands on it gets
// one day, not enforcement. The day must be granted once per excursion: if
// every tick re-granted it, enforcement would never arrive.
func TestApplyDBQuotaGrace_GrantedOnceThenEnforces(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	state, start, grace := applyDBQuotaGrace(dbEnforcementReadOnly, nil, now)
	if state != dbEnforcementNone || !start || grace == nil {
		t.Fatalf("first sight = (%q, %v, %v), want (none, true, deadline)", state, start, grace)
	}
	if got := grace.Sub(now); got != dbQuotaGraceWindow {
		t.Fatalf("grace window %s, want %s", got, dbQuotaGraceWindow)
	}
	first := *grace

	for i := 1; i <= 5; i++ {
		later := now.Add(time.Duration(i) * time.Hour)
		state, start, grace = applyDBQuotaGrace(dbEnforcementReadOnly, grace, later)
		if state != dbEnforcementNone || start {
			t.Fatalf("tick %d inside grace = (%q, %v), want (none, false)", i, state, start)
		}
		if !grace.Equal(first) {
			t.Fatalf("tick %d moved the deadline to %s, want %s", i, grace, first)
		}
	}

	state, start, grace = applyDBQuotaGrace(dbEnforcementReadOnly, grace, first.Add(time.Minute))
	if state != dbEnforcementReadOnly || start || !grace.Equal(first) {
		t.Fatalf("after expiry = (%q, %v, %v), want (read-only, false, unchanged deadline)", state, start, grace)
	}
}

// Coming back under the release threshold clears the grace deadline, so a
// database that fills up again months later gets a fresh day rather than
// instant enforcement on a stale deadline.
func TestApplyDBQuotaGrace_ClearedOnRelease(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)

	state, start, grace := applyDBQuotaGrace(dbEnforcementNone, &expired, now)
	if state != dbEnforcementNone || start || grace != nil {
		t.Fatalf("release = (%q, %v, %v), want (none, false, nil)", state, start, grace)
	}

	state, start, grace = applyDBQuotaGrace(dbEnforcementReadOnly, nil, now)
	if state != dbEnforcementNone || !start || grace == nil || !grace.After(now) {
		t.Fatalf("refill = (%q, %v, %v), want a fresh grace window", state, start, grace)
	}
}

func TestDBSizesByDatnameFrom_DropsGarbage(t *testing.T) {
	samples := []prometheus.Sample{
		{Metric: map[string]string{"datname": "good"}, Point: prometheus.Point{V: 1024}},
		{Metric: map[string]string{}, Point: prometheus.Point{V: 2048}},
		{Metric: map[string]string{"datname": "nan"}, Point: prometheus.Point{V: math.NaN()}},
		{Metric: map[string]string{"datname": "inf"}, Point: prometheus.Point{V: math.Inf(1)}},
		{Metric: map[string]string{"datname": "negative"}, Point: prometheus.Point{V: -1}},
	}
	got := dbSizesByDatnameFrom(samples)
	if len(got) != 1 || got["good"] != 1024 {
		t.Fatalf("got %v, want only good=1024 (a bogus sample must never drive enforcement)", got)
	}
}
