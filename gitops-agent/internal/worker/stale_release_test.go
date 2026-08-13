package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedRelease inserts one DeployImageVersion operation for an app and returns it
// in the shape the worker claims it as.
func seedRelease(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	projectID uuid.UUID, envID uuid.UUID, appName, image, status string, createdAt time.Time) db.Operation {
	t.Helper()
	id := uuid.New()
	payload := []byte(`{"app_name":"` + appName + `","image":"` + image + `"}`)
	if _, err := pool.Exec(ctx, `
		INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload, created_at)
		VALUES ($1, $2, $3, $4, 'DeployImageVersion', 'App', $5, $6, $7, $8)
	`, id, db.SystemActorID, projectID, envID, appName, status, payload, createdAt); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return db.Operation{
		ID: id, ActorID: db.SystemActorID, ProjectID: projectID, EnvironmentID: &envID,
		Action: "DeployImageVersion", ResourceKind: "App", ResourceName: appName,
		Payload: payload, CreatedAt: createdAt,
	}
}

// dropSeededOperations removes the rows a test seeded; the suite runs against a
// shared database, so a test owns only what it inserted.
func dropSeededOperations(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	_, _ = pool.Exec(ctx, `DELETE FROM operations WHERE project_id = $1`, projectID)
}

// seedProjectEnv creates the project and environment rows the operations table
// references, returning the environment id.
func seedProjectEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8]); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	envID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		envID, projectID, "ns-"+envID.String()[:8]); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return envID
}

// TestStaleReleaseIsRefusedByALandedNewerOne is the regression for the findata
// rollback: a queued release must not overwrite a newer image that is already
// rendered into git, no matter which order the queue hands the operations out.
func TestStaleReleaseIsRefusedByALandedNewerOne(t *testing.T) {
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
	envID := seedProjectEnv(t, ctx, pool, projectID)
	base := time.Now().Add(-time.Hour)
	old := seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/profi-backend:529", "Processing", base)
	seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/profi-backend:530", "Committed", base.Add(10*time.Minute))
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}

	landed, err := w.supersededByLandedRelease(ctx, old, "backend")
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if landed != "nexus/profi-backend:530" {
		t.Fatalf("guard saw %q, want the newer landed image; a stale release would roll production back", landed)
	}

	if err := w.updateComposeAppImage(ctx, old, "backend", "nexus/profi-backend:529"); err != nil {
		t.Fatalf("updateComposeAppImage: %v", err)
	}
	var status, code string
	if err := pool.QueryRow(ctx,
		`SELECT status, COALESCE(error_code, '') FROM operations WHERE id = $1`, old.ID).Scan(&status, &code); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if status != "Failed" || code != "SUPERSEDED_BY_NEWER_DEPLOY" {
		t.Fatalf("stale operation ended %s/%s, want Failed/SUPERSEDED_BY_NEWER_DEPLOY: a green build must not mean an unapplied image", status, code)
	}
}

// TestNewestReleaseIsNotRefusedByAQueuedOne keeps the guard from freezing the
// normal path: an operation that is merely QUEUED after this one has landed
// nothing, a landed OLDER one is what this release is meant to replace, and
// another app's release is none of its business.
func TestNewestReleaseIsNotRefusedByAQueuedOne(t *testing.T) {
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
	envID := seedProjectEnv(t, ctx, pool, projectID)
	base := time.Now().Add(-time.Hour)
	seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/profi-backend:528", "Committed", base)
	cur := seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/profi-backend:529", "Processing", base.Add(5*time.Minute))
	seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/profi-backend:530", "Created", base.Add(10*time.Minute))
	seedRelease(t, ctx, pool, projectID, envID, "frontend", "nexus/profi:600", "Committed", base.Add(20*time.Minute))
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}
	landed, err := w.supersededByLandedRelease(ctx, cur, "backend")
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if landed != "" {
		t.Fatalf("guard refused a current release because of %q; queued and other-app operations are not evidence", landed)
	}
}
