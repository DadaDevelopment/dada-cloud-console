package db

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

// TestMarkFailed_WritesFailureAudit pins the audit row on an operation this
// agent gave up on. portainer-agent owns the five actions the gitops agent's
// claim query excludes, and on prod seven of them had failed against zero
// failure rows in audit_events [live psql, 60d] -- a failed AppServer enrolment
// read exactly like one that worked.
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8]); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ('00000000-0000-0000-0000-000000000000', $1, 'CreateAppServer', 'AppServer', 'vm-1', 'Processing', '{}'::jsonb)
		RETURNING id`, projectID).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	if err := MarkFailed(ctx, pool, opID, "PROCESSING_ERROR", "beget: vm create rejected"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	var action, kind, name, reason, errText string
	if err := pool.QueryRow(ctx, `
		SELECT action, resource_kind, resource_name, metadata->>'reason', metadata->>'error'
		  FROM audit_events WHERE operation_id = $1 AND outcome = 'failure'`, opID,
	).Scan(&action, &kind, &name, &reason, &errText); err != nil {
		t.Fatalf("an operation failed terminally but wrote no audit row — the action stays a success in path analysis: %v", err)
	}
	if action != "CreateAppServer" || kind != "AppServer" || name != "vm-1" {
		t.Errorf("row = %s %s/%s, want CreateAppServer AppServer/vm-1", action, kind, name)
	}
	if reason != "PROCESSING_ERROR" || errText != "beget: vm create rejected" {
		t.Errorf("metadata = reason %q / error %q, want PROCESSING_ERROR / beget: vm create rejected", reason, errText)
	}

	if err := MarkFailed(ctx, pool, opID, "PROCESSING_ERROR", "beget: vm create rejected"); err != nil {
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

// ensureSchemaForTest applies the backend migrations only when the schema is
// absent. The whole repo shares one test database, so an unconditional
// drop-and-rebuild would pull the tables out from under a test running in
// another package.
func ensureSchemaForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var present *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.operations')::text`).Scan(&present); err != nil {
		t.Fatalf("probe schema: %v", err)
	}
	if present != nil {
		return
	}
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
