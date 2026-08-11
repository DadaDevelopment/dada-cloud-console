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

// TestReconcile_UrlReasonClearsWhenHostnameRecovers is the regression guard for
// the class of defect this project has already shipped once (f3c0e268, "an app
// that healed stayed broken forever"): db.UpdateLiveStatus merges its summary_json
// patch via jsonb `||`, which only ADDS/OVERWRITES keys, it never deletes one that
// is absent from the patch. So the write path (statusreconciler.go, the reconcile
// loop around db.PrimaryHostname) must set patchFields["url_reason"] on every
// pass, including when the reason is now empty — otherwise a stale failure reason
// from a broken route survives in the snapshot forever after the route heals,
// because the merge has nothing telling it to remove the old value.
//
// This exercises the real reconcile() end to end against a live Postgres and a
// fake k8s client: it does NOT stub PrimaryHostname or UpdateLiveStatus, so it
// proves the actual glue between the domain_hostnames row, the patch built in
// reconcile(), and the jsonb merge landing in resource_snapshots.summary_json.
func TestReconcile_UrlReasonClearsWhenHostnameRecovers(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "urlreason", "prod", "urlreason-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")

	if _, err := pool.Exec(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, status_reason)
		 VALUES ($1, 'profi', 'profi.dada-tuda.ru', 'CNAME', 'failed', 'route_missing')`,
		envID); err != nil {
		t.Fatalf("seed domain_hostnames (failed): %v", err)
	}

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "urlreason-prod"))
	r := &StatusReconciler{pool: pool, cfg: &config.Config{}, client: client}

	r.reconcile(ctx)

	status, reason := readURLFields(t, ctx, pool, projectID, envID, "profi")
	if status != "failed" || reason != "route_missing" {
		t.Fatalf("after first reconcile: url_status=%q url_reason=%q, want failed/route_missing", status, reason)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET status = 'active', status_reason = NULL
		 WHERE environment_id = $1 AND app_name = 'profi'`,
		envID); err != nil {
		t.Fatalf("flip domain_hostnames to active: %v", err)
	}

	r.reconcile(ctx)

	status, reason = readURLFields(t, ctx, pool, projectID, envID, "profi")
	if status != "active" {
		t.Fatalf("after recovery: url_status=%q, want active", status)
	}
	if reason != "" {
		t.Fatalf("after recovery: url_reason=%q, want empty — the stale route_missing reason must not survive a healed route", reason)
	}
}

// readURLFields reads back summary_json.url_status / summary_json.url_reason for
// the given App snapshot, treating a JSON-absent key and an empty string the
// same way ("no reason"): the contract is "the reason is gone", not "the key is
// gone", and a merge-based writer can satisfy that either way.
func readURLFields(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string) (string, string) {
	t.Helper()
	var raw []byte
	row := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName)
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("read summary_json: %v", err)
	}
	var parsed struct {
		URLStatus string `json:"url_status"`
		URLReason string `json:"url_reason"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal summary_json: %v", err)
	}
	return parsed.URLStatus, parsed.URLReason
}
