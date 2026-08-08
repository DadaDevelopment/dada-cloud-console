package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMarkFailed_WritesFailureAudit pins the audit row on an operation that dies
// inside the worker. The success row is written at enqueue time, so without this
// one a user action that was accepted and then failed reads as a success in
// audit_events forever. On prod that was 264 failed operations against a single
// failure row [live psql, 60d].
func TestMarkFailed_WritesFailureAudit(t *testing.T) {
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

	ensureSchemaForTest(t, ctx, pool)

	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	envID := seedPreviewEnv(t, ctx, pool, projectID, false, time.Now().Add(time.Hour))

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'CreateApp', 'App', 'shop', 'Processing', '{}'::jsonb)
		RETURNING id`, systemActorID, projectID, envID).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	if err := MarkFailed(ctx, pool, opID, "PROCESSING_ERROR", "render failed: no such chart"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	var action, kind, name, outcome, reason, errText string
	var gotEnv *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT action, resource_kind, resource_name, outcome, environment_id,
		       metadata->>'reason', metadata->>'error'
		  FROM audit_events WHERE operation_id = $1 AND outcome = 'failure'`, opID,
	).Scan(&action, &kind, &name, &outcome, &gotEnv, &reason, &errText); err != nil {
		t.Fatalf("an operation failed terminally but wrote no audit row — the action stays a success in path analysis: %v", err)
	}
	if action != "CreateApp" || kind != "App" || name != "shop" {
		t.Errorf("row = %s %s/%s, want CreateApp App/shop — the failure must carry the same identity as the success", action, kind, name)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %s", gotEnv, envID)
	}
	if reason != "PROCESSING_ERROR" {
		t.Errorf("metadata.reason = %q, want PROCESSING_ERROR", reason)
	}
	if errText != "render failed: no such chart" {
		t.Errorf("metadata.error = %q, want the worker's message", errText)
	}

	if err := MarkFailed(ctx, pool, opID, "PROCESSING_ERROR", "render failed: no such chart"); err != nil {
		t.Fatalf("MarkFailed (repeat): %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1 AND outcome = 'failure'`, opID).Scan(&n); err != nil {
		t.Fatalf("count failure rows: %v", err)
	}
	if n != 1 {
		t.Errorf("failure rows = %d, want 1 — a retried terminal write must not stack rows", n)
	}
}

// TestMarkCommitted_WritesSuccessAudit pins the audit row on an operation that
// committed with nothing said about it. AttachDefaultDomain is the case that
// matters: both of its call sites are self-repair, so no handler audits it, and
// on prod 15 of them ran against zero audit rows [live psql, 30d] -- a user's app
// gained a public URL with nothing recording when or why.
func TestMarkCommitted_WritesSuccessAudit(t *testing.T) {
	ctx, pool, projectID := setupCommitAuditTest(t)

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, 'AttachDefaultDomain', 'App', 'shop', 'Processing', '{}'::jsonb)
		RETURNING id`, systemActorID, projectID).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	if err := MarkCommitted(ctx, pool, opID, "deadbeef", "apps/shop/values.yaml"); err != nil {
		t.Fatalf("MarkCommitted: %v", err)
	}

	var action, kind, name, phase, sha string
	if err := pool.QueryRow(ctx, `
		SELECT action, resource_kind, resource_name, metadata->>'phase', metadata->>'git_commit'
		  FROM audit_events WHERE operation_id = $1 AND outcome = 'success'`, opID,
	).Scan(&action, &kind, &name, &phase, &sha); err != nil {
		t.Fatalf("an operation committed but wrote no audit row — only its failures are visible to path analysis: %v", err)
	}
	if action != "AttachDefaultDomain" || kind != "App" || name != "shop" {
		t.Errorf("row = %s %s/%s, want AttachDefaultDomain App/shop", action, kind, name)
	}
	if phase != "operation" {
		t.Errorf("metadata.phase = %q, want operation", phase)
	}
	if sha != "deadbeef" {
		t.Errorf("metadata.git_commit = %q, want deadbeef — the row must point at the change that did it", sha)
	}

	if err := MarkCommitted(ctx, pool, opID, "deadbeef", "apps/shop/values.yaml"); err != nil {
		t.Fatalf("MarkCommitted (repeat): %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1`, opID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 — a retried terminal write must not stack rows", n)
	}
}

// TestMarkCommitted_LeavesHandlerAuditAlone pins the guard that keeps the
// coverage fix from double-counting. CreateApp is audited by its handler at
// enqueue; a second row on commit would inflate every count built on
// audit_events, which is the table the funnel is read from.
func TestMarkCommitted_LeavesHandlerAuditAlone(t *testing.T) {
	ctx, pool, projectID := setupCommitAuditTest(t)

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, 'CreateApp', 'App', 'blog', 'Processing', '{}'::jsonb)
		RETURNING id`, systemActorID, projectID).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		VALUES ($1, $2, $3, 'CreateApp', 'App', 'blog', 'success', '{}'::jsonb)`,
		systemActorID, projectID, opID); err != nil {
		t.Fatalf("seed handler audit: %v", err)
	}

	if err := MarkCommitted(ctx, pool, opID, "deadbeef", "apps/blog/values.yaml"); err != nil {
		t.Fatalf("MarkCommitted: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1`, opID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 — an action already audited at enqueue must not get a second row", n)
	}
}

// setupCommitAuditTest connects to the shared test database, applies the schema
// when absent, and seeds one project for the caller's operations to hang off.
func setupCommitAuditTest(t *testing.T) (context.Context, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	ensureSchemaForTest(t, ctx, pool)

	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	return ctx, pool, projectID
}

// ensureSchemaForTest applies the migrations only when the schema is absent.
// applyMigrationsForReapTest drops and rebuilds public, and the whole repo shares
// one test database, so every extra drop widens the window in which a test from
// another package finds its tables gone mid-run.
func ensureSchemaForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var present *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.operations')::text`).Scan(&present); err != nil {
		t.Fatalf("probe schema: %v", err)
	}
	if present == nil {
		applyMigrationsForReapTest(t, ctx, pool)
	}
}
