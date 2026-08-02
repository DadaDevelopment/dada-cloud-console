package worker

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestWipeProjectRows_NoOrphans exercises the DeleteProject DB wipe against a
// real Postgres seeded with a full build+deployment+operations trail, proving
// the FK-safe delete order succeeds (no deployments_operation_id_fkey 23503) and
// leaves zero orphaned rows for the project. Skipped unless TEST_DATABASE_URL is
// set; CI runs go test in a Docker-less container, so this stays a local/opt-in
// integration test.
func TestWipeProjectRows_NoOrphans(t *testing.T) {
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

	projectID := uuid.New()
	seedProjectTrail(t, ctx, pool, projectID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := wipeProjectRows(ctx, tx, projectID); err != nil {
		t.Fatalf("wipeProjectRows: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	assertNoRows(t, ctx, pool, projectID)
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "backend", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dada') THEN CREATE ROLE dada; END IF; END $$;`,
	); err != nil {
		t.Fatalf("create role dada: %v", err)
	}
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}

// seedProjectTrail inserts a project with an environment, an actor, and one row
// in every table wipeProjectRows must clear or that FK-references the deleted
// operation: operations, deployments, domain_hostnames, git_commits,
// audit_events, resource_snapshots, builds, env_vars.
func seedProjectTrail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	t.Helper()

	actorID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', 'Test')`,
		actorID, "u-"+actorID.String(), actorID.String()+"@test.local")

	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		envID, projectID, "ns-"+envID.String()[:8])

	opID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status)
		 VALUES ($1, $2, $3, $4, 'DeployImageVersion', 'App', 'web', 'Committed')`,
		opID, actorID, projectID, envID)

	repoID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, 'web', 'github', 'org/web', 'https://github.com/org/web.git')`,
		repoID, projectID, envID)

	buildID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, status)
		 VALUES ($1, $2, $3, 'web', 'deadbeef', 'main', 'success')`,
		buildID, repoID, envID)

	exec(t, ctx, pool,
		`INSERT INTO deployments (id, environment_id, app_name, build_id, image_uri, operation_id, trigger, is_current)
		 VALUES ($1, $2, 'web', $3, 'harbor/x@sha256:abc', $4, 'push', true)`,
		uuid.New(), envID, buildID, opID)

	authID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO domain_authorizations (id, project_id, apex_domain, verification_token, status, created_by)
		 VALUES ($1, $2, $3, 'tok', 'verified', $4)`,
		authID, projectID, "apex-"+authID.String()[:8]+".example.com", actorID)
	exec(t, ctx, pool,
		`INSERT INTO domain_hostnames (id, authorization_id, environment_id, app_name, hostname, record_type, operation_id)
		 VALUES ($1, $2, $3, 'web', $4, 'A', $5)`,
		uuid.New(), authID, envID, "h-"+envID.String()[:8]+".example.com", opID)

	exec(t, ctx, pool,
		`INSERT INTO git_commits (sha, repo_url, branch, path, message, author_name, author_email, operation_id, source)
		 VALUES ($1, 'r', 'main', 'p', 'm', 'bot', 'bot@x', $2, 'agent')`,
		"sha-"+opID.String()[:12], opID)

	exec(t, ctx, pool,
		`INSERT INTO audit_events (id, actor_id, project_id, operation_id, action)
		 VALUES ($1, $2, $3, $4, 'DeleteProject')`,
		uuid.New(), actorID, projectID, opID)

	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (id, project_id, environment_id, kind, name)
		 VALUES ($1, $2, $3, 'App', 'web')`,
		uuid.New(), projectID, envID)

	exec(t, ctx, pool,
		`INSERT INTO env_vars (id, environment_id, app_name, key, value_encrypted)
		 VALUES ($1, $2, 'web', 'K', $3)`,
		uuid.New(), envID, []byte{0x01})
}

func assertNoRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	t.Helper()
	checks := []struct {
		name string
		sql  string
	}{
		{"projects", `SELECT count(*) FROM projects WHERE id = $1`},
		{"environments", `SELECT count(*) FROM environments WHERE project_id = $1`},
		{"operations", `SELECT count(*) FROM operations WHERE project_id = $1`},
		{"deployments", `SELECT count(*) FROM deployments WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`},
		{"domain_hostnames", `SELECT count(*) FROM domain_hostnames WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`},
		{"builds", `SELECT count(*) FROM builds WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`},
		{"env_vars", `SELECT count(*) FROM env_vars WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`},
		{"audit_events", `SELECT count(*) FROM audit_events WHERE project_id = $1`},
		{"resource_snapshots", `SELECT count(*) FROM resource_snapshots WHERE project_id = $1`},
		{"domain_authorizations", `SELECT count(*) FROM domain_authorizations WHERE project_id = $1`},
	}
	for _, c := range checks {
		var n int
		if err := pool.QueryRow(ctx, c.sql, projectID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("orphaned rows in %s: got %d, want 0", c.name, n)
		}
	}
}

func exec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("seed exec failed: %v\nsql: %s", err, sql)
	}
}

// TestWipeProjectRows_ForeignOperationChildren covers the app-moved-projects
// case: a surviving project's deployments and domain_hostnames rows still point
// at an operation owned by the project being deleted. Scoped deletes never touch
// them, so without a platform-wide detach the operations delete dies with
// domain_hostnames_operation_id_fkey (SQLSTATE 23503). Asserts the wipe succeeds,
// the survivor's rows are still there, and their operation_id is NULL.
func TestWipeProjectRows_ForeignOperationChildren(t *testing.T) {
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

	doomedID := uuid.New()
	seedProjectTrail(t, ctx, pool, doomedID)

	survivorID := uuid.New()
	seedProjectTrail(t, ctx, pool, survivorID)

	var doomedOp uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM operations WHERE project_id = $1`, doomedID,
	).Scan(&doomedOp); err != nil {
		t.Fatalf("read doomed operation: %v", err)
	}

	repoint := func(table string) {
		exec(t, ctx, pool,
			`UPDATE `+table+` SET operation_id = $1
			  WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $2)`,
			doomedOp, survivorID)
	}
	repoint("deployments")
	repoint("domain_hostnames")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := wipeProjectRows(ctx, tx, doomedID); err != nil {
		t.Fatalf("wipeProjectRows: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	assertNoRows(t, ctx, pool, doomedID)

	for _, table := range []string{"deployments", "domain_hostnames"} {
		var total, detached int
		if err := pool.QueryRow(ctx,
			`SELECT count(*), count(*) FILTER (WHERE operation_id IS NULL) FROM `+table+`
			  WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`,
			survivorID,
		).Scan(&total, &detached); err != nil {
			t.Fatalf("count survivor %s: %v", table, err)
		}
		if total != 1 {
			t.Errorf("survivor %s rows: got %d, want 1", table, total)
		}
		if detached != total {
			t.Errorf("survivor %s still references the deleted operation: %d of %d detached", table, detached, total)
		}
	}
}
