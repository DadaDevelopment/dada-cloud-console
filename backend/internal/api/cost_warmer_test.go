package api

import (
	"slices"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
)

// TestCostWindowSplit pins the property the two warm loops depend on: every
// window the UI may request is warmed by exactly one loop. A window that fell
// out of both lists would still validate and then be served cold on every
// request, paying the full OpenCost aggregation on the user path.
func TestCostWindowSplit(t *testing.T) {
	for _, w := range []string{"24h", "7d", "14d", "30d"} {
		if !costWindowAllowed(w) {
			t.Errorf("window %q must stay allowed", w)
		}
		inFast := slices.Contains(fastCostWindows, w)
		inSlow := slices.Contains(slowCostWindows, w)
		if inFast == inSlow {
			t.Errorf("window %q: want exactly one of fast/slow, got fast=%v slow=%v", w, inFast, inSlow)
		}
	}
	for _, w := range []string{"", "1h", "90d", "24h; drop"} {
		if costWindowAllowed(w) {
			t.Errorf("window %q must be rejected", w)
		}
	}
}

// TestCostCacheTTLPerWindow guards the read/write agreement: a long window must
// get the long TTL on BOTH the warm path and a user request that missed, or a
// miss would re-store 30d under the short TTL and hand the next user the full
// ~100s aggregation.
func TestCostCacheTTLPerWindow(t *testing.T) {
	h := &Handler{cfg: &config.Config{
		CacheCostTTL:     300 * time.Second,
		CacheCostSlowTTL: 5400 * time.Second,
	}}
	for _, w := range fastCostWindows {
		if got := h.costCacheTTL(w); got != 300*time.Second {
			t.Errorf("costCacheTTL(%q) = %v, want CacheCostTTL", w, got)
		}
	}
	for _, w := range slowCostWindows {
		if got := h.costCacheTTL(w); got != 5400*time.Second {
			t.Errorf("costCacheTTL(%q) = %v, want CacheCostSlowTTL", w, got)
		}
	}
}

// TestCostWarmIntervalsFitTTL is the regression test for the incident this
// split came from: the warmer ran on CacheCostTTL/2 while a sweep took longer
// than the TTL itself, so ticks never idled and OpenCost's per-day PromQL
// fan-out pinned Mimir at ~1.7 CPU. Refresh must be more frequent than
// expiry on both loops, with the slow loop's margin wide enough that one
// failed tick does not expose a cold entry.
func TestCostWarmIntervalsFitTTL(t *testing.T) {
	t.Setenv("DB_URL", "postgres://user:pass@localhost:5432/console")
	t.Setenv("JWT_SECRET", "test-secret")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.CostWarmInterval >= cfg.CacheCostTTL {
		t.Errorf("CostWarmInterval %v must be shorter than CacheCostTTL %v", cfg.CostWarmInterval, cfg.CacheCostTTL)
	}
	if cfg.CostSlowWarmInterval*2 >= cfg.CacheCostSlowTTL {
		t.Errorf("CacheCostSlowTTL %v must survive two missed slow ticks of %v", cfg.CacheCostSlowTTL, cfg.CostSlowWarmInterval)
	}
}
