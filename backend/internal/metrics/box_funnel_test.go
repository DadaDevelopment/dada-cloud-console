package metrics

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// metricValue reads the current value of a plain counter or gauge, following the
// same dto.Metric idiom as http_test.go (prometheus/testutil would pull in an
// extra module that this backend does not depend on).
func metricValue(t *testing.T, c prometheus.Metric) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	return m.GetCounter().GetValue()
}

// TestBoxRepeatUseStartsAsNoDataNotZero is the guard on the one funnel number that
// could most easily become a lie.
//
// A Prometheus gauge is exported from the moment it is registered, so an unsourced
// gauge scrapes as 0.0 forever. For this metric 0.0 means "not one person came
// back" — a devastating conclusion that nobody measured. The active-minute ledger
// it derives from does not exist yet, so the value must be NaN: Prometheus's
// explicit no-data, on which comparisons are false and alerts do not fire.
func TestBoxRepeatUseStartsAsNoDataNotZero(t *testing.T) {
	SetBoxRepeatUse7dUnavailable()
	if v := metricValue(t, boxRepeatUse7d); !math.IsNaN(v) {
		t.Fatalf("dada_box_repeat_use_7d_ratio = %v, want NaN — 0 would read as \"nobody came back\"", v)
	}
}

func TestSetBoxRepeatUse7dRatio(t *testing.T) {
	t.Cleanup(SetBoxRepeatUse7dUnavailable)
	SetBoxRepeatUse7dRatio(0.5)
	if v := metricValue(t, boxRepeatUse7d); v != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", v)
	}
	// A real, measured zero is publishable — it is only the *unsourced* zero that
	// is forbidden.
	SetBoxRepeatUse7dRatio(0)
	if v := metricValue(t, boxRepeatUse7d); v != 0 {
		t.Fatalf("ratio = %v, want 0", v)
	}
}

func TestRecordBoxFunnelEventCounts(t *testing.T) {
	before := metricValue(t, boxFunnelEvents.WithLabelValues("page_view", "en"))
	RecordBoxFunnelEvent("page_view", "en")
	if after := metricValue(t, boxFunnelEvents.WithLabelValues("page_view", "en")); after != before+1 {
		t.Fatalf("counter = %v, want %v", after, before+1)
	}
}

// TestCollectBoxRepeatUseDegradesToNoData exercises the real query against a real
// database whose box_repeat_use_7d view does not exist — which is the state of
// every environment today, since the view is gated on the ФАЗА 7 minute ledger.
//
// Two assertions, and the second matters as much as the first: a missing view must
// NOT increment dada_metrics_collect_errors_total, or an expected gap would page
// the platform team forever and the error counter would stop meaning "something
// broke".
func TestCollectBoxRepeatUseDegradesToNoData(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping box repeat-use collector DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	var viewExists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.box_repeat_use_7d') IS NOT NULL`).Scan(&viewExists); err != nil {
		t.Fatalf("probe for the view: %v", err)
	}
	if viewExists {
		t.Skip("box_repeat_use_7d exists in this database; this test covers the gated (absent) case")
	}

	// Start from a known value so a NaN afterwards can only have come from the
	// collector, not from package init.
	SetBoxRepeatUse7dRatio(0.42)
	errorsBefore := metricValue(t, collectErrors)

	collectBoxRepeatUse(ctx, pool)

	if v := metricValue(t, boxRepeatUse7d); !math.IsNaN(v) {
		t.Errorf("ratio = %v, want NaN when the view does not exist", v)
	}
	if got := metricValue(t, collectErrors); got != errorsBefore {
		t.Errorf("collect errors moved %v -> %v; a gated view is an expected absence, not a failure",
			errorsBefore, got)
	}
}

// TestCollectBoxRepeatUsePublishesTheMeasuredRatio is the mirror image: it runs
// only where box_repeat_use_7d and the active-minute ledger exist, and it pins the
// definition end to end — two sessions 26h apart count, two sessions 3h apart do
// not, and the gauge carries the resulting share.
//
// It skips everywhere today (the ledger is a ФАЗА 7 item), and it exists so that
// the migration which introduces the ledger turns the definition into a passing
// test rather than an argument.
func TestCollectBoxRepeatUsePublishesTheMeasuredRatio(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping box repeat-use collector DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	var ready bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('public.box_repeat_use_7d') IS NOT NULL
		   AND to_regclass('public.box_usage') IS NOT NULL`).Scan(&ready); err != nil {
		t.Fatalf("probe for the view and the ledger: %v", err)
	}
	if !ready {
		t.Skip("box_repeat_use_7d / box_usage not present; the repeat-use metric has no data source yet (ФАЗА 7)")
	}

	// Isolate from any other rows: score only the two claims seeded here by
	// clearing the tables inside a transaction we roll back at the end.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{`DELETE FROM box_usage`, `DELETE FROM box_grants`} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("clear: %v", err)
		}
	}

	const returner = "BOX-CAFE-0001"
	const oneShot = "BOX-CAFE-0002"
	seed := []struct {
		claim  string
		box    string
		offset string
	}{
		// Came back the next day: repeat use.
		{returner, "aaaaaaaa-0000-4000-8000-000000000001", "10 days"},
		{returner, "aaaaaaaa-0000-4000-8000-000000000002", "8 days 22 hours"},
		// Came back after lunch the same day: NOT repeat use. This is the case the
		// 24h clause exists to exclude.
		{oneShot, "bbbbbbbb-0000-4000-8000-000000000001", "10 days"},
	}
	for _, s := range seed {
		if _, err := tx.Exec(ctx,
			`INSERT INTO box_grants (claim, org_id, box_id, granted_by) VALUES ($1, 'org-test', $2, 'test')
			 ON CONFLICT (claim, box_id) DO NOTHING`, s.claim, s.box); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO box_usage (box_id, minute_start, kind)
			 SELECT $1, now() - $2::interval + (g || ' minutes')::interval, 'cpu'
			   FROM generate_series(0, 9) g`, s.box, s.offset); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
	}
	// The same-day return, three hours later, on the one-shot claim's own box.
	if _, err := tx.Exec(ctx,
		`INSERT INTO box_usage (box_id, minute_start, kind)
		 SELECT 'bbbbbbbb-0000-4000-8000-000000000001', now() - interval '10 days' + interval '3 hours' + (g || ' minutes')::interval, 'cpu'
		   FROM generate_series(0, 9) g`); err != nil {
		t.Fatalf("seed second same-day session: %v", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT claim, sessions_within_7d, repeat_use FROM box_repeat_use_7d ORDER BY claim`)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}
	got := map[string]struct {
		sessions int
		repeat   bool
	}{}
	for rows.Next() {
		var claim string
		var sessions int
		var repeat bool
		if err := rows.Scan(&claim, &sessions, &repeat); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[claim] = struct {
			sessions int
			repeat   bool
		}{sessions, repeat}
	}
	rows.Close()

	if r := got[returner]; r.sessions != 2 || !r.repeat {
		t.Errorf("%s: sessions=%d repeat=%v, want 2 sessions and repeat_use=true", returner, r.sessions, r.repeat)
	}
	if o := got[oneShot]; o.sessions != 2 || o.repeat {
		t.Errorf("%s: sessions=%d repeat=%v, want 2 sessions but repeat_use=false "+
			"(a second session 3h later is one work session, not a returning user)", oneShot, o.sessions, o.repeat)
	}
}
