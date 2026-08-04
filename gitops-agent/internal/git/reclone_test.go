package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

// corruptObjectStore garbles every packfile in the clone, reproducing the
// truncated pack that made production resets fail with "unexpected EOF". The
// refs stay intact, so the fetch still reports up-to-date and the failure lands
// exactly where it did in production: reading objects during the reset.
func corruptObjectStore(t *testing.T, clonePath string) {
	t.Helper()

	packDir := filepath.Join(clonePath, ".git", "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		t.Fatalf("read pack dir: %v", err)
	}
	corrupted := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".pack" {
			continue
		}
		if err := os.WriteFile(filepath.Join(packDir, e.Name()), []byte("PACK garbage"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", e.Name(), err)
		}
		corrupted++
	}
	if corrupted == 0 {
		t.Fatalf("no packfiles in %s — corruption not reproduced", packDir)
	}
}

// TestSyncHard_RecoversFromUnreadableClone is the regression guard for the
// orphan GC going permanently blind: go-git never repacks, so a long-lived
// clone accumulates packs and one bad pack makes every later reset fail the
// same way. The GC treats a failed sync as "cannot verify" and stops pruning
// anything, estate-wide, until a human notices. SyncHard must therefore heal
// itself rather than return the same error forever.
func TestSyncHard_RecoversFromUnreadableClone(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	corruptObjectStore(t, mgr.LocalPath())

	if err := mgr.SyncHard(); err != nil {
		t.Fatalf("SyncHard did not recover from a corrupt clone: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(mgr.LocalPath(), "victim.yaml"))
	if err != nil {
		t.Fatalf("victim.yaml missing after recovery: %v", err)
	}
	if string(got) != "victim: original\n" {
		t.Errorf("victim.yaml = %q, want the remote content after re-clone", string(got))
	}
}

// TestSyncHard_KeepsCloneOnRemoteFailure is the other half of the contract: an
// unreachable remote must not cost the local clone. Throwing it away on a
// network blip would re-clone the whole repo on every tick.
func TestSyncHard_KeepsCloneOnRemoteFailure(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	if err := os.RemoveAll(remoteDir); err != nil {
		t.Fatalf("remove remote: %v", err)
	}

	if err := mgr.SyncHard(); err == nil {
		t.Fatal("SyncHard succeeded with the remote gone, want an error")
	}
	if _, err := os.Stat(filepath.Join(mgr.LocalPath(), ".git")); err != nil {
		t.Errorf("clone discarded on a remote-side failure: %v", err)
	}
}
