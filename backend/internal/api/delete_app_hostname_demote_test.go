package api

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ginParams builds the path params DeleteApp reads: projectId, envId,
// appName.
func ginParams(projectID, envID uuid.UUID, appName string) gin.Params {
	return gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
}

// readDemotedHostnameRow reads back status, reattach_count and operation_id
// only -- unlike readHostnameRow (hostname_reattach_test.go) it does not
// require attach_started_at to be set, which demoteAppHostnames deliberately
// leaves untouched on a row it only demotes and never re-drives itself.
func readDemotedHostnameRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status string, reattachCount int, opID *uuid.UUID) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, reattach_count, operation_id FROM domain_hostnames WHERE id = $1`, id,
	).Scan(&status, &reattachCount, &opID); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	return
}

// TestDeleteAppDemotesActiveHostname pins the fix for the ggrk52.ru shape
// (project memory project_deleteapp_orphans_domain_row_under_live_app.md):
// DeleteApp used to leave a domain_hostnames row untouched while the gitops
// path removed the app's rendered Ingress underneath it, so the row stayed
// 'active' forever pointing at nothing. DeleteApp must now demote the row to
// 'failed' -- the state ReattachOrphanedHostnames already knows how to heal --
// rather than delete it, so the user's custom-domain authorization survives.
func TestDeleteAppDemotesActiveHostname(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "magic-mirror-" + uuid.NewString()[:8]
	hostname := appName + ".dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)

	var hostnameID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES ($1, $2, $3, 'CNAME', 'active', 'active', true)
		 RETURNING id`,
		envID, appName, hostname,
	).Scan(&hostnameID); err != nil {
		t.Fatalf("seed active hostname: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newCreateCtx(t, "", ginParams(projectID, envID, appName), claims)
	h.DeleteApp(c)
	if rec.Code != 202 {
		t.Fatalf("DeleteApp status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	status, reattachCount, opID := readDemotedHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (demoted, not left active)", status)
	}
	if reattachCount != 0 {
		t.Fatalf("reattach_count = %d, want reset to 0", reattachCount)
	}
	if opID != nil {
		t.Fatalf("operation_id = %v, want cleared (stale operation from the deleted app's life)", *opID)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM domain_hostnames WHERE id = $1`, hostnameID).Scan(&count); err != nil {
		t.Fatalf("count row: %v", err)
	}
	if count != 1 {
		t.Fatalf("hostname row deleted, want it kept (custom-domain authorization must survive)")
	}
}

// TestDeleteAppDemotesPendingHostname covers the other pre-delete state a
// hostname can be in: 'pending' (attach still in flight when the app was
// deleted) must also be demoted to 'failed', not left to time out on its own
// or silently deleted.
func TestDeleteAppDemotesPendingHostname(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "pending-app-" + uuid.NewString()[:8]
	hostname := appName + ".dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)

	var hostnameID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES ($1, $2, $3, 'CNAME', 'pending', 'pending', true)
		 RETURNING id`,
		envID, appName, hostname,
	).Scan(&hostnameID); err != nil {
		t.Fatalf("seed pending hostname: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newCreateCtx(t, "", ginParams(projectID, envID, appName), claims)
	h.DeleteApp(c)
	if rec.Code != 202 {
		t.Fatalf("DeleteApp status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	status, _, _ := readDemotedHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (pending row must also be demoted)", status)
	}
}

// TestDeleteAppLeavesOtherAppsHostnameAlone guards the WHERE clause: a
// hostname bound to a different app_name in the same environment must not be
// touched by an unrelated app's DeleteApp.
func TestDeleteAppLeavesOtherAppsHostnameAlone(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)

	victimApp := "victim-" + uuid.NewString()[:8]
	bystanderApp := "bystander-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, victimApp, `{"port":8080}`)
	seedReattachApp(t, pool, projectID, envID, bystanderApp, `{"port":8080}`)

	var bystanderHostnameID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES ($1, $2, $3, 'CNAME', 'active', 'active', true)
		 RETURNING id`,
		envID, bystanderApp, bystanderApp+".dada-tuda.ru",
	).Scan(&bystanderHostnameID); err != nil {
		t.Fatalf("seed bystander hostname: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newCreateCtx(t, "", ginParams(projectID, envID, victimApp), claims)
	h.DeleteApp(c)
	if rec.Code != 202 {
		t.Fatalf("DeleteApp status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	status, _, _ := readDemotedHostnameRow(t, pool, bystanderHostnameID)
	if status != "active" {
		t.Fatalf("bystander app hostname status = %q, want unchanged active", status)
	}
}

// TestDeleteAppThenRecreateGetsReattached is the end-to-end shape: after
// DeleteApp demotes the row, the app reappears under the same name (a real
// recreate would show up as a fresh App resource_snapshot) and
// ReattachOrphanedHostnames must pick the demoted row back up and drive it
// toward attach again -- the whole point of demoting instead of deleting.
func TestDeleteAppThenRecreateGetsReattached(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "recreate-" + uuid.NewString()[:8]
	hostname := appName + ".dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)

	var hostnameID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES ($1, $2, $3, 'CNAME', 'active', 'active', true)
		 RETURNING id`,
		envID, appName, hostname,
	).Scan(&hostnameID); err != nil {
		t.Fatalf("seed active hostname: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newCreateCtx(t, "", ginParams(projectID, envID, appName), claims)
	h.DeleteApp(c)
	if rec.Code != 202 {
		t.Fatalf("DeleteApp status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET updated_at = now() - interval '7 hours' WHERE id = $1`,
		hostnameID,
	); err != nil {
		t.Fatalf("backdate updated_at past the reattach cooldown: %v", err)
	}

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, opID := readHostnameRow(t, pool, hostnameID)
	if status != "pending" {
		t.Fatalf("status after recreate + reattach tick = %q, want pending (demoted row must be healable)", status)
	}
	if reattachCount != 1 {
		t.Fatalf("reattach_count = %d, want 1", reattachCount)
	}
	if opID == nil {
		t.Fatalf("operation_id is nil, want a new AttachDefaultDomain operation")
	}
}
