package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMarkNoopEndsAnOperationThatHadNothingToChange covers the trap that
// stranded a user's SetDatabaseTier for hours: the handler's "already the wanted
// value" branch returned nil, the dispatcher writes a terminal status only for
// an error, and the row was re-claimed by ClaimPending every 30 minutes forever
// while the console showed the action as still running.
func TestMarkNoopEndsAnOperationThatHadNothingToChange(t *testing.T) {
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

	opID := seedOperationWithAge(t, ctx, pool, projectID, envID, "noop-app", "Processing", 45*time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM audit_events WHERE operation_id = $1`, opID)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = $1`, opID)

	if err := MarkNoop(ctx, pool, opID, "already carries tier \"free\""); err != nil {
		t.Fatalf("mark noop: %v", err)
	}

	var status string
	var gitCommit *string
	var errMessage *string
	if err := pool.QueryRow(ctx,
		`SELECT status, git_commit, error_message FROM operations WHERE id = $1`, opID,
	).Scan(&status, &gitCommit, &errMessage); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if status != "Committed" {
		t.Fatalf("a no-op operation must end terminally, got status %q", status)
	}
	if gitCommit != nil && *gitCommit != "" {
		t.Fatalf("a no-op committed nothing, but git_commit says %q", *gitCommit)
	}
	if errMessage != nil && *errMessage != "" {
		t.Fatalf("a no-op did not fail, but error_message says %q", *errMessage)
	}

	ops, err := ClaimPending(ctx, pool)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, o := range ops {
		if o.ID == opID {
			t.Fatal("a finished no-op operation was re-claimed; the 30-minute loop is still open")
		}
	}

	var outcome string
	var noop bool
	if err := pool.QueryRow(ctx,
		`SELECT outcome, COALESCE((metadata->>'noop')::bool, false)
		   FROM audit_events WHERE operation_id = $1`, opID,
	).Scan(&outcome, &noop); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if outcome != "success" {
		t.Fatalf("a no-op is a success for every reader, got outcome %q", outcome)
	}
	if !noop {
		t.Fatal("the audit row does not say the operation changed nothing")
	}
}
