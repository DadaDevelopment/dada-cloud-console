package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRecordProbeResult_NeverServedHTTP_RaisesNoAlert pins the gate that stops
// the URL watcher from red-banneriing apps that publish no HTTP port.
//
// Before this gate, prod carried nine app_url_alerts rows and four of them were
// healthy workers: telemost-bot at 5864 consecutive failures, an MTProto proxy,
// and both copies of a user's long-poll telegram bot, which showed him a broken
// app for days. None of them ever answered HTTP, because none of them was ever
// a web app -- the port they were probed on came from defaultPortForFramework,
// not from the user.
//
// The test drives recordProbeResult against a real Postgres rather than the
// pure urlProbeState state machine on purpose: the whole gate lives in SQL
// (an EXISTS against app_url_http_seen and a DELETE of the stale row), so a
// unit test of the counter would pass with the gate deleted.
func TestRecordProbeResult_NeverServedHTTP_RaisesNoAlert(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping URL-watcher integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	ns := "url-gate-" + uuid.NewString()[:8]
	w := &appURLWatcher{h: &Handler{pool: pool}}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM app_url_alerts WHERE namespace = $1`, ns)
		_, _ = pool.Exec(ctx, `DELETE FROM app_url_http_seen WHERE namespace = $1`, ns)
	})

	alertCount := func(app string) int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM app_url_alerts WHERE namespace = $1 AND app_name = $2`,
			ns, app).Scan(&n); err != nil {
			t.Fatalf("count alerts for %s: %v", app, err)
		}
		return n
	}

	for i := 0; i < appURLAlertFailureThreshold+2; i++ {
		w.recordProbeResult(ctx, ns, "long-poll-bot", false, urlProbeReasonNoListener, "connection refused")
	}
	if n := alertCount("long-poll-bot"); n != 0 {
		t.Fatalf("app that never served HTTP has %d alert rows after %d failing probes, want 0",
			n, appURLAlertFailureThreshold+2)
	}

	w.recordProbeResult(ctx, ns, "web-app", true, "", "")
	for i := 0; i < appURLAlertFailureThreshold; i++ {
		w.recordProbeResult(ctx, ns, "web-app", false, urlProbeReasonNoListener, "connection refused")
	}
	var failures int
	if err := pool.QueryRow(ctx,
		`SELECT consecutive_failures FROM app_url_alerts WHERE namespace = $1 AND app_name = 'web-app'`,
		ns).Scan(&failures); err != nil {
		t.Fatalf("an app that once served HTTP must still alert when it stops: %v", err)
	}
	if failures != appURLAlertFailureThreshold {
		t.Fatalf("web-app consecutive_failures = %d, want %d", failures, appURLAlertFailureThreshold)
	}
}

// TestRecordProbeResult_ClearsStaleAlertForNeverHTTPApp covers the migration
// path for the rows already in production: the four false banners must clear
// themselves on the first failing tick after this ships, without a manual
// sweep of the table.
func TestRecordProbeResult_ClearsStaleAlertForNeverHTTPApp(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping URL-watcher integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	ns := "url-stale-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM app_url_alerts WHERE namespace = $1`, ns)
		_, _ = pool.Exec(ctx, `DELETE FROM app_url_http_seen WHERE namespace = $1`, ns)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO app_url_alerts (namespace, app_name, reason, detail, consecutive_failures, last_seen_at, updated_at)
		 VALUES ($1, 'telemost-bot', $2, 'i/o timeout', 5864, now(), now())`,
		ns, urlProbeReasonNoListener); err != nil {
		t.Fatalf("seed stale alert: %v", err)
	}

	w := &appURLWatcher{h: &Handler{pool: pool}}
	w.recordProbeResult(ctx, ns, "telemost-bot", false, urlProbeReasonNoListener, "i/o timeout")

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM app_url_alerts WHERE namespace = $1 AND app_name = 'telemost-bot'`,
		ns).Scan(&n); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if n != 0 {
		t.Fatalf("stale alert for an app that never served HTTP survived the tick (%d rows)", n)
	}
}
