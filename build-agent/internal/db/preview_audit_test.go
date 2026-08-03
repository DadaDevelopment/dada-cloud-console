package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestInsertPreviewEnvOps_AreAudited pins the audit rows on a preview
// environment's birth and death. Both are enqueued from a GitHub webhook, so the
// actor is the system user, but the events themselves are things a person did --
// opened a pull request, closed it. On prod they wrote 17 CreatePreviewEnv
// operations in 30 days against zero audit rows, so the whole preview feature was
// absent from path analysis.
func TestInsertPreviewEnvOps_AreAudited(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	appName := "prev-audit-" + uuid.NewString()[:8]
	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "small")
	namespace := "ns-" + uuid.NewString()[:8]

	createOp, err := InsertCreatePreviewEnvOp(ctx, pool, SystemUserID, projectID, envID,
		"pr-7-"+appName, namespace, gitRepoID, 7, "feature/x", envID, appName)
	if err != nil {
		t.Fatalf("InsertCreatePreviewEnvOp: %v", err)
	}
	assertPreviewAudited(t, pool, "CreatePreviewEnv", createOp, envID, namespace)

	var pr *int
	var branch *string
	if err := pool.QueryRow(ctx,
		`SELECT (metadata->>'pr_number')::int, metadata->>'head_branch'
		   FROM audit_events WHERE operation_id = $1`, createOp,
	).Scan(&pr, &branch); err != nil {
		t.Fatalf("read CreatePreviewEnv audit metadata: %v", err)
	}
	if pr == nil || *pr != 7 {
		t.Errorf("metadata.pr_number = %v, want 7 — without it the row cannot be tied back to the pull request", pr)
	}
	if branch == nil || *branch != "feature/x" {
		t.Errorf("metadata.head_branch = %v, want feature/x", branch)
	}

	deleteOp, err := InsertDeletePreviewEnvOp(ctx, pool, SystemUserID, projectID, envID, namespace)
	if err != nil {
		t.Fatalf("InsertDeletePreviewEnvOp: %v", err)
	}
	assertPreviewAudited(t, pool, "DeletePreviewEnv", deleteOp, envID, namespace)
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
