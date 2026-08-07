package api

import (
	"math"
	"testing"

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
		{"read-only that keeps growing freezes", dbEnforcementReadOnly, 1.3, dbEnforcementFrozen},
		{"read-only releases below the release line", dbEnforcementReadOnly, 0.5, dbEnforcementNone},
		{"frozen holds while still over", dbEnforcementFrozen, 1.1, dbEnforcementFrozen},
		{"frozen releases below the release line", dbEnforcementFrozen, 0.8, dbEnforcementNone},
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
