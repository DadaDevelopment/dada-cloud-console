package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestInsertDeletePreviewEnvOp_IsAudited pins the audit row on a legacy preview
// environment's death. Creation is gone with the feature; teardown still runs
// for environments opened before the removal, and it is enqueued from a GitHub
// webhook, so the actor is the system user while the event itself is something
// a person did -- closed a pull request. On prod the preview operations wrote 17
// rows in 30 days against zero audit rows, so the lifecycle was absent from path
// analysis entirely.
func TestInsertDeletePreviewEnvOp_IsAudited(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	namespace := "ns-" + uuid.NewString()[:8]

	deleteOp, err := InsertDeletePreviewEnvOp(ctx, pool, SystemUserID, projectID, envID, namespace)
	if err != nil {
		t.Fatalf("InsertDeletePreviewEnvOp: %v", err)
	}
	assertPreviewAudited(t, pool, "DeletePreviewEnv", deleteOp, envID, namespace)

	var trigger *string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'trigger' FROM audit_events WHERE operation_id = $1`, deleteOp,
	).Scan(&trigger); err != nil {
		t.Fatalf("read DeletePreviewEnv audit metadata: %v", err)
	}
	if trigger == nil || *trigger != "pr_event" {
		t.Errorf("metadata.trigger = %v, want pr_event — otherwise the teardown cannot be told apart from a reaper's", trigger)
	}
}

func assertPreviewAudited(t *testing.T, pool *pgxpool.Pool, action string, opID, envID uuid.UUID, namespace string) {
	t.Helper()
	var gotAction, gotKind, gotName string
	var gotEnv *uuid.UUID
	var actor uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT action, resource_kind, resource_name, environment_id, actor_id
		   FROM audit_events WHERE operation_id = $1`, opID,
	).Scan(&gotAction, &gotKind, &gotName, &gotEnv, &actor); err != nil {
		t.Fatalf("%s enqueued an operation but wrote no audit row — the preview lifecycle is then invisible to path analysis: %v", action, err)
	}
	if gotAction != action {
		t.Errorf("action = %q, want %q", gotAction, action)
	}
	if gotKind != "Environment" || gotName != namespace {
		t.Errorf("resource = %s/%s, want Environment/%s", gotKind, gotName, namespace)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %s", gotEnv, envID)
	}
	if actor != SystemUserID {
		t.Errorf("actor_id = %s, want the system user %s", actor, SystemUserID)
	}
}
