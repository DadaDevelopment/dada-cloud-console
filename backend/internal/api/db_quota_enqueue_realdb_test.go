package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestEnqueueDatabaseOperations_AreAcceptedByPostgres runs the three
// INSERT ... SELECT ... WHERE NOT EXISTS statements the quota feature enqueues
// its work with against a real Postgres.
//
// They cannot be covered any other way and were all three rejected in
// production: a parameter that appears both in the SELECT list and in the
// dedupe predicate is deduced twice, and Postgres answers 42P08 "inconsistent
// types deduced for parameter $4". Nothing retried and nothing alerted - the
// tier reconciler simply logged one failure per database per hour and never
// tiered anything, so the quota it exists to arm stayed disarmed
// (2026-08-14 11:48 UTC, "db-tier: tick databases=21 retiered=0").
func TestEnqueueDatabaseOperations_AreAcceptedByPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-quota enqueue integration test")
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
		"db-quota-enqueue-test-"+suffix).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	h := &Handler{pool: pool}
	name := "db-" + suffix

	tierOp, err := h.enqueueDatabaseTier(ctx,
		tieredDatabase{ProjectID: projectID, EnvironmentID: envID, Name: name, AppRef: name}, "free")
	if err != nil {
		t.Fatalf("enqueue SetDatabaseTier: %v", err)
	}
	if tierOp == uuid.Nil {
		t.Fatal("enqueue SetDatabaseTier returned no operation on an empty queue")
	}
	again, err := h.enqueueDatabaseTier(ctx,
		tieredDatabase{ProjectID: projectID, EnvironmentID: envID, Name: name, AppRef: name}, "free")
	if err != nil {
		t.Fatalf("re-enqueue SetDatabaseTier: %v", err)
	}
	if again != uuid.Nil {
		t.Fatalf("a second tier flip was queued behind the first: %s", again)
	}

	enfOp, err := h.enqueueDatabaseEnforcement(ctx,
		managedDatabase{ProjectID: projectID, EnvironmentID: envID, Name: name}, dbEnforcementReadOnly)
	if err != nil {
		t.Fatalf("enqueue SetDatabaseEnforcement: %v", err)
	}
	if enfOp == uuid.Nil {
		t.Fatal("enqueue SetDatabaseEnforcement returned no operation on an empty queue")
	}

	aw := &dbArchiveWorker{h: h}
	if err := aw.orderArchiveBucket(ctx, archiveRun{
		ID: uuid.New(), ProjectID: projectID, EnvironmentID: envID,
		Datname: name, Bucket: archiveBucketName(projectID),
	}); err != nil {
		t.Fatalf("order the archive bucket: %v", err)
	}
}
