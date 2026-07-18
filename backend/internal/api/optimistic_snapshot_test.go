package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testOptimisticPool connects to the ephemeral integration database, skipping the
// whole test when TEST_DATABASE_URL is unset so `go test` stays green offline.
func testOptimisticPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping optimistic-snapshot integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedOptimisticFixture creates a throwaway project + prod environment and returns
// their ids, cleaning both up when the test ends.
func seedOptimisticFixture(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"optimistic-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

// TestSeedOptimisticSnapshot exercises the exact create-time behaviour the API now
// relies on for every gitops resource: the row lands as Pending inside the caller's
// transaction, is visible to the list read path, is idempotent under a repeat write
// (ON CONFLICT DO NOTHING), and is removed by the worker's failure-cleanup DELETE so
// the valid-state set stays closed on a failed create.
func TestSeedOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	projectID, envID := seedOptimisticFixture(t, pool)
	name := "db-" + uuid.NewString()[:8]

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := seedOptimisticSnapshot(ctx, tx, projectID, envID, "ServiceDatabaseV2", name, map[string]any{
		"database": "app",
		"spec":     map[string]any{"database": "app"},
	}); err != nil {
		t.Fatalf("seedOptimisticSnapshot: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var phase string
	var rawSummary []byte
	if err := pool.QueryRow(ctx,
		`SELECT phase, summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&phase, &rawSummary); err != nil {
		t.Fatalf("read back snapshot: %v", err)
	}
	if phase != "Pending" {
		t.Fatalf("phase = %q, want Pending", phase)
	}
	var summary map[string]any
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if summary["live_source"] != "create-optimistic" {
		t.Fatalf("live_source = %v, want create-optimistic", summary["live_source"])
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	if err := seedOptimisticSnapshot(ctx, tx2, projectID, envID, "ServiceDatabaseV2", name, map[string]any{"database": "app"}); err != nil {
		t.Fatalf("seedOptimisticSnapshot (idempotent): %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit 2: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after repeat seed = %d, want 1 (idempotent)", count)
	}

	tag, err := pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id IS NOT DISTINCT FROM $2 AND kind = $3 AND name = $4`,
		projectID, envID, "ServiceDatabaseV2", name,
	)
	if err != nil {
		t.Fatalf("cleanup delete: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("cleanup deleted %d rows, want 1", tag.RowsAffected())
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&count); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("row count after cleanup = %d, want 0 (closed state)", count)
	}
}
