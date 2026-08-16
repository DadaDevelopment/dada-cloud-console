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

// TestCauseRefreshOutcomeStillClearsOnGenuineNoCause proves the fix did not
// break the case TestTouchAppHealthAlertSeenClearsStaleCauseWhenRefreshed
// guards at the DB layer: a log WAS read this tick (non-empty) and simply
// matched no known signature. That is real negative evidence -- the app
// crashed, we looked, nothing recognizable was there -- so refreshed must
// still come back true and let the stale cause from a previous, different
// failure get cleared.
func TestCauseRefreshOutcomeStillClearsOnGenuineNoCause(t *testing.T) {
	_, _, _, _, refreshed := causeRefreshOutcome("some unrelated stdout line\n", "", "", "")
	if !refreshed {
		t.Fatalf("a non-empty log that matched nothing must still report refreshed=true, got refreshed=false")
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
