package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// collectedSeries counts how many series a collector currently exposes.
func collectedSeries(c prometheus.Collector) int {
	ch := make(chan prometheus.Metric, 64)
	go func() {
		c.Collect(ch)
		close(ch)
	}()
	n := 0
	for range ch {
		n++
	}
	return n
}

func TestSetDBRoutingPublishesZeroForEveryReason(t *testing.T) {
	SetDBRouting(0, nil, nil)

	for _, reason := range routeDropReasons {
		if got := gaugeValue(t, dbRoutesDropped.WithLabelValues(reason)); got != 0 {
			t.Fatalf("reason %q = %v, want an explicit 0 so the alert resolves on a real zero rather than on missing data", reason, got)
		}
	}
}

func TestSetDBRoutingClearsShardsThatDisappear(t *testing.T) {
	SetDBRouting(1, map[string]int{RouteDropAmbiguousName: 2}, map[string]int{"shard-1": 3, "shard-2": 1})
	if got := gaugeValue(t, dbDatabasesByShard.WithLabelValues("shard-2")); got != 1 {
		t.Fatalf("shard-2 = %v, want 1", got)
	}
	if got := gaugeValue(t, dbRoutesDropped.WithLabelValues(RouteDropAmbiguousName)); got != 2 {
		t.Fatalf("ambiguous = %v, want 2", got)
	}
	if got := gaugeValue(t, dbRoutes); got != 1 {
		t.Fatalf("routed = %v, want 1", got)
	}

	SetDBRouting(0, nil, map[string]int{"shard-1": 4})
	if n := collectedSeries(dbDatabasesByShard); n != 1 {
		t.Fatalf("series after a shard emptied = %d, want 1: a stale shard would keep reporting databases it no longer holds", n)
	}
	if got := gaugeValue(t, dbRoutesDropped.WithLabelValues(RouteDropAmbiguousName)); got != 0 {
		t.Fatalf("ambiguous = %v, want 0 once the collision is gone", got)
	}
}
