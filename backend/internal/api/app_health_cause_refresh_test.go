package api

import "testing"

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
