package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

// Dada Box metrics.
//
// These are declared BEFORE the box runtime exists, on purpose: the runtime, the
// crystallizer and the test harness all emit into a fixed contract instead of
// inventing names later, and the latency budget becomes a reviewable artifact
// from day one. See docs/plans/2026-07-29-box-test-and-measurement.md and
// docs/runbooks/box-latency-budget.md.
//
// Time to ready is the one product claim marketing cannot compensate for: if a
// box is not ready in seconds there is no product. So it gets the same
// budget-as-code treatment as the HTTP latency rule in http.go — a const budget,
// a histogram, and a separate breach counter to alert on.
//
// Two rules that the shape of these metrics enforces:
//
//   - Every phase timestamp is taken by the orchestrator observing the guest. A
//     guest-reported timestamp is never trusted: a fresh box's clock is exactly
//     the thing that is wrong.
//   - dada_box_idle_minutes_total exists alongside dada_box_active_minutes_total
//     so "idle is not billed" is a queryable claim rather than a promise.
//
// Metering carries no per-org label. Per-org truth belongs in usage_records;
// Prometheus carries fleet aggregates only, so cardinality stays bounded (the
// same lesson as the route label in http.go).

// BoxReadyBudget is the ceiling on time-to-ready for a warm-pool hit. A breach is
// logged at WARN and counted in dada_box_ready_budget_breaches_total, labelled
// with the phase that dominated the breach so the alert names its own culprit.
//
// It is deliberately looser than the p50 target (3s) and the p95 target (8s):
// the budget is the "something is wrong" line, not the goal. A single spawn over
// 15s is a hard failure and is asserted by the nightly rehearsal, not here.
const BoxReadyBudget = 10 * time.Second

// boxDurationBuckets spans 0.5s to 2min. The HTTP histogram in http.go tops out
// at 10s, which is the wrong regime for box lifecycle: a cold-path spawn that
// takes 40s must remain distinguishable from one that takes 120s, and both must
// be distinguishable from the 3s warm path we actually sell.
var boxDurationBuckets = []float64{0.5, 1, 2, 3, 5, 8, 10, 15, 20, 30, 45, 60, 120}

// boxCrystallizeBuckets spans 5s to 10min. Crystallization is a minutes-scale
// saga (provision a VM, move volumes, issue a cert), so it needs its own scale.
var boxCrystallizeBuckets = []float64{5, 10, 30, 60, 120, 300, 600}

