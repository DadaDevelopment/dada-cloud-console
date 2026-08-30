package api

import (
	"testing"
	"time"
)

// TestCauseRefreshOutcomePreservesCauseOnEmptyLog is RED-proof for the live
// bug observed on envprobe0816 (2026-08-16): a flapping pod's reason
// bounces between CrashLoopBackOff and Error while the underlying crash (a
// missing env var) never changes. maybeCauseRefresh's cheap-skip only fires
// when the reason is unchanged, so a reason flap forces a real tailLog call,
// and if that call lands right after a restart -- before the container has
// written a previous-run log -- tailLog returns "" (see its doc comment:
// every kube GetLogs failure collapses to ""). Before this fix,
// causeRefreshOutcome (formerly the tail of maybeCauseRefresh) reported
// refreshed=true unconditionally once the log HAD been read, so
// touchAppHealthAlertSeen NULLed out a diagnosis that was still correct --
// "we saw nothing this tick" got recorded as "there is no cause anymore".
// On the old code this test fails because refreshed comes back true with an
// empty cause; on the fixed code the empty-log/empty-classification case
// reports refreshed=false so the caller passes "" through to
// touchAppHealthAlertSeen with refreshed=false, which COALESCEs and keeps
// the stored diagnosis (see TestTouchAppHealthAlertSeenPreservesCauseWhenNotRefreshed).
func TestCauseRefreshOutcomePreservesCauseOnEmptyLog(t *testing.T) {
	_, _, _, _, refreshed := causeRefreshOutcome("", "", "", "")
	if refreshed {
		t.Fatalf("empty log + no classification must report refreshed=false so the stored cause survives, got refreshed=true")
	}
}

// TestCauseRefreshOutcomePreservesCauseOnUnmatchedLog is RED-proof for the
// second live sighting of the same class: gulyaev-ai-core (2026-08-19)
// crashlooped 13 times on one unchanged ModuleNotFoundError, the platform had
// already classified it as app_code and mailed the owner, yet the live
// cause_kind column came back empty -- so frontend/lib/app-alerts.ts
// crashCauseKey() returned null and the console banner showed "your app is
// crashing" with no verdict and no lever.
//
// The mechanism the earlier fix missed: a non-empty log that matches no
// signature is NOT negative evidence. tailLog reads only the last
// appHealthLogTailLines lines of a single container instance, so the
// diagnostic line drifts in and out of that window across restarts of an
// identical crash, and a flapping reason defeats maybeCauseRefresh's
// cheap-skip so the read happens every tick. Under the old rule each unlucky
// window NULLed a correct diagnosis.
//
// On the old code this fails: refreshed comes back true with an empty
// causeKind, which is exactly what touchAppHealthAlertSeen turns into
// cause_kind = NULL.
func TestCauseRefreshOutcomePreservesCauseOnUnmatchedLog(t *testing.T) {
	_, _, _, causeKind, refreshed := causeRefreshOutcome("some unrelated stdout line\n", "", "", "")
	if refreshed {
		t.Fatalf("a non-empty log that classified nothing must report refreshed=false so the stored cause survives, got refreshed=true (causeKind=%q)", causeKind)
	}
}

// TestCauseRefreshOutcomeClearsOnlyOnPositiveReclassification is the other
// pole: a genuinely different failure DOES replace the old diagnosis, so the
// guard above cannot freeze a stale cause forever once the app starts failing
// for a new, recognizable reason.
func TestCauseRefreshOutcomeClearsOnlyOnPositiveReclassification(t *testing.T) {
	logExcerpt, cause, _, causeKind, refreshed := causeRefreshOutcome("Error: connect ECONNREFUSED\n", "cannot reach its database", "Error: connect ECONNREFUSED", "bad_connection_string")
	if !refreshed {
		t.Fatalf("a positive new classification must report refreshed=true, got refreshed=false")
	}
	if causeKind != "bad_connection_string" || cause == "" || logExcerpt == "" {
		t.Fatalf("expected the new verdict to pass through unchanged, got cause=%q causeKind=%q logExcerpt=%q", cause, causeKind, logExcerpt)
	}
}

// TestCauseRefreshOutcomeAlwaysRefreshesOnKubeAuthoritativeReason proves the
// escape hatch: OOMKilled/ImagePullBackOff/ErrImagePull are classified from
// the kube-reported reason alone (see ClassifyCrashCauseWithReason), so
// logExcerpt is legitimately always "" for them -- that must never be
// mistaken for "no evidence this tick" and must not suppress a genuine
// reason-change overwrite.
func TestCauseRefreshOutcomeAlwaysRefreshesOnKubeAuthoritativeReason(t *testing.T) {
	logExcerpt, cause, causeLine, causeKind, refreshed := causeRefreshOutcome("", "exceeded its memory limit", "", "resource_limit")
	if !refreshed {
		t.Fatalf("a kube-authoritative causeKind with empty log must still report refreshed=true, got refreshed=false")
	}
	if cause == "" || causeKind == "" {
		t.Fatalf("expected cause/causeKind to pass through unchanged, got cause=%q causeKind=%q logExcerpt=%q causeLine=%q", cause, causeKind, logExcerpt, causeLine)
	}
}

// TestEscalatedCooldownLadderMatchesSQL pins the Go ladder
// (appHealthAlertEscalationSteps, consumed by escalatedCooldown) to the same
// cutoffs the claim's WHERE clause inlines as interval literals ('3 days' ->
// +2 days of cooldown, '14 days' -> +6 days). The SQL cannot be expressed
// from the slice without building it at init time, so the two must simply
// agree; this test is the agreement. If you change one side, change both.
func TestEscalatedCooldownLadderMatchesSQL(t *testing.T) {
	if len(appHealthAlertEscalationSteps) != 3 {
		t.Fatalf("the SQL WHERE clause hardcodes two cutoffs (3d, 14d); expected exactly 3 steps, got %d", len(appHealthAlertEscalationSteps))
	}
	wantAfter := []time.Duration{0, 3 * 24 * time.Hour, 14 * 24 * time.Hour}
	wantCooldown := []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour}
	for i, s := range appHealthAlertEscalationSteps {
		if s.after != wantAfter[i] || s.cooldown != wantCooldown[i] {
			t.Fatalf("step %d = (after %v, cooldown %v), SQL inlines (after %v, cooldown %v)", i, s.after, s.cooldown, wantAfter[i], wantCooldown[i])
		}
	}
}

// TestEscalatedCooldown walks the ladder end to end: day 0 (fresh incident)
// and day 2 alert daily; day 3 through day 13 alert every three days; day 14
// and beyond alert weekly. A first_detected_at in the future (clock skew
// between replicas) must not produce a negative age, which would otherwise
// pick no step at all.
func TestEscalatedCooldown(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want time.Duration
	}{
		{age: 0, want: 24 * time.Hour},
		{age: 2 * 24 * time.Hour, want: 24 * time.Hour},
		{age: 3 * 24 * time.Hour, want: 3 * 24 * time.Hour},
		{age: 13 * 24 * time.Hour, want: 3 * 24 * time.Hour},
		{age: 14 * 24 * time.Hour, want: 7 * 24 * time.Hour},
		{age: 90 * 24 * time.Hour, want: 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		first := now.Add(-c.age)
		if got := escalatedCooldown(first, now); got != c.want {
			t.Fatalf("age %v: got cooldown %v, want %v", c.age, got, c.want)
		}
	}
	if got := escalatedCooldown(now.Add(time.Hour), now); got != 24*time.Hour {
		t.Fatalf("future first_detected_at (clock skew): got %v, want the young cadence %v", got, 24*time.Hour)
	}
}
