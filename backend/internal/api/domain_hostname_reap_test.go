package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/models"
)

// seedOrphanHostname inserts a domain_hostnames row in the given status/reason
// for ReapOrphanedAppHostnames tests. app_name intentionally need not match
// any resource_snapshots row -- that mismatch is exactly what the tests below
// are probing. reattach_count starts at 2 and operation_id points at a real
// operation row (FK-enforced) so the tests can prove ReapOrphanedAppHostnames
// actually resets both, not merely that they started zero/nil.
func seedOrphanHostname(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, hostname, status, certStatus string, statusReason *string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	userID := seedUser(t, pool)

	var opID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status)
		 VALUES ($1, $2, $3, 'AttachCustomHostname', 'App', $4, 'Ready') RETURNING id`,
		userID, projectID, envID, appName,
	).Scan(&opID); err != nil {
		t.Fatalf("seed operation for orphan hostname: %v", err)
	}

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, status_reason, managed, reattach_count, operation_id)
		 VALUES ($1, $2, $3, 'CNAME', $4, $5, $6, true, 2, $7)
		 RETURNING id`,
		envID, appName, hostname, status, certStatus, statusReason, opID,
	).Scan(&id); err != nil {
		t.Fatalf("seed orphan hostname: %v", err)
	}
	return id
}

func readReapedHostnameRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status, certStatus string, statusReason *string, reattachCount int, opID *uuid.UUID, updatedAt time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, cert_status, status_reason, reattach_count, operation_id, updated_at FROM domain_hostnames WHERE id = $1`, id,
	).Scan(&status, &certStatus, &statusReason, &reattachCount, &opID, &updatedAt); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	return
}

// TestReapOrphanedAppHostnamesLeavesLiveAppAlone is the negative case: a
// hostname whose app_name matches a live, fresh App resource_snapshot must
// not be touched, reap or no reap.
func TestReapOrphanedAppHostnamesLeavesLiveAppAlone(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "live-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)

	hostnameID := seedOrphanHostname(t, pool, projectID, envID, appName, appName+".dada-tuda.ru", "active", "active", nil)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "active" {
		t.Fatalf("status = %q, want unchanged active (app is live)", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched)", *reason)
	}
}

// TestReapOrphanedAppHostnamesDemotesDeletedApp is the core positive case: a
// hostname whose app_name has no matching resource_snapshots row -- in an
// environment that DOES have another live, fresh App snapshot proving the
// snapshot pipeline is alive -- gets driven to the same terminal shape
// demoteAppHostnames stamps at delete time.
func TestReapOrphanedAppHostnamesDemotesDeletedApp(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	liveApp := "live-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, liveApp, `{"port":8080}`)

	deletedApp := "gone-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, deletedApp, deletedApp+".dada-tuda.ru", "active", "active", nil)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, certStatus, reason, reattachCount, opID, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if certStatus != "failed" {
		t.Fatalf("cert_status = %q, want failed", certStatus)
	}
	if reason == nil || *reason != hostnameReasonAppDeleted {
		t.Fatalf("status_reason = %v, want %q", reason, hostnameReasonAppDeleted)
	}
	if reattachCount != 0 {
		t.Fatalf("reattach_count = %d, want reset to 0", reattachCount)
	}
	if opID != nil {
		t.Fatalf("operation_id = %v, want cleared", *opID)
	}
}

// TestReapOrphanedAppHostnamesIsIdempotent runs the pass twice over the same
// already-demoted row and requires the second run to change nothing --
// specifically that updated_at does not advance again, which is what proves
// the WHERE clause's own terminal-state exclusion works, not just that the
// UPDATE happens to be a no-op value-wise.
func TestReapOrphanedAppHostnamesIsIdempotent(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	liveApp := "live-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, liveApp, `{"port":8080}`)

	deletedApp := "gone-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, deletedApp, deletedApp+".dada-tuda.ru", "pending", "pending", nil)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames (first run): %v", err)
	}
	_, _, _, _, _, firstUpdatedAt := readReapedHostnameRow(t, pool, hostnameID)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames (second run): %v", err)
	}
	status, certStatus, reason, reattachCount, opID, secondUpdatedAt := readReapedHostnameRow(t, pool, hostnameID)

	if status != "failed" || certStatus != "failed" {
		t.Fatalf("status/cert_status = %q/%q after second run, want failed/failed", status, certStatus)
	}
	if reason == nil || *reason != hostnameReasonAppDeleted {
		t.Fatalf("status_reason after second run = %v, want %q", reason, hostnameReasonAppDeleted)
	}
	if reattachCount != 0 || opID != nil {
		t.Fatalf("reattach_count/operation_id after second run = %d/%v, want 0/nil", reattachCount, opID)
	}
	if !secondUpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("updated_at changed on second run (%v -> %v), want untouched by an already-terminal row", firstUpdatedAt, secondUpdatedAt)
	}
}

// TestReapOrphanedAppHostnamesSkipsBlindEnvironment covers the safety guard:
// an environment with NO live App resource_snapshot at all (the snapshot
// pipeline for it is either dead or the app really was the only one and is
// now gone) must not have its orphaned hostname reaped -- there is nothing to
// prove the snapshot data can be trusted, so this pass must stay hands-off
// rather than gamble on an empty result meaning "no apps" instead of "blind".
func TestReapOrphanedAppHostnamesSkipsBlindEnvironment(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	deletedApp := "gone-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, deletedApp, deletedApp+".dada-tuda.ru", "active", "active", nil)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "active" {
		t.Fatalf("status = %q, want unchanged active (blind environment must not be reaped)", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched)", *reason)
	}
}

// TestReapOrphanedAppHostnamesSkipsStaleEnvironment is
// TestReapOrphanedAppHostnamesSkipsBlindEnvironment's sibling: the environment
// DOES have an App resource_snapshot, but it fell outside
// hostnameReapEnvFreshnessWindow, so the snapshot pipeline for this
// environment cannot be trusted as currently alive either.
func TestReapOrphanedAppHostnamesSkipsStaleEnvironment(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	staleApp := "stale-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, staleApp, `{"port":8080}`)
	if _, err := pool.Exec(ctx,
		`UPDATE resource_snapshots SET last_synced_at = now() - interval '1 hour'
		  WHERE environment_id = $1 AND name = $2`,
		envID, staleApp,
	); err != nil {
		t.Fatalf("backdate resource_snapshots.last_synced_at: %v", err)
	}

	deletedApp := "gone-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, deletedApp, deletedApp+".dada-tuda.ru", "failed", "failed", nil)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want unchanged failed", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched, stale environment must not be reaped)", *reason)
	}
}

// seedAppOperation inserts one operations row for the deletion-evidence arm of
// ReapOrphanedAppHostnames. createdAgo backdates created_at so a test can order
// a DeleteApp and a later CreateApp for the same app name without relying on
// insert order resolution at the same timestamp.
func seedAppOperation(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, action, status string, createdAgo time.Duration) {
	t.Helper()
	userID := seedUser(t, pool)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, created_at)
		 VALUES ($1, $2, $3, $4, 'App', $5, $6, now() - $7::interval)`,
		userID, projectID, envID, action, appName, status, createdAgo.String(),
	); err != nil {
		t.Fatalf("seed %s operation: %v", action, err)
	}
}

