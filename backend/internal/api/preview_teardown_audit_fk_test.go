package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestPreviewTeardownSurvivesAuditRows guards migration 093. doDeletePreviewEnv
// runs `DELETE FROM environments` as its last step, after the app folders and
// the namespace-policy file are already removed and pushed, so an FK that
// blocks that delete does not fail safely -- it leaves a preview with no
// manifests and a live row. Migration 044 made operations and
// resource_snapshots survive this; 068 added audit_events.environment_id and
// re-armed it.
func TestPreviewTeardownSurvivesAuditRows(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"teardown-fk-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, is_ephemeral, pr_number)
		 VALUES ($1, 'pr-1', $2, 'preview', true, 1) RETURNING id`,
		projectID, "teardown-fk-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	action := "TeardownFKProbe" + suffix
	var auditID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome)
		 VALUES ($1, $2, $3, $4, 'Environment', $5, 'success') RETURNING id`,
		systemDeployActorID, projectID, envID, action, "teardown-fk-ns-"+suffix,
	).Scan(&auditID); err != nil {
		t.Fatalf("seed audit row: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE id = $1`, auditID) })

	if _, err := pool.Exec(ctx, `DELETE FROM environments WHERE id = $1 AND is_ephemeral`, envID); err != nil {
		t.Fatalf("preview teardown must not be blocked by its own audit trail: %v", err)
	}

	var envRef *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT environment_id FROM audit_events WHERE id = $1`, auditID,
	).Scan(&envRef); err != nil {
		t.Fatalf("audit row must outlive the environment: %v", err)
	}
	if envRef != nil {
		t.Fatalf("environment_id must be NULLed by the teardown, got %v", *envRef)
	}
}
