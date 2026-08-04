package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
)

// backfillTestConfig is the minimum config the backfill reads.
func backfillTestConfig(days int) *config.Config {
	return &config.Config{AppUsageBackfillDays: days, AppUsageBackfillTenant: "opencost"}
}

// TestBackfillExprsAvoidMetricsMimirDoesNotHave is the test the whole
// appMetricsSource split exists for. The long-retention store only holds the
// series Prometheus remote_writes into it, and kube_pod_status_phase is not one
// of them: a backfill built on the live meter's expressions would return nothing
// at all, and three weeks of history would silently reconstruct as "consumed
// nothing" -- the exact false answer the ledger was built to stop giving.
func TestBackfillExprsAvoidMetricsMimirDoesNotHave(t *testing.T) {
	h := &Handler{cfg: backfillTestConfig(21)}
	exprs := appMeterExprs([]string{"tenant-a"}, h.backfillMetricsSource().runningFilter)

	for i, expr := range exprs {
		if strings.Contains(expr, "kube_pod_status_phase") {
			t.Fatalf("expr %d uses a metric the long-retention store does not carry: %s", i, expr)
		}
	}
	running := exprs[2]
	if !strings.Contains(running, "kube_pod_container_status_running") {
		t.Fatalf("backfill must still refuse to bill a pod that was not running: %s", running)
	}
	if !strings.Contains(running, "max by (namespace, pod)") {
		t.Fatalf("per-container running signal must collapse to the pod, or a two-container pod doubles: %s", running)
	}
}

// TestLiveExprsKeepThePhaseFilter guards the other half of the split: the live
// meter reads a store that does carry phase, and phase is the stricter
// statement, so nothing about the backfill may loosen it.
func TestLiveExprsKeepThePhaseFilter(t *testing.T) {
	exprs := appMeterExprs([]string{"tenant-a"}, podPhaseRunning)
	if !strings.Contains(exprs[0], `kube_pod_status_phase`) || !strings.Contains(exprs[0], `phase="Running"`) {
		t.Fatalf("live meter lost its phase filter: %s", exprs[0])
	}
}

// TestBackfillNeverOverwritesAMeasurement pins the direction of the asymmetry.
// A reconstruction priced with today's numbers must not replace an hour that was
// actually measured at the time, or a settled bill quietly becomes an estimate.
func TestBackfillNeverOverwritesAMeasurement(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool}
	key := appUsageKey{namespace: k8sNS, app: "web"}
	target := appMeterTarget{envID: envID, projectID: projectID, orgID: orgID}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)

	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 0.25, 0.5, 0, 1, nil, nil, 1.5, appUsageSourceMeter) {
		t.Fatal("seed measured row failed")
	}
	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 9, 9, 0, 9, nil, nil, 99, appUsageSourceBackfill) {
		t.Fatal("backfill write failed")
	}

	var vcpu, cost float64
	var source string
	if err := pool.QueryRow(ctx,
		`SELECT vcpu::float8, cost_rub::float8, source FROM app_usage
		  WHERE environment_id = $1 AND app_name = $2 AND hour_start = $3 AND kind = $4`,
		envID, "web", hour, appUsageKindPod).Scan(&vcpu, &cost, &source); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if source != appUsageSourceMeter {
		t.Fatalf("measured row was relabelled as %q", source)
	}
	if vcpu != 0.25 || cost != 1.5 {
		t.Fatalf("measured hour was rewritten by the backfill: vcpu=%v cost=%v", vcpu, cost)
	}
}

// TestMeasurementOverwritesABackfill is the same rule read the other way: when
// the meter does get to an hour the backfill guessed at, the measurement wins
// and the row stops claiming to be a reconstruction.
func TestMeasurementOverwritesABackfill(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool}
	key := appUsageKey{namespace: k8sNS, app: "web"}
	target := appMeterTarget{envID: envID, projectID: projectID, orgID: orgID}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-4 * time.Hour)

	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 9, 9, 0, 9, nil, nil, 99, appUsageSourceBackfill) {
		t.Fatal("backfill write failed")
	}
	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 0.25, 0.5, 0, 1, nil, nil, 1.5, appUsageSourceMeter) {
		t.Fatal("meter write failed")
	}

	var vcpu float64
	var source string
	if err := pool.QueryRow(ctx,
		`SELECT vcpu::float8, source FROM app_usage
		  WHERE environment_id = $1 AND app_name = $2 AND hour_start = $3 AND kind = $4`,
		envID, "web", hour, appUsageKindPod).Scan(&vcpu, &source); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if source != appUsageSourceMeter || vcpu != 0.25 {
		t.Fatalf("measurement did not win over reconstruction: source=%q vcpu=%v", source, vcpu)
	}
}

// TestBackfillLeavesTheMetersHoursAlone proves the two passes do not fight over
// the recent tail. The live meter reaches appMeterMaxLagHours back on every
// tick; a backfill walking into that window would race it for hours it is about
// to measure properly.
func TestBackfillLeavesTheMetersHoursAlone(t *testing.T) {
	if appBackfillSkipRecentHours <= appMeterMaxLagHours {
		t.Fatalf("backfill starts inside the meter's own lag window: skip=%d lag=%d",
			appBackfillSkipRecentHours, appMeterMaxLagHours)
	}
}

// TestBackfillDisabledWithoutAStore proves the pass refuses to start rather than
// filling the ledger with nothing when no long-retention store is configured.
// An empty reconstruction is indistinguishable from "consumed nothing" once it
// is written, so not writing it is the only safe degradation.
func TestBackfillDisabledWithoutAStore(t *testing.T) {
	h := &Handler{cfg: backfillTestConfig(21)}
	if src := h.backfillMetricsSource(); src.client != nil {
		t.Fatal("backfill claims a metrics store it does not have")
	}
}
