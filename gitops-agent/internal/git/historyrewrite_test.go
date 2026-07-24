package git_test

import (
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

const rewriteTestBranch = "master"

func rewriteSig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@test", When: time.Now()}
}

// initSeedWithRemote creates a bare remote and a local seed worktree wired to
// push to it, both rooted under t.TempDir().
func initSeedWithRemote(t *testing.T) (remoteDir string, seedDir string, seedRepo *gogit.Repository, wt *gogit.Worktree) {
	t.Helper()

	remoteDir = filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seedDir = filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err = seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	return remoteDir, seedDir, seedRepo, wt
}

func pushBranch(t *testing.T, repo *gogit.Repository, force bool) {
	t.Helper()
	spec := "refs/heads/" + rewriteTestBranch + ":refs/heads/" + rewriteTestBranch
	if force {
		spec = "+" + spec
	}
	if err := repo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec(spec)},
	}); err != nil {
		t.Fatalf("push (force=%v): %v", force, err)
	}
}

// TestCommitsSince_HistoryRewritten is the RED-proof regression for the
// 2026-07-23 incident: a clone whose stored sync cursor points at a commit
// that a history rewrite (reset/force-push) has orphaned must not walk the
// entire reachable history looking for a sentinel that will never appear. It
// must report ErrHistoryRewritten and return zero commits.
//
// Setup: the remote gets commits A, B, C on rewriteTestBranch, then the
// remote is force-rewritten to a new line A, D (B and C dropped). A fresh
// clone of that already-rewritten remote (mirroring a wiped/re-cloned local
// mirror, or a freshly started gitops-agent pod) never fetches B or C at all,
// so calling CommitsSince with the stale cursor B must be recognized as a
// rewrite, not silently replayed.
func TestCommitsSince_HistoryRewritten(t *testing.T) {
	remoteDir, seedDir, seedRepo, wt := initSeedWithRemote(t)

	writeAndAdd(t, seedDir, wt, "a.yaml", "a: 1\n")
	if _, err := wt.Commit("A", &gogit.CommitOptions{Author: rewriteSig()}); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	writeAndAdd(t, seedDir, wt, "b.yaml", "b: 1\n")
	shaB, err := wt.Commit("B", &gogit.CommitOptions{Author: rewriteSig()})
	if err != nil {
		t.Fatalf("commit B: %v", err)
	}
	writeAndAdd(t, seedDir, wt, "c.yaml", "c: 1\n")
	if _, err := wt.Commit("C", &gogit.CommitOptions{Author: rewriteSig()}); err != nil {
		t.Fatalf("commit C: %v", err)
	}
	pushBranch(t, seedRepo, false)

	headRef, err := seedRepo.Head()
	if err != nil {
		t.Fatalf("seed head: %v", err)
	}
	commitC, err := seedRepo.CommitObject(headRef.Hash())
	if err != nil {
		t.Fatalf("resolve head commit: %v", err)
	}
	commitB, err := commitC.Parents().Next()
	if err != nil {
		t.Fatalf("resolve commit B: %v", err)
	}
	commitA, err := commitB.Parents().Next()
	if err != nil {
		t.Fatalf("resolve commit A: %v", err)
	}
	shaA := commitA.Hash

	if err := wt.Reset(&gogit.ResetOptions{Mode: gogit.HardReset, Commit: shaA}); err != nil {
		t.Fatalf("reset to A: %v", err)
	}
	writeAndAdd(t, seedDir, wt, "d.yaml", "d: 1\n")
	if _, err := wt.Commit("D", &gogit.CommitOptions{Author: rewriteSig()}); err != nil {
		t.Fatalf("commit D: %v", err)
	}
	pushBranch(t, seedRepo, true)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    rewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	commits, err := mgr.CommitsSince(shaB.String())
	if err == nil {
		t.Fatalf("CommitsSince(stale cursor) = %d commits, nil error; want ErrHistoryRewritten (replayed history instead of detecting the rewrite)", len(commits))
	}
	if err != git.ErrHistoryRewritten {
		t.Fatalf("CommitsSince(stale cursor) error = %v, want ErrHistoryRewritten", err)
	}
	if len(commits) != 0 {
		t.Fatalf("CommitsSince(stale cursor) returned %d commits on rewrite, want 0", len(commits))
	}
}

// TestCommitsSince_NormalFastForward is the companion regression proving the
// ordinary (non-rewritten) path is untouched: a cursor that is a genuine
// ancestor of HEAD still returns exactly the commits made since that cursor.
func TestCommitsSince_NormalFastForward(t *testing.T) {
	remoteDir, seedDir, seedRepo, wt := initSeedWithRemote(t)

	writeAndAdd(t, seedDir, wt, "a.yaml", "a: 1\n")
	if _, err := wt.Commit("A", &gogit.CommitOptions{Author: rewriteSig()}); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	writeAndAdd(t, seedDir, wt, "b.yaml", "b: 1\n")
	shaB, err := wt.Commit("B", &gogit.CommitOptions{Author: rewriteSig()})
	if err != nil {
		t.Fatalf("commit B: %v", err)
	}
	pushBranch(t, seedRepo, false)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    rewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	writeAndAdd(t, seedDir, wt, "c.yaml", "c: 1\n")
	shaC, err := wt.Commit("C", &gogit.CommitOptions{Author: rewriteSig()})
	if err != nil {
		t.Fatalf("commit C: %v", err)
	}
	pushBranch(t, seedRepo, false)

	commits, err := mgr.CommitsSince(shaB.String())
	if err != nil {
		t.Fatalf("CommitsSince(genuine ancestor cursor): %v", err)
	}
	if len(commits) != 1 || commits[0].SHA != shaC.String() {
		t.Fatalf("CommitsSince(shaB) = %+v, want exactly [%s]", commits, shaC.String())
	}
}
