package api

import (
	"context"
	"strings"
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

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", "", "", "")

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
	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", "", "", "")

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

// TestTouchAppHealthAlertSeenPersistsCauseForNonEmailableReason proves the
// cause is written independently of emailableReason: maybeNotify calls
// touchAppHealthAlertSeen before it ever checks emailableReason, so a plain
// "Error" exit (which never emails) must still land its cause in the row,
// the console being the only place that class of crash shows up at all.
func TestTouchAppHealthAlertSeenPersistsCauseForNonEmailableReason(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-cause-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "Error", "pod-1/worker exit=1",
		"Судя по логам, это ошибка в коде приложения (Python).", "ModuleNotFoundError: No module named 'flask'", "app_code")

	var cause, causeLine string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(cause, ''), COALESCE(cause_line, '') FROM app_health_alerts WHERE namespace = $1 AND app_name = 'worker'`,
		ns).Scan(&cause, &causeLine); err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if !strings.Contains(cause, "Python") {
		t.Fatalf("expected cause to be persisted for a non-emailable reason, got %q", cause)
	}
	if causeLine != "ModuleNotFoundError: No module named 'flask'" {
		t.Fatalf("expected cause_line to be persisted, got %q", causeLine)
	}
}

// TestTouchAppHealthAlertSeenPreservesCauseWhenNotRefreshed proves the "skip
// this tick" contract from maybeCauseRefresh: passing "" for cause/causeLine
// on a later touch (the common case once a cause is already known and the
// reason has not changed) must not erase what an earlier tick already wrote.
func TestTouchAppHealthAlertSeenPreservesCauseWhenNotRefreshed(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-cause-keep-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "CrashLoopBackOff", "pod-1/worker",
		"Судя по логам, это похоже на ошибку в коде приложения.", "panic: boom", "app_code")
	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "CrashLoopBackOff", "pod-1/worker", "", "", "")

	var cause, causeLine string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(cause, ''), COALESCE(cause_line, '') FROM app_health_alerts WHERE namespace = $1 AND app_name = 'worker'`,
		ns).Scan(&cause, &causeLine); err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if cause == "" || causeLine == "" {
		t.Fatalf("expected the earlier cause/cause_line to survive an empty touch, got cause=%q cause_line=%q", cause, causeLine)
	}
}

// TestCurrentAlertCauseStateDrivesRefreshDecision proves the two branches
// maybeCauseRefresh relies on: no row yet must report hasCause=false (a
// first-ever detection always fetches the log), and a row with a cause
// already recorded for the same reason must report hasCause=true so the
// caller can skip the kube-API read.
func TestCurrentAlertCauseStateDrivesRefreshDecision(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-cause-state-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	if _, _, err := currentAlertCauseState(ctx, pool, ns, "worker"); err == nil {
		t.Fatalf("expected an error (no row yet) before the first touch")
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "OOMKilled", "pod-1/worker", "hint", "evidence line", "app_code")

	reason, hasCause, err := currentAlertCauseState(ctx, pool, ns, "worker")
	if err != nil {
		t.Fatalf("currentAlertCauseState: %v", err)
	}
	if reason != "OOMKilled" {
		t.Fatalf("expected reason=OOMKilled, got %q", reason)
	}
	if !hasCause {
		t.Fatalf("expected hasCause=true after a touch with a non-empty cause")
	}
}

// TestCurrentAlertCauseStateForcesRefreshOnPoisonedCauseLine is RED-proof for
// P1-CAUSELINE-HEADER. The older extractor could store the bare Python
// traceback header as the crash cause, and maybeCauseRefresh skips the log
// read entirely while a cause is on record — so without this, a row poisoned
// before the fix would keep showing "Traceback (most recent call last):" in
// the console banner for as long as the app kept crashlooping with the same
// reason, and the fixed extractor would never get to run on it.
func TestCurrentAlertCauseStateForcesRefreshOnPoisonedCauseLine(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-cause-poison-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "CrashLoopBackOff", "pod-1/worker",
		"ошибка в коде приложения (Python)", "Traceback (most recent call last):", "app_code")

	reason, hasCause, err := currentAlertCauseState(ctx, pool, ns, "worker")
	if err != nil {
		t.Fatalf("currentAlertCauseState: %v", err)
	}
	if reason != "CrashLoopBackOff" {
		t.Fatalf("expected reason=CrashLoopBackOff, got %q", reason)
	}
	if hasCause {
		t.Fatalf("a stored bare traceback header must not count as a known cause")
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "worker", "CrashLoopBackOff", "pod-1/worker",
		"ошибка в коде приложения (Python)", "RuntimeError: no objects found under 's3://models/buffalo_l'", "app_code")

	if _, hasCause, err = currentAlertCauseState(ctx, pool, ns, "worker"); err != nil {
		t.Fatalf("currentAlertCauseState after repair: %v", err)
	}
	if !hasCause {
		t.Fatalf("expected hasCause=true once a real exception line replaced the header")
	}
}