// TestReapOrphanedAppHostnamesDemotesOnDeleteOperationWhenBlind is the second
// proof arm: the environment has no App snapshot at all (the deleted app was
// its only one, so the freshness guard can never be satisfied), but a committed
// DeleteApp operation records the deletion directly. That evidence cannot be
// faked by a wedged snapshot pipeline, so the row is reaped.
func TestReapOrphanedAppHostnamesDemotesOnDeleteOperationWhenBlind(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	deletedApp := "gone-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, deletedApp, deletedApp+".dada-tuda.ru", "active", "active", nil)
	seedAppOperation(t, pool, projectID, envID, deletedApp, "DeleteApp", string(models.OperationStatusCommitted), time.Hour)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, certStatus, reason, reattachCount, opID, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "failed" || certStatus != "failed" {
		t.Fatalf("status/cert_status = %q/%q, want failed/failed", status, certStatus)
	}
	if reason == nil || *reason != hostnameReasonAppDeleted {
		t.Fatalf("status_reason = %v, want %q", reason, hostnameReasonAppDeleted)
	}
	if reattachCount != 0 || opID != nil {
		t.Fatalf("reattach_count/operation_id = %d/%v, want 0/nil", reattachCount, opID)
	}
}

// TestReapOrphanedAppHostnamesSkipsRecreatedAppName is the name-reuse guard: an
// app deleted and then created again under the same name in the same
// environment leaves a stale DeleteApp row behind. Acting on it would demote a
// live app's hostname, so a newer CreateApp operation disqualifies the
// deletion evidence and the pass falls back to needing snapshot proof, which a
// blind environment cannot give.
func TestReapOrphanedAppHostnamesSkipsRecreatedAppName(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "reused-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, appName, appName+".dada-tuda.ru", "active", "active", nil)
	seedAppOperation(t, pool, projectID, envID, appName, "DeleteApp", string(models.OperationStatusCommitted), 2*time.Hour)
	seedAppOperation(t, pool, projectID, envID, appName, "CreateApp", string(models.OperationStatusCommitted), time.Hour)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "active" {
		t.Fatalf("status = %q, want unchanged active (app name was recreated)", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched)", *reason)
	}
}

