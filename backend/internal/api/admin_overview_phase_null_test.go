package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResourceSnapshotPhaseIsNotNullable guards the crash class that took
// /admin/overview down: every Go reader scans resource_snapshots.phase into a
// plain string, so a single NULL row makes pgx fail the scan and the handler
// answer "failed to aggregate projects" -- a whole-platform 500 caused by one
// row a test seeder inserted without the column.
//
// The guard belongs at the schema level (migration 096) because the poisoning
// writer need not be production code: any INSERT that omits the column is
// enough, and reads are what break, far from the writer. An omitted column
// still works -- it falls back to the empty-string default the readers already
// fold into "Unknown" -- so the constraint rejects only a deliberate NULL.
func TestResourceSnapshotPhaseIsNotNullable(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping resource_snapshots phase constraint test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()[:8]
	var projectID, envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"phase-null-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, NULL, '{}')`,
		projectID, envID, "app-"+suffix,
	)
	if err == nil {
		t.Fatal("a NULL phase must be rejected by the schema, not discovered by a 500 on read")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" {
		t.Fatalf("expected not_null_violation (23502), got %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, summary_json)
		 VALUES ($1, $2, 'App', $3, '{}')`,
		projectID, envID, "app-default-"+suffix,
	); err != nil {
		t.Fatalf("an omitted phase column must still insert, got: %v", err)
	}

	var phase string
	if err := pool.QueryRow(ctx,
		`SELECT phase FROM resource_snapshots WHERE project_id = $1 AND name = $2`,
		projectID, "app-default-"+suffix,
	).Scan(&phase); err != nil {
		t.Fatalf("read back defaulted phase: %v", err)
	}
	if phase != "" {
		t.Fatalf("defaulted phase = %q, want empty string", phase)
	}
}
