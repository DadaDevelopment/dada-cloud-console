package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

// TestSyncRepo_EmptyCursorAdoptsHEADWithoutReplay is the watcher-level
// regression for the 2026-08-08 incident: a repo with no row in git_sync_state
// must not have its history replayed from the root.
//
// Replaying is destructive in one direction only — processCommit syncs adds and
// modifications but drops deletions — so every resource ever created is
// re-applied and nothing that deleted it ever is. The live incident re-inserted
// 52 App snapshots for apps deleted days to weeks earlier, and a user deleted a
// resurrected app a second time.
//
// The repo below carries an add-commit for project 'ghost-firstsync' in its
// history. With no cursor stored, syncRepo must stamp the cursor at HEAD and
// process zero commits, leaving the project unresurrected.
func TestSyncRepo_EmptyCursorAdoptsHEADWithoutReplay(t *testing.T) {
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

	historyRewriteWriteAndAdd(t, seedDir, wt, "clusters/beget-prod/projects/ghost-firstsync/project.yaml", "project: ghost-firstsync\n")
	if _, err := wt.Commit("add ghost-firstsync", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit add: %v", err)
	}
	historyRewriteWriteAndAdd(t, seedDir, wt, "unrelated.yaml", "unrelated: 1\n")
	if _, err := wt.Commit("unrelated", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit unrelated: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    historyRewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	head, err := mgr.LocalHEAD()
	if err != nil {
		t.Fatalf("LocalHEAD: %v", err)
	}

	stored, err := db.GetSyncState(ctx, pool, mgr.RepoURL(), mgr.Branch())
	if err != nil {
		t.Fatalf("GetSyncState precondition: %v", err)
	}
	if stored != "" {
		t.Fatalf("precondition: expected no stored cursor, got %q", stored)
	}

	w := &GitWatcher{pool: pool}
	if err := w.syncRepo(ctx, mgr); err != nil {
		t.Fatalf("syncRepo on empty cursor: %v", err)
	}

	got, err := db.GetSyncState(ctx, pool, mgr.RepoURL(), mgr.Branch())
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got != head {
		t.Fatalf("stored cursor after first sync = %q, want HEAD %q", got, head)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE name = 'ghost-firstsync'`).Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if n != 0 {
		t.Fatalf("git watcher replayed history from root and resurrected project 'ghost-firstsync': count = %d, want 0", n)
	}
}

// TestGetSyncState_RealErrorIsNotAnEmptyCursor pins the other half of the same
// incident. An empty cursor is load-bearing — it now means "adopt HEAD" — so a
// failing query must surface as an error rather than as a fabricated empty
// cursor, which is what previously let a transient pool failure at startup
// impersonate a never-synced repo.
func TestGetSyncState_RealErrorIsNotAnEmptyCursor(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	pool.Close()

	sha, err := db.GetSyncState(ctx, pool, "https://example.invalid/repo.git", "main")
	if err == nil {
		t.Fatalf("GetSyncState on a closed pool returned no error (sha=%q); a dead pool must not be reported as a first poll", sha)
	}
	if sha != "" {
		t.Fatalf("GetSyncState returned sha %q alongside an error, want empty", sha)
	}
}
