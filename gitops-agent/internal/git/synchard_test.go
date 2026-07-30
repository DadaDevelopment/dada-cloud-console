package git_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

// removeOnRemote deletes path on the remote branch through a throwaway clone,
// simulating another actor (the console's DeleteApp) removing a manifest. It
// adds a keeper file in the same commit so the index never empties, which
// go-git rejects as an empty commit.
func removeOnRemote(t *testing.T, remoteDir, path string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "pusher")
	repo, err := gogit.PlainClone(dir, false, &gogit.CloneOptions{URL: remoteDir})
	if err != nil {
		t.Fatalf("clone for remote delete: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("pusher worktree: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatalf("rm %s: %v", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		t.Fatalf("stage deletion %s: %v", path, err)
	}
	writeAndAdd(t, dir, wt, "keeper.yaml", "keeper: yes\n")
	if _, err := wt.Commit("remove "+path, &gogit.CommitOptions{
		Author: &object.Signature{Name: "pusher", Email: "pusher@test", When: time.Now()},
	}); err != nil {
		t.Fatalf("pusher commit: %v", err)
	}
	if err := repo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec("refs/heads/" + staleTestBranch + ":refs/heads/" + staleTestBranch),
		},
	}); err != nil {
		t.Fatalf("pusher push: %v", err)
	}
}

// TestSyncHard_DropsPathsDeletedUpstream is the regression guard for the
// console listing apps that no longer exist: the orphan GC decides "still in
// git" by stat'ing this worktree, so a checkout that lags the remote makes
// deleted apps immortal. A drifted index (staged leftovers) is what made Pull
// stop updating the checkout in production, so the test reproduces that first.
func TestSyncHard_DropsPathsDeletedUpstream(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	repo, err := gogit.PlainOpen(mgr.LocalPath())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("clone worktree: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(mgr.LocalPath(), "ghost"), 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}
	writeAndAdd(t, mgr.LocalPath(), wt, "ghost/app.yaml", "ghost: staged\n")
	if err := os.WriteFile(filepath.Join(mgr.LocalPath(), "ghost/values.yaml"), []byte("ghost: untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	removeOnRemote(t, remoteDir, "victim.yaml")

	if err := mgr.SyncHard(); err != nil {
		t.Fatalf("SyncHard: %v", err)
	}

	for _, gone := range []string{"victim.yaml", "ghost/app.yaml", "ghost/values.yaml"} {
		if _, err := os.Stat(filepath.Join(mgr.LocalPath(), gone)); !os.IsNotExist(err) {
			t.Errorf("%s still on disk after SyncHard (stat err=%v) — existence probes would lie", gone, err)
		}
	}
}

// TestSyncHard_KeepsRemoteContent guards the other direction: the sweep must
// not wipe files the remote still has, or every app would look deleted.
func TestSyncHard_KeepsRemoteContent(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mgr.LocalPath(), "victim.yaml"), []byte("victim: locally dirty\n"), 0o644); err != nil {
		t.Fatalf("dirty write: %v", err)
	}

	if err := mgr.SyncHard(); err != nil {
		t.Fatalf("SyncHard: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(mgr.LocalPath(), "victim.yaml"))
	if err != nil {
		t.Fatalf("victim.yaml missing after SyncHard: %v", err)
	}
	if string(got) != "victim: original\n" {
		t.Errorf("victim.yaml = %q, want remote content restored", string(got))
	}
}
