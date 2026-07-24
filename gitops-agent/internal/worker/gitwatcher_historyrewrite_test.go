package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

const historyRewriteTestBranch = "master"

func historyRewriteSig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@test", When: time.Now()}
}

func historyRewriteWriteAndAdd(t *testing.T, dir string, wt *gogit.Worktree, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
}

func historyRewritePush(t *testing.T, repo *gogit.Repository, force bool) {
	t.Helper()
	spec := "refs/heads/" + historyRewriteTestBranch + ":refs/heads/" + historyRewriteTestBranch
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

// TestSyncRepo_HistoryRewrittenAdoptsNewHEADWithoutReplay is the watcher-level
// regression for the 2026-07-23 incident: when the stored sync cursor is
// orphaned by a history rewrite, syncRepo must not process any commits (which
// would replay historical project.yaml adds and resurrect deleted projects).
// It must instead fast-forward the stored cursor to the new HEAD and return
// without error, so the watcher resumes cleanly on the next poll.
func TestSyncRepo_HistoryRewrittenAdoptsNewHEADWithoutReplay(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	seedDir := filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}

	historyRewriteWriteAndAdd(t, seedDir, wt, "a.yaml", "a: 1\n")
	if _, err := wt.Commit("A", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	historyRewriteWriteAndAdd(t, seedDir, wt, "clusters/beget-prod/projects/ghost/project.yaml", "project: ghost\n")
	shaB, err := wt.Commit("B", &gogit.CommitOptions{Author: historyRewriteSig()})
	if err != nil {
		t.Fatalf("commit B: %v", err)
	}
	historyRewriteWriteAndAdd(t, seedDir, wt, "c.yaml", "c: 1\n")
	if _, err := wt.Commit("C", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit C: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

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
	historyRewriteWriteAndAdd(t, seedDir, wt, "d.yaml", "d: 1\n")
	if _, err := wt.Commit("D", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit D: %v", err)
	}
	historyRewritePush(t, seedRepo, true)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    historyRewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	newHead, err := mgr.LocalHEAD()
	if err != nil {
		t.Fatalf("LocalHEAD: %v", err)
	}

	if err := db.SetSyncState(ctx, pool, mgr.RepoURL(), mgr.Branch(), shaB.String()); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}

	w := &GitWatcher{pool: pool}
	if err := w.syncRepo(ctx, mgr); err != nil {
		t.Fatalf("syncRepo on rewritten history: %v", err)
	}

	got, err := db.GetSyncState(ctx, pool, mgr.RepoURL(), mgr.Branch())
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got != newHead {
		t.Fatalf("stored cursor after rewrite = %q, want new HEAD %q", got, newHead)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE name = 'ghost'`).Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if n != 0 {
		t.Fatalf("git watcher replayed history and resurrected project 'ghost': count = %d, want 0", n)
	}
}
