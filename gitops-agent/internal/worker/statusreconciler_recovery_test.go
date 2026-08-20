package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
)

// seedStaleCrashReason writes a summary_json carrying a stale crash tail onto
// an already-seeded App snapshot, mimicking what a real CrashLoopBackOff tick
// leaves behind before the app recovers.
func seedStaleCrashReason(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE resource_snapshots
		 SET summary_json = '{"reason":"CrashLoopBackOff","exit_code":137}'::jsonb
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, name); err != nil {
		t.Fatalf("seed stale crash reason: %v", err)
	}
}

// readCrashTail reads back summary_json.reason and summary_json.exit_code for
// the given App snapshot. exitCodeNull is true when the key is present but
// JSON null, exitCodePresent is false when the key is absent entirely.
func readCrashTail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) (reason string, exitCodePresent, exitCodeNull bool) {
	t.Helper()
	var raw []byte
	row := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, name)
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("read summary_json: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal summary_json: %v", err)
	}
	rv, ok := m["reason"]
	if ok {
		if err := json.Unmarshal(rv, &reason); err != nil {
			t.Fatalf("unmarshal reason: %v", err)
		}
	}
	ev, ok := m["exit_code"]
	if !ok {
		return reason, false, false
	}
	return reason, true, string(ev) == "null"
}

// TestReconcile_RecoveredAppClearsStaleCrashTail is the regression case for
// backlog 0422: gitops-agent/internal/worker/statusreconciler.go only wrote
// reason/exit_code into summary_json when la.reason/la.lastExitCode were
// non-empty, so a genuine recovery (phase Ready, ready=1, restarts=0, no
// terminated container) never overwrote the CrashLoopBackOff tail left by a
// prior crash tick. resource_snapshots.summary_json is merged with jsonb `||`
// (db.UpdateLiveStatus), which only ever adds/overwrites keys, so the stale
// values survived forever once written -- a healthy app kept reporting
// reason=CrashLoopBackOff to every direct summary_json reader (MCP tools,
// admin overview's not-ready list). Before the fix this test fails: reason
// stays "CrashLoopBackOff" after a clean reconcile tick against a fully
// healthy Deployment.
func TestReconcile_RecoveredAppClearsStaleCrashTail(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "recovered", "prod", "recovered-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "CrashLoop")
	seedStaleCrashReason(t, ctx, pool, projectID, envID, "profi")

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "recovered-prod"))
	r := &StatusReconciler{pool: pool, cfg: &config.Config{}, client: client}

	r.reconcile(ctx)

	reason, exitCodePresent, exitCodeNull := readCrashTail(t, ctx, pool, projectID, envID, "profi")
	if reason != "" {
		t.Fatalf("reason = %q after recovery, want empty -- stale CrashLoopBackOff survived a healthy reconcile tick", reason)
	}
	if exitCodePresent && !exitCodeNull {
		t.Fatalf("exit_code still set after recovery (present=%v null=%v), want absent or null -- stale exit code survived a healthy reconcile tick", exitCodePresent, exitCodeNull)
	}
}
