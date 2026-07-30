package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTouchAppHealthAlertSeenDoesNotResetCooldown is RED-proof for the
// P1-ALERTS-IN-UI-FRESHNESS fix: an unconditional per-tick "seen" touch must
// never reset last_sent_at, or claimAppHealthAlertSlot would see a
// perpetually "just sent" row and the owner would stop getting alert emails
// entirely. This proves the first claim right after a touch still succeeds
// (email still goes out), and that the touch actually did move last_seen_at
// while leaving last_sent_at at the epoch sentinel until the claim runs.
func TestTouchAppHealthAlertSeenDoesNotResetCooldown(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-touch-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web")

	var lastSent time.Time
	var lastSeen *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_sent_at, last_seen_at FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&lastSent, &lastSeen); err != nil {
		t.Fatalf("read back touched row: %v", err)
	}
	if lastSeen == nil {
		t.Fatalf("expected last_seen_at to be set by touch, got nil")
	}
	if lastSent.After(time.Now().Add(-23 * time.Hour)) {
		t.Fatalf("touch must not stamp last_sent_at with now(), got %v", lastSent)
	}

	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", 24*time.Hour) {
		t.Fatalf("first claim after a touch-only row must still succeed (email must still be sent)")
	}

	firstSeen := *lastSeen
	time.Sleep(10 * time.Millisecond)
	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web")

	var sentAfterSecondTouch time.Time
	var seenAfterSecondTouch time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_sent_at, last_seen_at FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&sentAfterSecondTouch, &seenAfterSecondTouch); err != nil {
		t.Fatalf("read back row after second touch: %v", err)
	}
	if !seenAfterSecondTouch.After(firstSeen) {
		t.Fatalf("expected last_seen_at to advance on a later touch, got %v (was %v)", seenAfterSecondTouch, firstSeen)
	}

	if claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", 24*time.Hour) {
		t.Fatalf("second claim inside the 24h cooldown must still be rejected after a touch")
	}
}

// TestLoadAppAlertsFreshnessWindow is RED-proof for the same fix from the
// read side: a health row whose last_seen_at is 2h stale (long past the 15m
// fresh window, appHealthAlertFreshWindow) must not surface as a current
// alert — that is exactly the "app fixed 10 minutes ago, console still red
// a day later" bug the owner flagged. A row touched moments ago must show.
func TestLoadAppAlertsFreshnessWindow(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-fresh-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO app_health_alerts (namespace, app_name, last_sent_at, last_seen_at, reason, detail)
		 VALUES ($1, 'stale-app', now() - interval '2 hours', now() - interval '2 hours', 'CrashLoopBackOff', 'pod/stale-app')`,
		ns); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_health_alerts (namespace, app_name, last_sent_at, last_seen_at, reason, detail)
		 VALUES ($1, 'live-app', now() - interval '2 hours', now(), 'OOMKilled', 'pod/live-app')`,
		ns); err != nil {
		t.Fatalf("seed fresh row: %v", err)
	}

	h := &Handler{pool: pool}
	byApp, err := h.loadAppAlerts(ctx, ns)
	if err != nil {
		t.Fatalf("loadAppAlerts: %v", err)
	}

	if _, ok := byApp["stale-app"]; ok {
		t.Fatalf("stale-app's last_seen_at is 2h old (outside %s fresh window), must not surface as a current alert, got %+v", appHealthAlertFreshWindow, byApp["stale-app"])
	}
	if alerts, ok := byApp["live-app"]; !ok || len(alerts) != 1 || alerts[0].Reason != "OOMKilled" {
		t.Fatalf("live-app was touched just now, must surface as a current alert, got %+v (ok=%v)", byApp["live-app"], ok)
	}
}