var (
	// --- lifecycle ---

	boxSpawns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_spawns_total",
		Help: "Box spawn attempts. result=ready|failed|timeout|rejected, pool=hit|miss, reason=none|quota|spend_cap|pool_exhausted|runtime_error|image_pull. rate(result=\"failed\") > 0 means agents are being denied a body.",
	}, []string{"result", "pool", "reason"})

	boxes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_boxes",
		Help: "Live boxes grouped by phase (requested|booting|ready|idle|sleeping|crystallizing|failed). Refreshed by the state collector.",
	}, []string{"phase"})

	boxFailedRecent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_box_failed_recent",
		Help: "Boxes that reached a failed state within the last hour. Alert on >0; it clears itself as failures age out, so it tracks live breakage rather than historical totals (same shape as dada_builds_failed_recent).",
	})

	boxDestroys = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_destroys_total",
		Help: "Boxes destroyed, by cause (user|ttl|spend_cap|abuse|crystallized). cause=\"crystallized\" is a success: the box graduated to a permanent VM.",
	}, []string{"cause"})

	// --- time to ready ---

	boxReadyDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_ready_duration_seconds",
		Help:    "Server-side time to ready: from spawn admission to the canary exec's exit status arriving from inside the box. Labelled by pool (hit|miss) and region. Warm-path targets: p50 <= 3s, p95 <= 8s.",
		Buckets: boxDurationBuckets,
	}, []string{"pool", "region"})

	boxPhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_phase_duration_seconds",
		Help:    "Time to ready broken down by phase (admit|pool_pop|boot|net|auth|canary). Phases are disjoint and sum to the total, so a regression names its own culprit instead of hiding behind a fast total.",
		Buckets: boxDurationBuckets,
	}, []string{"phase", "pool"})

	boxReadyBudgetBreaches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_ready_budget_breaches_total",
		Help: "Spawns whose time to ready exceeded BoxReadyBudget. The phase label is the phase that dominated that spawn, so the alert points at the cause. rate() > 0 means the core product claim is regressing.",
	}, []string{"pool", "phase"})

	boxClientReadyDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_client_ready_duration_seconds",
		Help:    "Client-perceived time to ready, reported by the synthetic canary or the CLI: includes the client's own TLS/SSH handshake. This is the number quoted publicly — it comes from production, not from a spreadsheet.",
		Buckets: boxDurationBuckets,
	}, []string{"pool"})

	boxPoolAvailable = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_box_pool_available",
		Help: "Free pre-warmed boxes per image and region. Hitting 0 means the next spawn pays a cold start.",
	}, []string{"image", "region"})

	boxPoolTarget = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_box_pool_target",
		Help: "Target number of free pre-warmed boxes per image and region. Compare with dada_box_pool_available to see whether the pool controller is keeping up.",
	}, []string{"image", "region"})

	boxPoolMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_pool_misses_total",
		Help: "Spawns that found no warm box and paid a cold start, by image. Target: under 2% of spawns.",
	}, []string{"image"})

	boxPoolRefillDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_pool_refill_duration_seconds",
		Help:    "Time for the pool controller to bring a replacement warm box to ready, by image. This is what decides whether a burst of spawns turns into a run of pool misses.",
		Buckets: boxDurationBuckets,
	}, []string{"image"})

	// --- attach / expose ---

	boxAttachDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_attach_duration_seconds",
		Help:    "Time to attach a managed resource to a running box, by resource (postgres|s3), measured to a usable credential rather than to the API response.",
		Buckets: boxDurationBuckets,
	}, []string{"resource"})

	boxAttachTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_attach_total",
		Help: "Attach attempts by resource (postgres|s3) and result (success|failed).",
	}, []string{"resource", "result"})

	boxExposeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_expose_duration_seconds",
		Help:    "Time from expose request to the first public 200, by cert path (wildcard|acme). The wildcard path is the one we ship; acme is measured to prove why.",
		Buckets: boxDurationBuckets,
	}, []string{"cert"})

	// --- metering ---

	boxActiveMinutes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_active_minutes_total",
		Help: "Billed active box-minutes by plan. Fleet aggregate only — per-org truth lives in usage_records.",
	}, []string{"plan"})

	boxIdleMinutes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_idle_minutes_total",
		Help: "Unbilled idle box-minutes by plan. Its existence alongside dada_box_active_minutes_total is what makes \"idle is not billed\" a queryable claim: idle advances while active does not.",
	}, []string{"plan"})

	boxMeteredMinutesLag = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_box_metered_minutes_lag_seconds",
		Help: "Age of the newest metered box-minute. A stalled meter is silent revenue loss, so this is alerted on rather than left to a monthly reconciliation.",
	})

	boxMeterErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "dada_box_meter_errors_total",
		Help: "Errors while metering box minutes.",
	})

	// --- spend cap ---

	boxSpendCapHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_spend_cap_hits_total",
		Help: "Per-box spend cap actions taken, by action (warned|throttled|stopped). stopped suspends the box; it never deletes it, so the customer's data survives their runaway.",
	}, []string{"action"})

	boxSpendCapMaxRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_box_spend_cap_max_ratio",
		Help: "Highest spend-to-cap ratio in the fleet. No per-box label on purpose: this answers \"is anyone about to be cut off\" without unbounded cardinality.",
	})

	// --- crystallization ---

	boxCrystallizations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_crystallizations_total",
		Help: "Crystallization attempts by result (success|failed|rolled_back) and the stage it ended in (none|capture|provision|seed|dns|cert|cutover|verify).",
	}, []string{"result", "stage"})

	boxCrystallizeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dada_box_crystallize_duration_seconds",
		Help:    "End-to-end crystallization duration by result. Target p95 <= 180s; zero-loss is a correctness assertion, not a latency one.",
		Buckets: boxCrystallizeBuckets,
	}, []string{"result"})

	boxCrystallizeStateLoss = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_crystallize_state_loss_total",
		Help: "State a crystallization failed to carry over, by kind (volume|env|attachment|address|port|process). Incremented by the post-cutover verifier diffing the carry manifest against reality. This is a metric and not merely a test assertion because one loss teaches distrust and severs the monetization ladder at step two — it is the only critical box alert.",
	}, []string{"kind"})

	boxCrystallizationsPendingAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_box_crystallizations_pending_age_seconds",
		Help: "Age of the oldest crystallization that has not reached a terminal state. 0 when none are pending. A high value means a customer's promotion is silently stuck (same shape as dada_domain_hostname_pending_age_seconds).",
	})

	// --- funnel ---

	boxFunnelEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_box_funnel_events_total",
		Help: "Box fake-door funnel events by event (page_view|demo_run|box_requested|crystallize_intent) and locale. crystallize_intent is the event that decides whether Box is a product with a ladder or a one-off utility.",
	}, []string{"event", "locale"})

	boxRepeatUse7d = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_box_repeat_use_7d_ratio",
		Help: "Share of granted boxes used in two sessions at least 24h apart within 7 days of first activation. The brief's headline metric: first use is curiosity, second use is a closed need.",
	})
)

