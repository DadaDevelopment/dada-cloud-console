package api

import (
	"testing"
	"time"
)

// TestClassifyAppHealth_SettledPhasesWinRegardlessOfLiveSource pins that
// Ready/Stopped/Orphaned are settled answers, same as both admin_overview.go
// predicates treat them: they short-circuit before the live_source/staleness
// branching even matters.
func TestClassifyAppHealth_SettledPhasesWinRegardlessOfLiveSource(t *testing.T) {
	cases := []struct {
		phase string
		want  appHealthVerdict
	}{
		{"Ready", appHealthReady},
		{"Stopped", appHealthStopped},
		{"Orphaned", appHealthOrphaned},
	}
	for _, tc := range cases {
		row := appSnapshotRow{
			Phase:        tc.phase,
			LiveSource:   "k8s",
			LastSyncedAt: time.Now().Add(-30 * time.Minute),
			FirstSeenAt:  time.Now().Add(-30 * 24 * time.Hour),
		}
		verdict, stale := classifyAppHealth(row)
		if verdict != tc.want {
			t.Errorf("phase %q: verdict = %q, want %q", tc.phase, verdict, tc.want)
		}
		if stale {
			t.Errorf("phase %q: a settled phase must never report stale", tc.phase)
		}
	}
}

// TestClassifyAppHealth_FreshK8sNonTerminalIsNotReady mirrors
// brokenAppSnapshotPredicate directly: a live k8s workload outside
// Ready/Stopped/Orphaned, synced within the freshness window, is proven
// not_ready.
func TestClassifyAppHealth_FreshK8sNonTerminalIsNotReady(t *testing.T) {
	row := appSnapshotRow{
		Phase:        "CrashLoopBackOff",
		LiveSource:   "k8s",
		LastSyncedAt: time.Now().Add(-1 * time.Minute),
		FirstSeenAt:  time.Now().Add(-30 * 24 * time.Hour),
	}
	verdict, stale := classifyAppHealth(row)
	if verdict != appHealthNotReady {
		t.Fatalf("verdict = %q, want %q", verdict, appHealthNotReady)
	}
	if stale {
		t.Fatal("a snapshot synced 1 minute ago must not be reported stale")
	}
}

// TestClassifyAppHealth_StaleK8sSnapshotIsUnknownNotNotReady is the case
// brokenAppSnapshotPredicate's freshness clause exists to protect: a k8s
// snapshot last synced outside appHealthStaleWindow cannot be trusted to
// still describe the live workload (classic stale-ghost-after-move case), so
// it must report unknown+stale, never a confident not_ready.
func TestClassifyAppHealth_StaleK8sSnapshotIsUnknownNotNotReady(t *testing.T) {
	row := appSnapshotRow{
		Phase:        "CrashLoopBackOff",
		LiveSource:   "k8s",
		LastSyncedAt: time.Now().Add(-11 * time.Minute),
		FirstSeenAt:  time.Now().Add(-30 * 24 * time.Hour),
	}
	verdict, stale := classifyAppHealth(row)
	if verdict != appHealthUnknown {
		t.Fatalf("verdict = %q, want %q", verdict, appHealthUnknown)
	}
	if !stale {
		t.Fatal("a snapshot synced 11 minutes ago must be reported stale")
	}
}

// TestClassifyAppHealth_NoSignalMeasuresFromFirstSeenNotLastSynced is
// RED-proof for the exact grabli noSignalAppSnapshotPredicate's own doc
// comment warns about: last_synced_at is re-stamped on every reconcile tick,
// so a row with a fresh last_synced_at but an old first_seen_at must still
// classify as no_signal. Grounding the classifier in first_seen_at (not
// last_synced_at) is what this test pins.
func TestClassifyAppHealth_NoSignalMeasuresFromFirstSeenNotLastSynced(t *testing.T) {
	row := appSnapshotRow{
		Phase:        "Pending",
		LiveSource:   "",
		LastSyncedAt: time.Now(),
		FirstSeenAt:  time.Now().Add(-3 * 24 * time.Hour),
	}
	verdict, stale := classifyAppHealth(row)
	if verdict != appHealthNoSignal {
		t.Fatalf("verdict = %q, want %q (last_synced_at is fresh but first_seen_at is 3 days old)", verdict, appHealthNoSignal)
	}
	if stale {
		t.Fatal("stale is a k8s-only concept; a no_signal row was never live in the first place")
	}
}

// TestClassifyAppHealth_YoungWorkloadlessAppIsUnknownNotNoSignal is the
// control case: an app still inside its first-build grace window has no
// workload for an ordinary reason and must not be reported as no_signal.
func TestClassifyAppHealth_YoungWorkloadlessAppIsUnknownNotNoSignal(t *testing.T) {
	row := appSnapshotRow{
		Phase:        "Pending",
		LiveSource:   "",
		LastSyncedAt: time.Now(),
		FirstSeenAt:  time.Now().Add(-5 * time.Minute),
	}
	verdict, _ := classifyAppHealth(row)
	if verdict != appHealthUnknown {
		t.Fatalf("verdict = %q, want %q: 5 minutes old is normal first-build provisioning, not a missing signal", verdict, appHealthUnknown)
	}
}

// TestAppHealthNote_NonEmptyForStaleAndNoSignal pins the contract clause that
// matters most: silence must never read as health. Both cases that mean "the
// platform cannot currently see this app" must carry an honest note; every
// settled verdict must not.
func TestAppHealthNote_NonEmptyForStaleAndNoSignal(t *testing.T) {
	if note := appHealthNote(appHealthUnknown, true); note == "" {
		t.Error("a stale snapshot must carry a non-empty note")
	}
	if note := appHealthNote(appHealthNoSignal, false); note == "" {
		t.Error("no_signal must carry a non-empty note")
	}
	for _, v := range []appHealthVerdict{appHealthReady, appHealthNotReady, appHealthStopped, appHealthOrphaned} {
		if note := appHealthNote(v, false); note != "" {
			t.Errorf("verdict %q is a settled/trustworthy answer and must not carry a note, got %q", v, note)
		}
	}
}
