package worker

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCopyPreviewEnvVars exercises copyPreviewEnvVars against a real Postgres:
// a key with no override copies straight through from the parent's env_vars, a
// key with a matching preview_env_overrides row is copied with the override's
// value/is_secret (not the parent's), and an override-only key (no env_vars
// counterpart) is copied in as an ordinary runtime var. A second run is a
// no-op (ON CONFLICT DO NOTHING keeps it idempotent, matching the async
// gitops-agent re-run of the same operation the sync build-agent insert
// already performed). Skipped unless TEST_DATABASE_URL is set, mirroring
// TestWipeProjectRows_NoOrphans.
func TestCopyPreviewEnvVars(t *testing.T) {
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

	applyMigrations(t, ctx, pool)

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	parentID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		parentID, projectID, "ns-parent-"+parentID.String()[:8])

	previewID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type, is_ephemeral, parent_env_id)
		 VALUES ($1, $2, 'pr-1-web', $3, 'preview', TRUE, $4)`,
		previewID, projectID, "ns-preview-"+previewID.String()[:8], parentID)

	exec(t, ctx, pool,
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope)
		 VALUES ($1, 'web', 'APP_NORMAL', $2, FALSE, 'runtime')`,
		parentID, []byte("parent-normal"))
	exec(t, ctx, pool,
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope)
		 VALUES ($1, 'web', 'APP_SCHEDULER_ENABLED', $2, FALSE, 'runtime')`,
		parentID, []byte("parent-true"))

	exec(t, ctx, pool,
		`INSERT INTO preview_env_overrides (environment_id, app_name, key, value_encrypted, is_secret)
		 VALUES ($1, 'web', 'APP_SCHEDULER_ENABLED', $2, FALSE)`,
		parentID, []byte("override-false"))
	exec(t, ctx, pool,
		`INSERT INTO preview_env_overrides (environment_id, app_name, key, value_encrypted, is_secret)
		 VALUES ($1, 'web', 'APP_PREVIEW_ONLY', $2, TRUE)`,
		parentID, []byte("override-only"))

	if err := copyPreviewEnvVars(ctx, pool, previewID, parentID); err != nil {
		t.Fatalf("copyPreviewEnvVars: %v", err)
	}

	assertPreviewEnvVar(t, ctx, pool, previewID, "APP_NORMAL", "parent-normal", false, "runtime")
	assertPreviewEnvVar(t, ctx, pool, previewID, "APP_SCHEDULER_ENABLED", "override-false", false, "runtime")
	assertPreviewEnvVar(t, ctx, pool, previewID, "APP_PREVIEW_ONLY", "override-only", true, "runtime")

	assertParentUnaffected(t, ctx, pool, parentID, "APP_SCHEDULER_ENABLED", "parent-true")

	if err := copyPreviewEnvVars(ctx, pool, previewID, parentID); err != nil {
		t.Fatalf("copyPreviewEnvVars (second run): %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM env_vars WHERE environment_id = $1`, previewID,
	).Scan(&n); err != nil {
		t.Fatalf("count preview env_vars: %v", err)
	}
	if n != 3 {
		t.Fatalf("preview env_vars count after second run = %d, want 3 (idempotent, no duplicates)", n)
	}
}

func assertPreviewEnvVar(t *testing.T, ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, key, wantValue string, wantSecret bool, wantScope string) {
	t.Helper()
	var value []byte
	var isSecret bool
	var scope string
	if err := pool.QueryRow(ctx,
		`SELECT value_encrypted, is_secret, scope FROM env_vars
		 WHERE environment_id = $1 AND app_name = 'web' AND key = $2`,
		envID, key,
	).Scan(&value, &isSecret, &scope); err != nil {
		t.Fatalf("read preview env_var %s: %v", key, err)
	}
	if string(value) != wantValue {
		t.Errorf("preview env_var %s value = %q, want %q", key, value, wantValue)
	}
	if isSecret != wantSecret {
		t.Errorf("preview env_var %s is_secret = %v, want %v", key, isSecret, wantSecret)
	}
	if scope != wantScope {
		t.Errorf("preview env_var %s scope = %q, want %q", key, scope, wantScope)
	}
}

func assertParentUnaffected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, key, wantValue string) {
	t.Helper()
	var value []byte
	if err := pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars
		 WHERE environment_id = $1 AND app_name = 'web' AND key = $2`,
		envID, key,
	).Scan(&value); err != nil {
		t.Fatalf("read parent env_var %s: %v", key, err)
	}
	if string(value) != wantValue {
		t.Errorf("parent env_var %s value = %q, want %q (parent must stay unmodified by the copy)", key, value, wantValue)
	}
}
