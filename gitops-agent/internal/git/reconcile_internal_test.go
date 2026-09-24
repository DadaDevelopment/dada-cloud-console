package git

import (
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func strandCommit(t *testing.T, mgr *Manager, path, content, message string) plumbing.Hash {
	t.Helper()
	repo, err := gogit.PlainOpen(mgr.LocalPath())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	return commitFile(t, repo, mgr.LocalPath(), path, content, message)
}

func captureReconcile(t *testing.T) *[]ReconcileResult {
	t.Helper()
	var got []ReconcileResult
	SetReconcileHook(func(r ReconcileResult) { got = append(got, r) })
	t.Cleanup(func() { SetReconcileHook(nil) })
	return &got
}

func remoteHead(t *testing.T, mgr *Manager) string {
	t.Helper()
	sha, err := mgr.RemoteHEAD()
	if err != nil {
		t.Fatalf("RemoteHEAD: %v", err)
	}
	return sha
}

// TestPull_ReplaysCommitStrandedByKilledPodAfterRemoteMoved reproduces the
// 2026-09-24 wedge: a pod committed a resize, died before pushing, and someone
// else pushed on top of the remote. The pull that used to fail with
// "non-fast-forward update" on every tick must now deliver the stranded change
// and leave the clone on the remote head.
func TestPull_ReplaysCommitStrandedByKilledPodAfterRemoteMoved(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)
	got := captureReconcile(t)

	stranded := strandCommit(t, mgr, "resize.yaml", "cpu: 40m\n",
		"[DADA Console] Resize app affiliate-site\n\nOperation: 9e5be247-2228-4376-b179-c923ed2e318a\n")
	advanceRemote(t, remoteDir, "pin.yaml", "tag: 687c9f52\n")

	head, err := mgr.Pull()
	if err != nil {
		t.Fatalf("Pull on a diverged clone failed: %v", err)
	}

	if content, ok := remoteFile(t, remoteDir, "resize.yaml"); !ok || content != "cpu: 40m\n" {
		t.Fatalf("stranded change did not reach the remote: %q %v", content, ok)
	}
	if content, ok := remoteFile(t, remoteDir, "pin.yaml"); !ok || content != "tag: 687c9f52\n" {
		t.Fatalf("concurrent writer's change was lost: %q %v", content, ok)
	}
	if rh := remoteHead(t, mgr); head != rh {
		t.Fatalf("clone head %s is not the remote head %s", head, rh)
	}
	if len(*got) != 1 || len((*got)[0].Replayed) != 1 || len((*got)[0].Dropped) != 0 {
		t.Fatalf("reconcile report = %+v, want one replayed commit", *got)
	}
	newSHA := (*got)[0].Replayed[stranded.String()]
	if newSHA == "" || newSHA != head {
		t.Fatalf("replayed map %v does not lead from %s to the remote head %s", (*got)[0].Replayed, stranded, head)
	}
}

// TestPull_PushesCommitStrandedBeforeRemoteMoved covers the first half of
// the same incident: before anyone else pushed, pull reported "already up to
// date" and the resize read its own unpushed file as delivered. The commit must
// be pushed as is, keeping its SHA, so an operation already recorded against it
// stays correct.
func TestPull_PushesCommitStrandedBeforeRemoteMoved(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)
	got := captureReconcile(t)

	stranded := strandCommit(t, mgr, "resize.yaml", "cpu: 40m\n", "resize\n\nOperation: op-1\n")

	head, err := mgr.Pull()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if head != stranded.String() || remoteHead(t, mgr) != stranded.String() {
		t.Fatalf("stranded commit %s not delivered unchanged: local %s remote %s", stranded, head, remoteHead(t, mgr))
	}
	if len(*got) != 1 || (*got)[0].Replayed[stranded.String()] != stranded.String() {
		t.Fatalf("reconcile report = %+v, want the SHA kept", *got)
	}
}

// TestPull_DropsConflictingStrandedCommitAndNamesItsOperation proves the
// fallback: when the remote changed the stranded commit's file, the change is
// not forced over it, the clone returns to the remote, and the hook names the
// operation so it can run again instead of staying Committed on a lost SHA.
func TestPull_DropsConflictingStrandedCommitAndNamesItsOperation(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)
	got := captureReconcile(t)

	stranded := strandCommit(t, mgr, "seed.yaml", "seed: local\n", "edit\n\nOperation: op-2\n")
	advanceRemote(t, remoteDir, "seed.yaml", "seed: remote\n")

	head, err := mgr.Pull()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if content, _ := remoteFile(t, remoteDir, "seed.yaml"); content != "seed: remote\n" {
		t.Fatalf("remote change was overwritten: %q", content)
	}
	if head != remoteHead(t, mgr) {
		t.Fatalf("clone not reset to the remote: %s", head)
	}
	if len(*got) != 1 || len((*got)[0].Dropped) != 1 || (*got)[0].Dropped[0].SHA != stranded.String() {
		t.Fatalf("reconcile report = %+v, want the stranded commit dropped", *got)
	}
	if id := OperationIDFromMessage((*got)[0].Dropped[0].Message); id != "op-2" {
		t.Fatalf("operation id = %q, want op-2", id)
	}
}

func TestPull_BehindRemoteReportsNothing(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)
	got := captureReconcile(t)

	advanceRemote(t, remoteDir, "pin.yaml", "tag: new\n")
	head, err := mgr.Pull()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if head != remoteHead(t, mgr) || len(*got) != 0 {
		t.Fatalf("plain fast-forward disturbed: head %s reports %+v", head, *got)
	}
}
