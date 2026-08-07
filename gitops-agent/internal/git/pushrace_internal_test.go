package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const raceTestBranch = "master"

// seedRaceRemote creates a bare remote with one commit on raceTestBranch and
// returns its path.
func seedRaceRemote(t *testing.T) string {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seedDir := filepath.Join(t.TempDir(), "seed")
	repo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed clone: %v", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatalf("add origin to seed clone: %v", err)
	}
	commitFile(t, repo, seedDir, "seed.yaml", "seed: yes\n", "seed")
	pushBranch(t, repo)
	return remoteDir
}

// advanceRemote commits path on the remote branch through a throwaway clone,
// standing in for the concurrent writer that wins the race.
func advanceRemote(t *testing.T, remoteDir, path, content string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "concurrent")
	repo, err := gogit.PlainClone(dir, false, &gogit.CloneOptions{URL: remoteDir})
	if err != nil {
		t.Fatalf("clone for concurrent write: %v", err)
	}
	commitFile(t, repo, dir, path, content, "concurrent write")
	pushBranch(t, repo)
}

func commitFile(t *testing.T, repo *gogit.Repository, dir, path, content, message string) plumbing.Hash {
	t.Helper()

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
	hash, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit %s: %v", path, err)
	}
	return hash
}

func pushBranch(t *testing.T, repo *gogit.Repository) {
	t.Helper()

	if err := repo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec("refs/heads/" + raceTestBranch + ":refs/heads/" + raceTestBranch),
		},
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		t.Fatalf("push: %v", err)
	}
}

func remoteFile(t *testing.T, remoteDir, path string) (string, bool) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "verify")
	if _, err := gogit.PlainClone(dir, false, &gogit.CloneOptions{URL: remoteDir}); err != nil {
		t.Fatalf("clone for verification: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, path))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read %s from remote: %v", path, err)
	}
	return string(content), true
}

func newRaceManager(t *testing.T, remoteDir string) *Manager {
	t.Helper()

	mgr := New(RepoConfig{RepoURL: remoteDir, Branch: raceTestBranch, LocalBase: t.TempDir()})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	return mgr
}

// TestCommitFilesAndPush_RetriesRaceLostToConcurrentWriter is the regression
// guard for the false terminal deploy failure of 2026-08-06: the remote moved
// between fetch and push, the server said "cannot lock ref '...': is at X but
// expected Y", and substring matching on "non-fast-forward"/"rejected" missed
// it, so a race that only needed a retry was reported to the user as a failed
// deploy.
func TestCommitFilesAndPush_RetriesRaceLostToConcurrentWriter(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	raced := false
	mgr.prePush = func(plumbing.Hash) {
		if raced {
			return
		}
		raced = true
		advanceRemote(t, remoteDir, "concurrent.yaml", "concurrent: yes\n")
	}

	sha, err := mgr.CommitFilesAndPush(
		[]FileChange{{Path: "apps/ours.yaml", Content: "image: ours\n"}},
		"deploy ours", "gitops", "gitops@test")
	if err != nil {
		t.Fatalf("CommitFilesAndPush after a lost race: %v", err)
	}
	if !raced {
		t.Fatal("hook never ran: the test did not reproduce a race")
	}
	if sha == "" {
		t.Fatal("no commit SHA returned")
	}

	ours, ok := remoteFile(t, remoteDir, "apps/ours.yaml")
	if !ok || ours != "image: ours\n" {
		t.Fatalf("our file on remote = %q, present=%v; want it pushed", ours, ok)
	}
	if _, ok := remoteFile(t, remoteDir, "concurrent.yaml"); !ok {
		t.Fatal("concurrent writer's file is gone: the retry clobbered it instead of rebuilding on top")
	}
}

