package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedProjectEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, envName, namespace string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID := uuid.New()
	envID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, $2)`,
		projectID, slug)
	execReap(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, $3, $4, 'prod')`,
		envID, projectID, envName, namespace)
	return projectID, envID
}

func seedAppSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, phase string) {
	t.Helper()
	execReap(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'App', $3, $4)`,
		projectID, envID, name, phase)
}

// TestSnapshotEnvsByKind_IgnoresOrphanedTwin is the regression guard for the
// monitoring estate going dark in the console on 2026-08-04. Ten apps were
// re-homed from project "platform" to "observability"; the git watcher created
// the new rows and soft-deleted the old ones (phase=Orphaned), and because the
// soft-deleted twin still answered to the same name, the status reconciler
// declared every app ambiguous and attributed its live workload to no
// environment at all. The apps sat at phase "Unknown" with no image, no
// namespaces and no log search, while their pods were healthy the whole time.
//
// A soft-deleted row must never veto the live row that replaced it.
func TestSnapshotEnvsByKind_IgnoresOrphanedTwin(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	newProject, newEnv := seedProjectEnv(t, ctx, pool, "observability", "prod", "observability-prod")
	oldProject, oldEnv := seedProjectEnv(t, ctx, pool, "platform", "prod", "platform-prod")

	seedAppSnapshot(t, ctx, pool, newProject, newEnv, "mimir", "Unknown")
	seedAppSnapshot(t, ctx, pool, oldProject, oldEnv, "mimir", "Orphaned")

	got, err := SnapshotEnvsByKind(ctx, pool, "App")
	if err != nil {
		t.Fatalf("SnapshotEnvsByKind: %v", err)
	}

	envs := got["mimir"]
	if len(envs) != 1 {
		t.Fatalf("mimir resolves to %d environments %v, want exactly 1 — more than one is read as ambiguous and drops live status", len(envs), envs)
	}
	if envs[0] != newEnv {
		t.Errorf("mimir resolves to env %s, want the live one %s (not the orphaned %s)", envs[0], newEnv, oldEnv)
	}
}

// TestSnapshotEnvsByKind_KeepsGenuineAmbiguity guards the other direction: two
// real apps sharing a name in different projects must still be treated as
// ambiguous, or one tenant's pods would be attributed to another's environment.
func TestSnapshotEnvsByKind_KeepsGenuineAmbiguity(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	projectA, envA := seedProjectEnv(t, ctx, pool, "tenant-a", "prod", "tenant-a-prod")
	projectB, envB := seedProjectEnv(t, ctx, pool, "tenant-b", "prod", "tenant-b-prod")

	seedAppSnapshot(t, ctx, pool, projectA, envA, "api", "Running")
	seedAppSnapshot(t, ctx, pool, projectB, envB, "api", "Running")

	got, err := SnapshotEnvsByKind(ctx, pool, "App")
	if err != nil {
		t.Fatalf("SnapshotEnvsByKind: %v", err)
	}
	if len(got["api"]) != 2 {
		t.Errorf("api resolves to %d environments, want 2 so the caller refuses to guess", len(got["api"]))
	}
}
