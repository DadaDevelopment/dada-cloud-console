package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestClaimAppHealthAlertSlot_LateCauseOpensExactlyOneCatchUp replays the
// live incident that motivated the catch-up slot: bruzas.85's sevarateambot
// crashlooped, the alert email went out five minutes later while the cause
// columns were still empty, and the diagnosis (missing_env_var /
// TELEGRAM_API_TOKEN) only arrived in the row 18 hours later. Under the plain
// 24h cooldown the owner could not be told the reason until the evening.
//
// Pole one of the proof: an app that is still failing and whose cause showed
// up after the last send gets exactly one more slot — and only one, so the
// catch-up cannot degenerate into an every-tick mail loop on a crashloop that
// lasts days.
func TestClaimAppHealthAlertSlot_LateCauseOpensExactlyOneCatchUp(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-catchup-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot", "", "", "", false)
	if !claimAppHealthAlertSlot(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot", 24*time.Hour) {
		t.Fatalf("first claim on a fresh row must succeed")
	}
	if claimAppHealthAlertSlot(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot", 24*time.Hour) {
		t.Fatalf("a second claim inside the window with no cause on record must be refused")
	}

	touchAppHealthAlertSeen(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot",
		"missing env var", "TELEGRAM_API_TOKEN", "missing_env_var", true)

	if !claimAppHealthAlertSlot(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot", 24*time.Hour) {
		t.Fatalf("a cause discovered after a causeless send must open one catch-up slot")
	}
	if claimAppHealthAlertSlot(ctx, pool, ns, "bot", "CrashLoopBackOff", "pod-1/bot", 24*time.Hour) {
		t.Fatalf("the catch-up slot must open exactly once, not once per tick")
	}

	if got := storedAlertCause(ctx, pool, ns, "bot"); got != "missing env var" {
		t.Fatalf("catch-up email would carry cause %q, want the stored diagnosis", got)
	}
}

// TestClaimAppHealthAlertSlot_UnchangedCauseKeepsWindow is pole two: the
// window still holds for everyone else. An app whose diagnosis was already
// known when its email went out never gets a second one inside 24h, so the
// catch-up branch cannot be read as "any app with a cause mails again".
func TestClaimAppHealthAlertSlot_UnchangedCauseKeepsWindow(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-nocatchup-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web",
		"module not found", "Error: Cannot find module 'x'", "app_code", true)
	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", 24*time.Hour) {
		t.Fatalf("first claim on a fresh row must succeed")
	}

	for tick := 0; tick < 3; tick++ {
		touchAppHealthAlertSeen(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", "", "", "", false)
		if claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod-1/web", 24*time.Hour) {
			t.Fatalf("tick %d: an app whose cause was already known at send time must stay inside its 24h window", tick)
		}
	}
}
