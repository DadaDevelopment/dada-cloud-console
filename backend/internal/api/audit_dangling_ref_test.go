package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testAuditPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping audit dangling-reference DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAuditActor creates a throwaway user and project for the audit tests and
// removes both afterwards, together with the audit rows the actor writes.
//
// The audit rows go with the actor rather than with the project: these tests
// deliberately produce rows whose project reference is dangling and therefore
// stored as NULL, so dropping the project does not take them along. Dropping
// the user does, because dropSeededUser clears audit_events.actor_id first.
func seedAuditActor(t *testing.T, pool *pgxpool.Pool) (actorID, projectID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, '', $1) RETURNING id`,
		"audit-ref-"+suffix, "audit-ref-"+suffix+"@example.test",
	).Scan(&actorID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		dropSeededUser(pool, actorID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"audit-ref-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})
	return actorID, projectID
}

// TestRecordAuditKeepsRowWhenEnvironmentIsDangling covers the rejection that
// matters most and used to be the one guaranteed to vanish: a request aimed at
// an environment that does not exist. audit_events.environment_id is a foreign
// key, so the best-effort insert was dropped by the database and the attempt
// left no trace at all.
func TestRecordAuditKeepsRowWhenEnvironmentIsDangling(t *testing.T) {
	pool := testAuditPool(t)
	actorID, projectID := seedAuditActor(t, pool)
	ctx := context.Background()

	danglingEnv := uuid.New()
	h := &Handler{pool: pool}
	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: danglingEnv,
		Action:        "DeleteApp",
		ResourceKind:  "App",
		ResourceName:  "some-app",
		Outcome:       auditOutcomeFailure,
		Metadata:      map[string]any{"reason": "environment_not_in_project", "status": 404},
	})

	var envID *uuid.UUID
	var metaRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT environment_id, metadata FROM audit_events WHERE actor_id = $1 AND action = 'DeleteApp'`,
		actorID,
	).Scan(&envID, &metaRaw); err != nil {
		t.Fatalf("the rejection must still be recorded: %v", err)
	}
	if envID != nil {
		t.Fatalf("a dangling environment must be stored as NULL, got %s", envID)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["unresolved_environment_id"] != danglingEnv.String() {
		t.Fatalf("the id that could not be stored must survive in metadata, got %v", meta["unresolved_environment_id"])
	}
	if meta["reason"] != "environment_not_in_project" {
		t.Fatalf("the original metadata must be preserved, got %v", meta["reason"])
	}
}

// TestRecordAuditKeepsRowWhenProjectIsDangling is the same guarantee one level
// up: a request naming a project that does not exist.
func TestRecordAuditKeepsRowWhenProjectIsDangling(t *testing.T) {
	pool := testAuditPool(t)
	actorID, _ := seedAuditActor(t, pool)
	ctx := context.Background()

	danglingProject := uuid.New()
	h := &Handler{pool: pool}
	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID:    danglingProject,
		Action:       "CreateDatabaseBackup",
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: "some-db",
		Outcome:      auditOutcomeFailure,
		Metadata:     map[string]any{"reason": "not_a_writer", "status": 404},
	})

	var projectID *uuid.UUID
	var metaRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT project_id, metadata FROM audit_events WHERE actor_id = $1 AND action = 'CreateDatabaseBackup'`,
		actorID,
	).Scan(&projectID, &metaRaw); err != nil {
		t.Fatalf("the rejection must still be recorded: %v", err)
	}
	if projectID != nil {
		t.Fatalf("a dangling project must be stored as NULL, got %s", projectID)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["unresolved_project_id"] != danglingProject.String() {
		t.Fatalf("the id that could not be stored must survive in metadata, got %v", meta["unresolved_project_id"])
	}
}