// TestReapOrphanedAppHostnamesSkipsUncommittedDeleteOperation requires the
// deletion evidence to be committed: a DeleteApp operation still in flight (or
// one that failed) proves an intent, not an outcome, and the app may well still
// be serving traffic under that hostname.
func TestReapOrphanedAppHostnamesSkipsUncommittedDeleteOperation(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "inflight-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, appName, appName+".dada-tuda.ru", "active", "active", nil)
	seedAppOperation(t, pool, projectID, envID, appName, "DeleteApp", string(models.OperationStatusCreated), time.Minute)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "active" {
		t.Fatalf("status = %q, want unchanged active (delete not committed)", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched)", *reason)
	}
}

// TestReapOrphanedAppHostnamesIgnoresDeleteOperationInOtherEnvironment pins the
// join key: a committed DeleteApp for the same app NAME in a different
// environment says nothing about this environment's hostname. Without the
// environment_id predicate the two would be indistinguishable, since app names
// are only unique per environment.
func TestReapOrphanedAppHostnamesIgnoresDeleteOperationInOtherEnvironment(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	otherProjectID, otherEnvID := seedReattachProjectEnv(t, pool)
	appName := "shared-" + uuid.NewString()[:8]
	hostnameID := seedOrphanHostname(t, pool, projectID, envID, appName, appName+".dada-tuda.ru", "active", "active", nil)
	seedAppOperation(t, pool, otherProjectID, otherEnvID, appName, "DeleteApp", string(models.OperationStatusCommitted), time.Hour)

	if err := ReapOrphanedAppHostnames(ctx, pool); err != nil {
		t.Fatalf("ReapOrphanedAppHostnames: %v", err)
	}

	status, _, reason, _, _, _ := readReapedHostnameRow(t, pool, hostnameID)
	if status != "active" {
		t.Fatalf("status = %q, want unchanged active (deletion belongs to another environment)", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %v, want nil (untouched)", *reason)
	}
}

// TestOverviewDomainIssuesExcludesAppDeletedButKeepsRealIssues covers gate (1)
// end to end: after ReapOrphanedAppHostnames demotes an orphaned row, the
// admin broken-state panel must not report it, while a genuinely broken row
// under a live app (attach_timeout) must still be reported.
func TestOverviewDomainIssuesExcludesAppDeletedButKeepsRealIssues(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "reap-issues-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	appDeletedReason := hostnameReasonAppDeleted
	deletedHostname := "deleted-" + suffix + ".test"
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, status_reason)
		 VALUES ($1, 'gone-app', $2, 'CNAME', 'failed', 'failed', $3)`,
		envID, deletedHostname, appDeletedReason,
	); err != nil {
		t.Fatalf("seed app_deleted hostname: %v", err)
	}

	attachTimeoutReason := hostnameReasonAttachTimeout
	brokenHostname := "broken-" + suffix + ".test"
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, status_reason)
		 VALUES ($1, 'live-app', $2, 'CNAME', 'failed', 'failed', $3)`,
		envID, brokenHostname, attachTimeoutReason,
	); err != nil {
		t.Fatalf("seed attach_timeout hostname: %v", err)
	}

	issues, err := h.overviewDomainIssues(context.Background())
	if err != nil {
		t.Fatalf("overviewDomainIssues: %v", err)
	}
	byHostname := map[string]overviewDomainIssue{}
	for _, i := range issues {
		byHostname[i.Hostname] = i
	}
	if _, ok := byHostname[deletedHostname]; ok {
		t.Fatal("an app_deleted hostname must not be reported as a broken-panel issue")
	}
	if _, ok := byHostname[brokenHostname]; !ok {
		t.Fatal("a genuinely stuck (attach_timeout) hostname must still be reported")
	}
}
