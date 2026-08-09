package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestClaimPendingRetakesOperationsAbandonedByADeadWorker covers the operation a
// pod was holding when it was rolled: nothing ever wrote a terminal status, so
// before this the row sat in Processing forever and the user's action was lost
// with the console still showing it as running.
func TestClaimPendingRetakesOperationsAbandonedByADeadWorker(t *testing.T) {
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

	stale := seedOperationWithAge(t, ctx, pool, projectID, envID, "stale-app", "Processing", 45*time.Minute)
	fresh := seedOperationWithAge(t, ctx, pool, projectID, envID, "fresh-app", "Processing", time.Minute)

	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = ANY($1)`, []uuid.UUID{stale, fresh})

	ops, err := ClaimPending(ctx, pool)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimed := map[uuid.UUID]bool{}
	for _, o := range ops {
		claimed[o.ID] = true
	}
	if !claimed[stale] {
		t.Fatal("an operation abandoned by a dead worker was not re-claimed")
	}
	if claimed[fresh] {
		t.Fatal("an operation another worker is still running was stolen")
	}
}

func seedOperationWithAge(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, status string, age time.Duration) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload, created_at, updated_at)
		VALUES ($1, $2, $3, 'CreateApp', 'App', $4, $5, '{}'::jsonb, NOW() - $6::interval, NOW() - $6::interval)
		RETURNING id`, systemActorID, projectID, envID, name, status, age.String()).Scan(&id); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return id
}
