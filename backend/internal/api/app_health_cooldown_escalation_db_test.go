package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestClaimAppHealthAlertSlotCooldownEscalation pins the escalating cooldown
// to first_detected_at (migration 146): a young incident claims again after
// the flat 24h cadence, the same incident backdated past the three-day
// cutoff is held to the 72h cadence even though last_sent_at is 25h old, and
// a reason change resets the incident so the young cadence applies again.
// The claim's WHERE measures age in SQL from first_detected_at; backdating
// both columns is exactly what a real long-lived incident looks like.
func TestClaimAppHealthAlertSlotCooldownEscalation(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("first claim for a fresh app must succeed")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET last_sent_at = now() - interval '25 hours'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate cooldown row: %v", err)
	}
	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("young incident: claim 25h after the last send must succeed")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts
		 SET last_sent_at = now() - interval '25 hours',
		     first_detected_at = now() - interval '4 days'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("age the incident past the 3-day cutoff: %v", err)
	}
	if claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("3-day-old incident is on the 72h cadence: a 25h-old send must NOT claim yet")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts
		 SET last_sent_at = now() - interval '73 hours'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate past the escalated cadence: %v", err)
	}
	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("3-day-old incident: claim 73h after the last send must succeed")
	}

	// The reset flows through touchAppHealthAlertSeen, which in maybeNotify
	// always runs BEFORE the claim: the tick that first detects the new
	// reason stamps reason + first_detected_at = now(), and only then does
	// the claim evaluate the cooldown against the already-reset age.
	touchAppHealthAlertSeen(ctx, pool, ns, "web", "OOMKilled", "pod/web", "", "", "", false)
	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET last_sent_at = now() - interval '2 hours'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate a recent send: %v", err)
	}
	if claimAppHealthAlertSlot(ctx, pool, ns, "web", "OOMKilled", "pod/web", 24*time.Hour) {
		t.Fatalf("the reset changes the CADENCE, not the last send: an email went out 2h ago, none is due yet")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET last_sent_at = now() - interval '25 hours'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate past the young cadence: %v", err)
	}
	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "OOMKilled", "pod/web", 24*time.Hour) {
		t.Fatalf("a reason change resets the incident to the young 24h cadence: claim 25h after the last send must succeed")
	}
	var firstDetected time.Time
	if err := pool.QueryRow(ctx,
		`SELECT first_detected_at FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&firstDetected); err != nil {
		t.Fatalf("read back first_detected_at: %v", err)
	}
	if age := time.Since(firstDetected); age > 5*time.Minute {
		t.Fatalf("reason change must reset first_detected_at to now, got age %v", age)
	}
}

// TestTouchAppHealthAlertSeenResetsIncidentAge pins the touch side of the
// reset: every tick writes reason, so first_detected_at must hold steady
// while the reason is unchanged and advance to now() exactly when the
// detected reason changes.
func TestTouchAppHealthAlertSeenResetsIncidentAge(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", "", "", "", false)

	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET first_detected_at = now() - interval '5 days'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate first_detected_at: %v", err)
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", "", "", "", false)
	var firstDetected time.Time
	if err := pool.QueryRow(ctx,
		`SELECT first_detected_at FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&firstDetected); err != nil {
		t.Fatalf("read back first_detected_at: %v", err)
	}
	if age := time.Since(firstDetected); age < 4*24*time.Hour {
		t.Fatalf("same reason on every tick must keep first_detected_at (age %v), not reset it", age)
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "OOMKilled", "pod/web", "", "", "", false)
	if err := pool.QueryRow(ctx,
		`SELECT first_detected_at FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&firstDetected); err != nil {
		t.Fatalf("read back first_detected_at after reason change: %v", err)
	}
	if age := time.Since(firstDetected); age > 5*time.Minute {
		t.Fatalf("reason change must reset first_detected_at to now, got age %v", age)
	}
}

// TestTouchAppHealthAlertSeenClearsStaleVerdictOnReasonChange is RED-proof
// for the sticky-verdict shape from the 2026-08-30 gateway incident: an old
// ErrImagePull episode left cause_kind=platform_registry, the app flipped to
// CrashLoopBackOff whose Java stack matches no signature, so ticks came back
// refreshed=false and the COALESCE re-adopted the registry verdict forever.
// The fix: when the row's reason changes, the stored cause triplet is stale
// evidence for the wrong failure and must be cleared outright even when this
// tick's own read produced nothing yet (the next tick's forced re-read,
// which a changed reason also triggers via maybeCauseRefresh, repopulates
// it or leaves the console honestly without a verdict).
func TestTouchAppHealthAlertSeenClearsStaleVerdictOnReasonChange(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "ErrImagePull", "pod/web",
		"Контейнер не запускался вообще: платформа не смогла скачать образ.",
		"", "platform_registry", false)

	var cause, causeKind string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(cause, ''), COALESCE(cause_kind, '') FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&cause, &causeKind); err != nil {
		t.Fatalf("read back seeded verdict: %v", err)
	}
	if causeKind != "platform_registry" || cause == "" {
		t.Fatalf("seed setup: expected the registry verdict stored, got cause=%q causeKind=%q", cause, causeKind)
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", "", "", "", false)

	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(cause, ''), COALESCE(cause_kind, '') FROM app_health_alerts WHERE namespace = $1 AND app_name = 'web'`,
		ns).Scan(&cause, &causeKind); err != nil {
		t.Fatalf("read back after reason change: %v", err)
	}
	if cause != "" || causeKind != "" {
		t.Fatalf("a reason change must clear the stale verdict for the wrong failure, got cause=%q causeKind=%q", cause, causeKind)
	}
}
