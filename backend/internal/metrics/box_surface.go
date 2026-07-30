package metrics

import "github.com/prometheus/client_golang/prometheus"

// The declared Box metric surface.
//
// This table exists so a rename or a label change is a reviewable diff instead of
// a silently broken dashboard or a silently dead alert. Two tests keep it honest
// in opposite directions:
//
//   - TestBoxMetricSurfaceGolden compares this table to a golden file, so
//     changing the table without updating the golden fails the PR.
//   - TestBoxMetricSpecsMatchCollectors compares this table to what the
//     collectors actually registered, so changing a promauto declaration without
//     updating the table fails the PR.
//
// Neither test alone is enough: the first catches intent drifting from the record,
// the second catches the record drifting from reality.

// boxMetricSpec is one declared metric: its name, Prometheus type, and variable
// label names in declaration order.
type boxMetricSpec struct {
	Name      string
	Type      string
	Labels    []string
	collector prometheus.Collector
}

// boxMetricSpecs is the full Box metric surface, sorted by name. Keep it sorted:
// the golden file is sorted too, so an insertion produces a one-line diff instead
// of a reshuffle.
var boxMetricSpecs = []boxMetricSpec{
	{"dada_box_active_minutes_total", "counter", []string{"plan"}, boxActiveMinutes},
	{"dada_box_attach_duration_seconds", "histogram", []string{"resource"}, boxAttachDuration},
	{"dada_box_attach_total", "counter", []string{"resource", "result"}, boxAttachTotal},
	{"dada_box_client_ready_duration_seconds", "histogram", []string{"pool"}, boxClientReadyDuration},
	{"dada_box_crystallizations_pending_age_seconds", "gauge", nil, boxCrystallizationsPendingAge},
	{"dada_box_crystallizations_total", "counter", []string{"result", "stage"}, boxCrystallizations},
	{"dada_box_crystallize_duration_seconds", "histogram", []string{"result"}, boxCrystallizeDuration},
	{"dada_box_crystallize_state_loss_total", "counter", []string{"kind"}, boxCrystallizeStateLoss},
	{"dada_box_destroys_total", "counter", []string{"cause"}, boxDestroys},
	{"dada_box_expose_duration_seconds", "histogram", []string{"cert"}, boxExposeDuration},
	{"dada_box_failed_recent", "gauge", nil, boxFailedRecent},
	{"dada_box_funnel_events_total", "counter", []string{"event", "locale"}, boxFunnelEvents},
	{"dada_box_idle_minutes_total", "counter", []string{"plan"}, boxIdleMinutes},
	{"dada_box_meter_errors_total", "counter", nil, boxMeterErrors},
	{"dada_box_metered_minutes_lag_seconds", "gauge", nil, boxMeteredMinutesLag},
	{"dada_box_phase_duration_seconds", "histogram", []string{"phase", "pool"}, boxPhaseDuration},
	{"dada_box_pool_available", "gauge", []string{"image", "region"}, boxPoolAvailable},
	{"dada_box_pool_misses_total", "counter", []string{"image"}, boxPoolMisses},
	{"dada_box_pool_refill_duration_seconds", "histogram", []string{"image"}, boxPoolRefillDuration},
	{"dada_box_pool_target", "gauge", []string{"image", "region"}, boxPoolTarget},
	{"dada_box_ready_budget_breaches_total", "counter", []string{"pool", "phase"}, boxReadyBudgetBreaches},
	{"dada_box_ready_duration_seconds", "histogram", []string{"pool", "region"}, boxReadyDuration},
	{"dada_box_repeat_use_7d_ratio", "gauge", nil, boxRepeatUse7d},
	{"dada_box_spawns_total", "counter", []string{"result", "pool", "reason"}, boxSpawns},
	{"dada_box_spend_cap_hits_total", "counter", []string{"action"}, boxSpendCapHits},
	{"dada_box_spend_cap_max_ratio", "gauge", nil, boxSpendCapMaxRatio},
	{"dada_boxes", "gauge", []string{"phase"}, boxes},
}
