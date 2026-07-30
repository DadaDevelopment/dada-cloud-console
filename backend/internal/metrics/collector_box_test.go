package metrics

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gaugeValue reads one gauge's current value. Hand-rolled rather than pulled from
// prometheus/testutil so the test needs no new module dependency; a Gauge is a
// prometheus.Metric, so Write is all it takes.
func gaugeValue(t *testing.T, g prometheus.Metric) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	if m.GetGauge() == nil {
		t.Fatal("metric is not a gauge")
	}
	return m.GetGauge().GetValue()
}

// testCollectorPool connects to the ephemeral integration database, skipping when
// TEST_DATABASE_URL is unset so `go test` stays green offline. Same idiom as
// internal/api's testOptimisticPool.
func testCollectorPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping box collector integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedCollectorBoxes creates a throwaway project with one box per given status and
// returns nothing: the test reads the gauges, not the rows.
func seedCollectorBoxes(t *testing.T, pool *pgxpool.Pool, statuses ...string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"box-collector-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// projects cascades to environments, environments cascades to boxes.
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	for i, status := range statuses {
		name := "cb-" + suffix + "-" + string(rune('a'+i))
		var envID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO environments (project_id, name, namespace, type, runtime)
			 VALUES ($1, $2, $3, 'dev', 'box') RETURNING id`,
			projectID, name, name+"-ns",
		).Scan(&envID); err != nil {
			t.Fatalf("seed environment: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO boxes (project_id, environment_id, name, image, profile, status, updated_at)
			 VALUES ($1, $2, $3, 'warm-v1', 'box-standard', $4, now())`,
			projectID, envID, name, status,
		); err != nil {
			t.Fatalf("seed box (%s): %v", status, err)
		}
	}
}

// TestCollectBoxesGroupsByPhaseAndExcludesTombstones is the gauge's contract: one
// series per live phase, Deleted rows absent. The phase label is a lowercased
// boxes.status, which is why migration 061, models.BoxStatus and the gauge's Help
// all carry the same vocabulary — a rename in one of them without the others shows
// up here as a missing series.
func TestCollectBoxesGroupsByPhaseAndExcludesTombstones(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	// A pre-existing fleet is possible in a shared test database, so assert on
	// deltas rather than absolutes.
	collectBoxes(ctx, pool)
	beforeReady := gaugeValue(t, boxes.WithLabelValues("ready"))
	beforeSleeping := gaugeValue(t, boxes.WithLabelValues("sleeping"))
	beforeDeleted := gaugeValue(t, boxes.WithLabelValues("deleted"))

	seedCollectorBoxes(t, pool, "Ready", "Ready", "Sleeping", "Deleted")
	collectBoxes(ctx, pool)

	if got := gaugeValue(t, boxes.WithLabelValues("ready")) - beforeReady; got != 2 {
		t.Errorf("dada_boxes{phase=\"ready\"} moved by %v, want 2", got)
	}
	if got := gaugeValue(t, boxes.WithLabelValues("sleeping")) - beforeSleeping; got != 1 {
		t.Errorf("dada_boxes{phase=\"sleeping\"} moved by %v, want 1", got)
	}
	if got := gaugeValue(t, boxes.WithLabelValues("deleted")) - beforeDeleted; got != 0 {
		t.Errorf("dada_boxes{phase=\"deleted\"} moved by %v, want 0 — tombstones hold no capacity "+
			"and counting them would make the gauge grow forever", got)
	}
}

// TestCollectBoxesCountsRecentFailures mirrors dada_builds_failed_recent: a
// one-hour window, so the gauge clears itself as failures age out and therefore
// tracks live breakage rather than history. A box that failed yesterday must not
// keep an alert lit today.
func TestCollectBoxesCountsRecentFailures(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectBoxes(ctx, pool)
	before := gaugeValue(t, boxFailedRecent)

	seedCollectorBoxes(t, pool, "Failed")
	collectBoxes(ctx, pool)
	if got := gaugeValue(t, boxFailedRecent) - before; got != 1 {
		t.Errorf("dada_box_failed_recent moved by %v, want 1", got)
	}

	// Age the failure out of the window.
	if _, err := pool.Exec(ctx,
		`UPDATE boxes SET updated_at = now() - interval '2 hours' WHERE status = 'Failed'`); err != nil {
		t.Fatalf("age failures: %v", err)
	}
	collectBoxes(ctx, pool)
	if got := gaugeValue(t, boxFailedRecent); got != 0 {
		t.Errorf("dada_box_failed_recent = %v after aging every failure past the hour window, want 0", got)
	}
}

// TestCollectBoxesPublishesZeroForTablelessGauges: the two gauges whose tables do
// not exist yet are PUBLISHED at 0 rather than left unset, because alert rules
// already watch them and a gauge that only starts reporting later is
// silent-by-absence in between — "the alert never fired" would be
// indistinguishable from "nothing is wrong".
func TestCollectBoxesPublishesZeroForTablelessGauges(t *testing.T) {
	pool := testCollectorPool(t)
	collectBoxes(context.Background(), pool)

	if got := gaugeValue(t, boxSpendCapMaxRatio); got != 0 {
		t.Errorf("dada_box_spend_cap_max_ratio = %v, want 0 until the usage ledger exists", got)
	}
	if got := gaugeValue(t, boxCrystallizationsPendingAge); got != 0 {
		t.Errorf("dada_box_crystallizations_pending_age_seconds = %v, want 0 until box_crystallizations exists", got)
	}
}

// TestCollectBoxesSurvivesAMissingTable proves the refresh is best-effort in the
// same way as the rest of collect(): a query that cannot run must not take the
// whole gauge refresh (or the /metrics endpoint) down with it, and must not publish
// a zero that looks like "the fleet emptied out".
func TestCollectBoxesSurvivesAMissingTable(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	seedCollectorBoxes(t, pool, "Ready")
	collectBoxes(ctx, pool)
	withData := gaugeValue(t, boxes.WithLabelValues("ready"))
	if withData < 1 {
		t.Fatalf("dada_boxes{phase=\"ready\"} = %v before the failure injection, want >= 1", withData)
	}

	// A cancelled context is the cheapest stand-in for "the query failed".
	dead, cancel := context.WithCancel(ctx)
	cancel()
	collectBoxes(dead, pool) // must not panic

	if got := gaugeValue(t, boxes.WithLabelValues("ready")); got != withData {
		t.Errorf("dada_boxes{phase=\"ready\"} = %v after a failed refresh, want the previous %v kept: "+
			"publishing a zero on a transient DB error looks exactly like the fleet emptying out "+
			"and would clear an alert that should still be firing", got, withData)
	}
}