// RecordBoxReady records one completed spawn.
//
// phases must be the orchestrator-observed, disjoint phase durations; total is
// the T0..T1 span (admission to canary exit status). Passing a guest-reported
// duration here would silently corrupt the one number the product is sold on, so
// callers take timestamps themselves — see internal/box.PhaseTimeline.
//
// A breach is attributed to the phase that consumed the most time, so the alert
// that fires already names the culprit.
func RecordBoxReady(pool, region string, total time.Duration, phases map[string]time.Duration) {
	boxReadyDuration.WithLabelValues(pool, region).Observe(total.Seconds())
	for phase, d := range phases {
		boxPhaseDuration.WithLabelValues(phase, pool).Observe(d.Seconds())
	}
	if total <= BoxReadyBudget {
		return
	}

	dominant := dominantPhase(phases)
	boxReadyBudgetBreaches.WithLabelValues(pool, dominant).Inc()
	log.Warn().
		Str("pool", pool).
		Str("region", region).
		Dur("duration", total).
		Str("dominant_phase", dominant).
		Float64("budget_s", BoxReadyBudget.Seconds()).
		Msg("slow box spawn: exceeded time-to-ready budget")
}

// dominantPhase returns the phase that consumed the most time. Ties break on
// phase name so the label is deterministic and a flapping alert cannot be caused
// by map iteration order. Returns "unknown" for an empty timeline rather than an
// empty label, so a missing timeline is visible on a dashboard instead of
// silently merging into another series.
func dominantPhase(phases map[string]time.Duration) string {
	dominant := "unknown"
	var longest time.Duration
	for phase, d := range phases {
		if d > longest || (d == longest && phase < dominant) {
			dominant, longest = phase, d
		}
	}
	return dominant
}

// RecordBoxSpawnOutcome records a spawn that did not reach ready. reason must be
// one of the documented values so the label set stays bounded.
func RecordBoxSpawnOutcome(result, pool, reason string) {
	boxSpawns.WithLabelValues(result, pool, reason).Inc()
}

// RecordBoxPoolMiss records a spawn that found no warm box.
func RecordBoxPoolMiss(image string) { boxPoolMisses.WithLabelValues(image).Inc() }

// RecordBoxClientReady records client-perceived time to ready, as measured by the
// synthetic canary or the CLI. Kept separate from RecordBoxReady because it
// includes the client's own handshake and is the number quoted publicly.
func RecordBoxClientReady(pool string, total time.Duration) {
	boxClientReadyDuration.WithLabelValues(pool).Observe(total.Seconds())
}

// RecordBoxCrystallizeStateLoss records state a crystallization failed to carry.
// Any non-zero value is a critical alert: the promise is that the same object
// continues living.
func RecordBoxCrystallizeStateLoss(kind string) {
	boxCrystallizeStateLoss.WithLabelValues(kind).Inc()
}

// RecordBoxFunnelEvent records one fake-door funnel event.
func RecordBoxFunnelEvent(event, locale string) {
	boxFunnelEvents.WithLabelValues(event, locale).Inc()
}
