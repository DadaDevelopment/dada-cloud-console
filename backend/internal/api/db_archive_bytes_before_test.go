package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testArchivePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping db-archive bytes_before DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestArchiveBytesBeforeSurvivesTheTick is the regression for a run that
// reclaimed 226 MB and reported that it had freed nothing.
//
// The reclaim phase measured the table on both sides of one call, but the call
// that creates the Job returns before the Job runs, so the only re-entry that
// reaches the second measurement reads its "before" after the rewrite. The
// pre-rewrite size therefore has to live on the row, not in the call - this
// asserts it round-trips through exactly the read the next tick does.
func TestArchiveBytesBeforeSurvivesTheTick(t *testing.T) {
	pool := testArchivePool(t)
	ctx := context.Background()
	w := &dbArchiveWorker{h: &Handler{pool: pool}}

	datname := "archive-bytes-before-" + uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO db_archive_runs
		       (project_id, environment_id, resource_name, datname, shard,
		        table_name, cutoff_column, cutoff_date, phase, deleted_rows)
		VALUES ($1, $2, 'probe', $3, 'shard-0', 'events', 'created_at',
		        DATE '2026-01-01', 'repack', 100)
		RETURNING id`,
		uuid.New(), uuid.New(), datname).Scan(&id); err != nil {
		t.Fatalf("seed archive run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM db_archive_runs WHERE id = $1`, id)
	})

	const before = int64(309 * 1024 * 1024)
	if err := w.stampBytesBefore(ctx, id, before); err != nil {
		t.Fatalf("stamp the pre-rewrite size: %v", err)
	}

	runs, err := w.openRuns(ctx)
	if err != nil {
		t.Fatalf("read open runs: %v", err)
	}
	var found *archiveRun
	for i := range runs {
		if runs[i].ID == id {
			found = &runs[i]
		}
	}
	if found == nil {
		t.Fatalf("the seeded run is not among the open runs")
	}
	if found.BytesBefore != before {
		t.Fatalf("BytesBefore after a tick boundary = %d, want %d", found.BytesBefore, before)
	}

	const after = int64(76 * 1024 * 1024)
	if freed := archiveFreedBytes(found.BytesBefore, after); freed != before-after {
		t.Fatalf("freed bytes = %d, want %d", freed, before-after)
	}
}
