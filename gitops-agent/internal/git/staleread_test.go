package git

import (
	"testing"
)

// TestEnsureClonedDoesNotRefreshAWarmClone pins the behaviour that caused the
// fin-core/findata outage of 2026-08-13: EnsureCloned returns as soon as the
// path holds a .git directory, so on a long-lived agent it is a no-op and a
// subsequent ReadFile serves whatever the clone last happened to hold — even
// when the branch moved seconds ago.
//
// This is not a bug in EnsureCloned; its contract is "clone if absent". It is a
// trap for every handler that READS a file to decide what to deploy. The guard
// below fails if EnsureCloned ever starts fetching, which would make the
// SyncHard calls that now protect those read paths look redundant.
func TestEnsureClonedDoesNotRefreshAWarmClone(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	if _, err := mgr.ReadFile("source.yaml"); err == nil {
		t.Fatal("source.yaml exists before the remote was advanced; fixture is wrong")
	}

	advanceRemote(t, remoteDir, "source.yaml", "image: new\n")

	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	if got, err := mgr.ReadFile("source.yaml"); err == nil {
		t.Fatalf("EnsureCloned refreshed the clone (read %q); the SyncHard guards on the read paths may now be redundant", got)
	}
}

// TestSyncHardMakesAStaleCloneSeeTheCurrentBranch is the regression guard for
// the adopt path. doAdoptComposeStack reads the source compose and .env that
// BECOME the deployed stack, so it must see the branch as it is now. Before the
// fix it only called EnsureCloned and rendered prod from a five-week-old
// scaffold.
func TestSyncHardMakesAStaleCloneSeeTheCurrentBranch(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	advanceRemote(t, remoteDir, "source.yaml", "image: new\n")

	if err := mgr.SyncHard(); err != nil {
		t.Fatalf("SyncHard: %v", err)
	}
	got, err := mgr.ReadFile("source.yaml")
	if err != nil {
		t.Fatalf("read source.yaml after SyncHard: %v", err)
	}
	if got != "image: new\n" {
		t.Errorf("source.yaml = %q, want the content pushed to the branch", got)
	}
}

// TestSyncHardPicksUpAnUpdateToAFileTheCloneAlreadyHas covers the shape the
// outage actually took: the file was present in the stale clone, so no
// "not found" error ever surfaced — it simply held superseded content, and the
// handler deployed it without complaint.
func TestSyncHardPicksUpAnUpdateToAFileTheCloneAlreadyHas(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	advanceRemote(t, remoteDir, "source.yaml", "image: old\n")

	mgr := newRaceManager(t, remoteDir)
	if got, err := mgr.ReadFile("source.yaml"); err != nil || got != "image: old\n" {
		t.Fatalf("fixture: source.yaml = %q, %v; want the old content", got, err)
	}

	advanceRemote(t, remoteDir, "source.yaml", "image: new\n")

	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	if got, _ := mgr.ReadFile("source.yaml"); got != "image: old\n" {
		t.Fatalf("EnsureCloned refreshed the clone (read %q); this guard assumes it does not", got)
	}

	if err := mgr.SyncHard(); err != nil {
		t.Fatalf("SyncHard: %v", err)
	}
	got, err := mgr.ReadFile("source.yaml")
	if err != nil {
		t.Fatalf("read source.yaml after SyncHard: %v", err)
	}
	if got != "image: new\n" {
		t.Errorf("source.yaml = %q, want %q — a read path that skips SyncHard deploys superseded content", got, "image: new\n")
	}
}
