package worker

import (
	"context"
	"os"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSyncablePaths_ExcludesDeletions guards the ghost-respawn fix at the
// dispatch layer: paths removed in a commit must not be handed to the sync
// handlers, so a DeleteProject commit never reaches resolveOrCreateProjectEnv
// and cannot recreate the just-deleted project.
func TestSyncablePaths_ExcludesDeletions(t *testing.T) {
	projectYAML := "clusters/beget-prod/projects/foo/project.yaml"
	appYAML := "clusters/beget-prod/projects/foo/environments/prod/apps/web/app.yaml"
	keepYAML := "clusters/beget-prod/projects/bar/project.yaml"

	c := git.Commit{
		Files: []string{projectYAML, appYAML, keepYAML},
		Deleted: map[string]bool{
			projectYAML: true,
			appYAML:     true,
		},
	}

	got := syncablePaths(c)
	if len(got) != 1 || got[0] != keepYAML {
		t.Fatalf("syncablePaths = %v, want [%s]", got, keepYAML)
	}
}

// TestSyncablePaths_NoDeletionsPassthrough keeps the common (no-deletion) path a
// zero-copy passthrough so ordinary commits are unaffected.
func TestSyncablePaths_NoDeletionsPassthrough(t *testing.T) {
	c := git.Commit{Files: []string{"a", "b", "c"}}
	got := syncablePaths(c)
	if len(got) != 3 {
		t.Fatalf("syncablePaths dropped paths: got %v", got)
	}
}

// TestProcessCommit_DeletedProjectNotRecreated is the end-to-end regression test
// for the prod bug: after DeleteProject wipes the DB and git-rm's the project
// tree, the git watcher processes that removal commit and must NOT re-insert the
// project row. Gated on TEST_DATABASE_URL (CI runs Docker-less), mirroring
// TestWipeProjectRows_NoOrphans.
func TestProcessCommit_DeletedProjectNotRecreated(t *testing.T) {
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

	countFoo := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE name = 'foo'`).Scan(&n); err != nil {
			t.Fatalf("count projects: %v", err)
		}
		return n
	}
	if countFoo() != 0 {
		t.Fatalf("baseline: project foo already present")
	}

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/org/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})

	projectYAML := "clusters/beget-prod/projects/foo/project.yaml"
	appYAML := "clusters/beget-prod/projects/foo/environments/prod/apps/web/app.yaml"
	delCommit := git.Commit{
		SHA:     "0000000000000000000000000000000000000000",
		Message: "DeleteProject foo",
		Author:  "gitops-agent",
		Email:   "bot@dada",
		Files:   []string{projectYAML, appYAML},
		Deleted: map[string]bool{projectYAML: true, appYAML: true},
	}

	w := &GitWatcher{pool: pool}
	w.processCommit(ctx, mgr, delCommit)

	if n := countFoo(); n != 0 {
		t.Fatalf("git watcher respawned deleted project: projects WHERE name='foo' = %d, want 0", n)
	}
}