// TestCommitFilesAndPush_TerminalErrorIsNotRetried keeps the retry narrow: a
// push that fails while the remote branch stays put is a real failure and must
// surface immediately, not spin through attempts. The remote is made readable
// but unwritable so the failure lands on the push itself — deleting it instead
// would fail earlier, in the fetch, and never exercise the retry decision.
func TestCommitFilesAndPush_TerminalErrorIsNotRetried(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	attempts := 0
	mgr.prePush = func(plumbing.Hash) {
		attempts++
		chmodTree(t, remoteDir, 0o500)
	}
	t.Cleanup(func() { chmodTree(t, remoteDir, 0o700) })

	if _, err := mgr.CommitFilesAndPush(
		[]FileChange{{Path: "apps/ours.yaml", Content: "image: ours\n"}},
		"deploy ours", "gitops", "gitops@test"); err == nil {
		t.Fatal("push to a read-only remote returned no error")
	}
	if attempts != 1 {
		t.Fatalf("push attempted %d times against an unmoved remote; want 1", attempts)
	}
}

// chmodTree sets mode on every directory under root.
func chmodTree(t *testing.T, root string, mode os.FileMode) {
	t.Helper()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		t.Fatalf("chmod %s to %o: %v", root, mode, err)
	}
}

// TestCommitFilesAndPush_AlreadyUpToDateIsSuccess covers the other false
// failure of the same class: the commit is already on the remote branch, so
// go-git answers NoErrAlreadyUpToDate, which is delivery, not an error.
func TestCommitFilesAndPush_AlreadyUpToDateIsSuccess(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	mgr.prePush = func(plumbing.Hash) {
		repo, err := gogit.PlainOpen(mgr.LocalPath())
		if err != nil {
			t.Fatalf("opening manager clone: %v", err)
		}
		pushBranch(t, repo)
	}

	sha, err := mgr.CommitFilesAndPush(
		[]FileChange{{Path: "apps/ours.yaml", Content: "image: ours\n"}},
		"deploy ours", "gitops", "gitops@test")
	if err != nil {
		t.Fatalf("CommitFilesAndPush when the commit already landed: %v", err)
	}
	if sha == "" {
		t.Fatal("no commit SHA returned")
	}
	if content, ok := remoteFile(t, remoteDir, "apps/ours.yaml"); !ok || content != "image: ours\n" {
		t.Fatalf("our file on remote = %q, present=%v; want it pushed", content, ok)
	}
}

// TestRemoveAndPush_RetriesRaceLostToConcurrentWriter covers the deletion side
// of the same defect: RemoveAndPush shares the retry path, so a delete that
// loses the fetch-to-push window must rebuild on the new remote head instead of
// failing the operation that removes an app from gitops.
func TestRemoveAndPush_RetriesRaceLostToConcurrentWriter(t *testing.T) {
	remoteDir := seedRaceRemote(t)
	mgr := newRaceManager(t, remoteDir)

	if _, err := mgr.CommitFilesAndPush(
		[]FileChange{{Path: "apps/doomed.yaml", Content: "image: doomed\n"}},
		"add doomed", "gitops", "gitops@test"); err != nil {
		t.Fatalf("seeding the file to remove: %v", err)
	}

	raced := false
	mgr.prePush = func(plumbing.Hash) {
		if raced {
			return
		}
		raced = true
		advanceRemote(t, remoteDir, "concurrent.yaml", "concurrent: yes\n")
	}

	sha, err := mgr.RemoveAndPush([]string{"apps/doomed.yaml"}, "remove doomed", "gitops", "gitops@test")
	if err != nil {
		t.Fatalf("RemoveAndPush after a lost race: %v", err)
	}
	if !raced {
		t.Fatal("hook never ran: the test did not reproduce a race")
	}
	if sha == "" {
		t.Fatal("no commit SHA returned")
	}

	if _, ok := remoteFile(t, remoteDir, "apps/doomed.yaml"); ok {
		t.Fatal("file still on remote: the removal was lost")
	}
	if _, ok := remoteFile(t, remoteDir, "concurrent.yaml"); !ok {
		t.Fatal("concurrent writer's file is gone: the retry clobbered it instead of rebuilding on top")
	}
}
