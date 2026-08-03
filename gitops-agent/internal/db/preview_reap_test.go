package db

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReapExpiredPreviewEnvs exercises the reaper query against a real
// Postgres: an expired ephemeral env gets a DeletePreviewEnv op enqueued, a
// not-yet-expired one and a non-ephemeral one do not, and a second sweep is a
// no-op once a Created teardown already exists for the same environment (no
// double-enqueue). Skipped unless TEST_DATABASE_URL is set, mirroring
// worker.TestWipeProjectRows_NoOrphans (opt-in, since CI runs without Docker).
func TestReapExpiredPreviewEnvs(t *testing.T) {
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

	applyMigrationsForReapTest(t, ctx, pool)

	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	expiredID := seedPreviewEnv(t, ctx, pool, projectID, true, time.Now().Add(-time.Hour))
	freshID := seedPreviewEnv(t, ctx, pool, projectID, true, time.Now().Add(time.Hour))
	nonEphemeralID := seedPreviewEnv(t, ctx, pool, projectID, false, time.Now().Add(-time.Hour))

	ids, err := ReapExpiredPreviewEnvs(ctx, pool)
	if err != nil {
		t.Fatalf("ReapExpiredPreviewEnvs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("ReapExpiredPreviewEnvs enqueued %d ops, want 1", len(ids))
	}

	assertReapAudited(t, ctx, pool, expiredID, ids[0])

	assertHasPendingTeardown(t, ctx, pool, expiredID, true)
	assertHasPendingTeardown(t, ctx, pool, freshID, false)
	assertHasPendingTeardown(t, ctx, pool, nonEphemeralID, false)

	var payload struct {
		EnvironmentID string `json:"environment_id"`
		Namespace     string `json:"namespace"`
	}
	if err := pool.QueryRow(ctx,
		`SELECT payload->>'environment_id', payload->>'namespace' FROM operations
		 WHERE environment_id = $1 AND action = 'DeletePreviewEnv'`,
		expiredID,
	).Scan(&payload.EnvironmentID, &payload.Namespace); err != nil {
		t.Fatalf("read enqueued op payload: %v", err)
	}
	if payload.EnvironmentID != expiredID.String() {
		t.Errorf("payload.environment_id = %q, want %q", payload.EnvironmentID, expiredID.String())
	}
	if payload.Namespace == "" {
		t.Errorf("payload.namespace is empty")
	}

	ids2, err := ReapExpiredPreviewEnvs(ctx, pool)
	if err != nil {
		t.Fatalf("ReapExpiredPreviewEnvs (second sweep): %v", err)
	}
	if len(ids2) != 0 {
		t.Fatalf("second sweep enqueued %d ops, want 0", len(ids2))
	}
}

// assertReapAudited pins the audit row for a TTL teardown. Expiry is the one
// end-of-life a user never asks for, so without the row the environment just
// stops existing and path analysis cannot tell that from a preview that was
// never created at all.
func assertReapAudited(t *testing.T, ctx context.Context, pool *pgxpool.Pool, envID, opID uuid.UUID) {
	t.Helper()
	var gotOp uuid.UUID
	var trigger *string
	if err := pool.QueryRow(ctx,
		`SELECT operation_id, metadata->>'trigger' FROM audit_events
		  WHERE action = 'DeletePreviewEnv' AND environment_id = $1`, envID,
	).Scan(&gotOp, &trigger); err != nil {
		t.Fatalf("the reaper enqueued a teardown but wrote no audit row for env %s: %v", envID, err)
	}
	if gotOp != opID {
		t.Errorf("audit operation_id = %s, want %s", gotOp, opID)
	}
	if trigger == nil || *trigger != "ttl_expired" {
		t.Errorf("metadata.trigger = %v, want ttl_expired — a TTL sweep must stay distinguishable from a user closing the PR", trigger)
	}
}

// seedPreviewEnv inserts an environment row and returns its id.
func seedPreviewEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, ephemeral bool, expiresAt time.Time) uuid.UUID {
	t.Helper()
	envID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type, is_ephemeral, expires_at)
		 VALUES ($1, $2, $3, $4, 'preview', $5, $6)`,
		envID, projectID, "env-"+envID.String()[:8], "ns-"+envID.String()[:8], ephemeral, expiresAt)
	return envID
}

// assertHasPendingTeardown checks whether a DeletePreviewEnv op exists for envID.
func assertHasPendingTeardown(t *testing.T, ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, want bool) {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM operations WHERE environment_id = $1 AND action = 'DeletePreviewEnv'`,
		envID,
	).Scan(&n); err != nil {
		t.Fatalf("count teardown ops for %s: %v", envID, err)
	}
	got := n > 0
	if got != want {
		t.Errorf("env %s: has pending teardown = %v, want %v", envID, got, want)
	}
}

func execReap(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("seed exec failed: %v\nsql: %s", err, sql)
	}
}

// applyMigrationsForReapTest resets the schema and applies every backend
// migration in order, mirroring worker.applyMigrations.
func applyMigrationsForReapTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
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
