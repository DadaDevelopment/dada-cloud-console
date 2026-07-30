package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestRecordBoxMeteredMinutesAdvancesBothCounters is the guard on the claim
// "idle is not billed".
//
// That claim is a statement about a RATIO, and a ratio needs both terms to be
// written by the same call. If a bug stopped the idle counter advancing, a dashboard
// would read "everything we meter is billable" — the flattering direction, and
// therefore the one that has to be structurally impossible rather than merely
// unlikely. So the two counters share one entry point, and this test asserts they
// move independently and correctly from it.
func TestRecordBoxMeteredMinutesAdvancesBothCounters(t *testing.T) {
	beforeActive := metricValue(t, boxActiveMinutes.WithLabelValues("free"))
	beforeIdle := metricValue(t, boxIdleMinutes.WithLabelValues("free"))

	RecordBoxMeteredMinutes("free", 10, 50)

	if got := metricValue(t, boxActiveMinutes.WithLabelValues("free")) - beforeActive; got != 10 {
		t.Errorf("dada_box_active_minutes_total moved by %v, want 10", got)
	}
	if got := metricValue(t, boxIdleMinutes.WithLabelValues("free")) - beforeIdle; got != 50 {
		t.Errorf("dada_box_idle_minutes_total moved by %v, want 50", got)
	}
}

// TestRecordBoxMeteredMinutesDoesNotCreateEmptySeries: a plan with nothing to report
// must not mint a child series full of zeros. A CounterVec child exists from the
// moment it is touched, so touching one per plan per tick would publish a permanent
// zero series for every plan on the platform whether or not it has any boxes.
func TestRecordBoxMeteredMinutesDoesNotCreateEmptySeries(t *testing.T) {
	const plan = "plan-that-has-no-boxes"
	RecordBoxMeteredMinutes(plan, 0, 0)
	// Reading through WithLabelValues would itself create the child, so the assertion
	// has to be on the gathered families instead.
	for _, family := range []string{"dada_box_active_minutes_total", "dada_box_idle_minutes_total"} {
		if n := countChildren(t, family, plan); n != 0 {
			t.Errorf("%s gained %d series for a plan with no boxes, want 0", family, n)
		}
	}
}

// countChildren counts the series of one metric family carrying the given label
// value, read from the registry promauto registered them in.
func countChildren(t *testing.T, family, labelValue string) int {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	n := 0
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetValue() == labelValue {
					n++
				}
			}
		}
	}
	return n
}

// TestSetBoxMeteredMinutesLagPublishesSeconds: the gauge is in seconds, because the
// alert rule that watches it is written in seconds. A duration published as
// nanoseconds would make a 30-second lag look like a catastrophe and a real
// catastrophe indistinguishable from anything else.
func TestSetBoxMeteredMinutesLagPublishesSeconds(t *testing.T) {
	t.Cleanup(func() { SetBoxMeteredMinutesLag(0) })
	SetBoxMeteredMinutesLag(90 * time.Second)
	if got := metricValue(t, boxMeteredMinutesLag); got != 90 {
		t.Errorf("dada_box_metered_minutes_lag_seconds = %v, want 90", got)
	}
}

// TestSetBoxSpendCapMaxRatioIsOwnedByTheMeter: the gauge has no per-box label, on
// purpose — it answers "is anyone about to be cut off" without unbounded cardinality.
// The value is the fleet MAXIMUM, so it must be settable to any ratio including one
// above 1.0 (a box that blew past its cap between two ticks).
func TestSetBoxSpendCapMaxRatioIsOwnedByTheMeter(t *testing.T) {
	t.Cleanup(func() { SetBoxSpendCapMaxRatio(0) })
	for _, ratio := range []float64{0, 0.5, 1.0, 1.4} {
		SetBoxSpendCapMaxRatio(ratio)
		if got := metricValue(t, boxSpendCapMaxRatio); got != ratio {
			t.Fatalf("dada_box_spend_cap_max_ratio = %v, want %v", got, ratio)
		}
	}
}

// TestRecordBoxSpendCapHitUsesTheDocumentedActions keeps the label set bounded to the
// three values the metric's own Help text promises. A fourth value invented at a call
// site would be a series no dashboard draws and no alert watches.
func TestRecordBoxSpendCapHitUsesTheDocumentedActions(t *testing.T) {
	for _, action := range []string{"warned", "throttled", "stopped"} {
		before := metricValue(t, boxSpendCapHits.WithLabelValues(action))
		RecordBoxSpendCapHit(action)
		if got := metricValue(t, boxSpendCapHits.WithLabelValues(action)) - before; got != 1 {
			t.Errorf("dada_box_spend_cap_hits_total{action=%q} moved by %v, want 1", action, got)
		}
	}
}

// TestRecordBoxMeterErrorCounts: a metering error must be countable, because the
// failure mode it guards against (the meter silently not writing rows) is otherwise
// invisible until an invoice is short.
func TestRecordBoxMeterErrorCounts(t *testing.T) {
	before := metricValue(t, boxMeterErrors)
	RecordBoxMeterError()
	if got := metricValue(t, boxMeterErrors) - before; got != 1 {
		t.Errorf("dada_box_meter_errors_total moved by %v, want 1", got)
	}
}
