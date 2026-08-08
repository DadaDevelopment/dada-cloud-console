package worker

import (
	"context"
	"os"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAppWithGitRepo inserts a project, an owning environment, an actor, and a
// git_repos row for one app in that environment -- the minimum doDeleteApp's
// cleanup needs to act on. Returns the ids the tests assert against.
func seedAppWithGitRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appName string) (projectID, environmentID, repoID uuid.UUID) {
	t.Helper()

	actorID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', 'Test')`,
		actorID, "u-"+actorID.String(), actorID.String()+"@test.local")

	projectID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	environmentID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		environmentID, projectID, "ns-"+environmentID.String()[:8])

	repoID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, $4, 'github', $5, $6)`,
		repoID, projectID, environmentID, appName, "org/"+appName, "https://github.com/org/"+appName+".git")

	return projectID, environmentID, repoID
}

// seedEphemeralEnvReferencing inserts an ephemeral (preview) environment whose
// git_repo_id points at repoID -- the case that used to make the guarded
// DELETE in doDeleteApp never fire, leaving the git_repos row (and the
// NotDeployed placeholder it renders as) alive forever.
func seedEphemeralEnvReferencing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, repoID uuid.UUID) uuid.UUID {
	t.Helper()
	previewID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type, is_ephemeral, git_repo_id)
		 VALUES ($1, $2, 'pr-1', $3, 'preview', true, $4)`,
		previewID, projectID, "ns-"+previewID.String()[:8], repoID)
	return previewID
}

func newTestDBWatcher(pool *pgxpool.Pool) *DBWatcher {
	return &DBWatcher{pool: pool, cfg: &config.Config{}}
}

// TestDeleteAppGitRepo_ReferencedByPreviewEnv is the regression test for the
// prod defect: 32/32 DeleteApp operations reached Committed, yet 6 apps stayed
// visible and got deleted 2-3 times, because the old guarded DELETE silently
// no-oped whenever an (ephemeral, dead-feature) preview environment still
// referenced the app's git_repos row. Asserts the row is gone, the preview
// environment row survives with git_repo_id cleared (not cascade-deleted), and
// a DeletePreviewEnv operation was enqueued to actually tear it down.
func TestDeleteAppGitRepo_ReferencedByPreviewEnv(t *testing.T) {
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

	appName := "web"
	projectID, environmentID, repoID := seedAppWithGitRepo(t, ctx, pool, appName)
	previewID := seedEphemeralEnvReferencing(t, ctx, pool, projectID, repoID)

	w := newTestDBWatcher(pool)
	w.deleteAppGitRepo(ctx, projectID, &environmentID, appName)

	var repoCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM git_repos WHERE id = $1`, repoID).Scan(&repoCount); err != nil {
		t.Fatalf("count git_repos: %v", err)
	}
	if repoCount != 0 {
		t.Errorf("git_repos row survived: got %d, want 0 -- the app will keep rendering as a NotDeployed phantom", repoCount)
	}

	var envExists bool
	var envGitRepoID *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT true, git_repo_id FROM environments WHERE id = $1`, previewID,
	).Scan(&envExists, &envGitRepoID); err != nil {
		t.Fatalf("read preview environment: %v (the ON DELETE CASCADE from git_repos must not have fired)", err)
	}
	if !envExists {
		t.Fatal("preview environment row was cascade-deleted; it must survive until its own DeletePreviewEnv operation runs")
	}
	if envGitRepoID != nil {
		t.Errorf("preview environment git_repo_id = %v, want NULL", *envGitRepoID)
	}

	var opCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM operations WHERE environment_id = $1 AND action = 'DeletePreviewEnv' AND status = 'Created'`,
		previewID,
	).Scan(&opCount); err != nil {
		t.Fatalf("count DeletePreviewEnv operations: %v", err)
	}
	if opCount != 1 {
		t.Errorf("DeletePreviewEnv operations enqueued for the preview env: got %d, want 1", opCount)
	}
}

// TestDeleteAppGitRepo_NoReference is the plain case: no environment
// references the app's git_repos row, so it is just deleted, same as before.
func TestDeleteAppGitRepo_NoReference(t *testing.T) {
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

	appName := "web"
	projectID, environmentID, repoID := seedAppWithGitRepo(t, ctx, pool, appName)

	w := newTestDBWatcher(pool)
	w.deleteAppGitRepo(ctx, projectID, &environmentID, appName)

	var repoCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM git_repos WHERE id = $1`, repoID).Scan(&repoCount); err != nil {
		t.Fatalf("count git_repos: %v", err)
	}
	if repoCount != 0 {
		t.Errorf("git_repos row survived: got %d, want 0", repoCount)
	}

	var opCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM operations WHERE project_id = $1 AND action = 'DeletePreviewEnv'`,
		projectID,
	).Scan(&opCount); err != nil {
		t.Fatalf("count DeletePreviewEnv operations: %v", err)
	}
	if opCount != 0 {
		t.Errorf("DeletePreviewEnv operations enqueued with nothing to tear down: got %d, want 0", opCount)
	}
}
