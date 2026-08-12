package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func autofixGuardPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping autofix in-flight guard DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAutofixTarget creates the project/environment pair a claim needs, so the
// guard is exercised against real rows rather than invented identifiers.
func seedAutofixTarget(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var actorID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name)
		 VALUES ($1, $2, 'x', $1) RETURNING id`,
		"autofix-guard-"+suffix, "autofix-guard-"+suffix+"@example.invalid").Scan(&actorID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"autofix-guard-"+uuid.NewString()[:8], "org-autofix-guard").Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "autofix-guard-ns-"+uuid.NewString()[:8]).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM cloud_tasks WHERE project_id=$1`, projectID)
		pool.Exec(ctx, `DELETE FROM audit_events WHERE project_id=$1`, projectID)
		pool.Exec(ctx, `DELETE FROM environments WHERE project_id=$1`, projectID)
		pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, actorID)
	})
	return projectID, envID, actorID
}

// TestClaimAutofixRun_SecondClaimIsRejected pins the guard that was missing on
// 2026-08-11, when two clicks six seconds apart launched two parallel runs
// against one app. The claim is a row, not an in-memory lock, so this asserts
// what every backend replica sees: the second INSERT violates
// idx_cloud_tasks_autofix_inflight and exactly one running row survives.
func TestClaimAutofixRun_SecondClaimIsRejected(t *testing.T) {
	pool := autofixGuardPool(t)
	projectID, envID, actorID := seedAutofixTarget(t, pool)
	h := &Handler{pool: pool}
	in := autofixLaunch{ProjectID: projectID, EnvID: envID, AppName: "fonbet-value", ActorID: actorID}

	first, err := h.claimAutofixRun(context.Background(), in)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Status != "running" {
		t.Fatalf("first claim status=%q want running", first.Status)
	}

	if _, err = h.claimAutofixRun(context.Background(), in); err == nil {
		t.Fatal("second claim succeeded; the double click would launch a second parallel run")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("second claim error=%v want unique violation", err)
	}

	var running int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM cloud_tasks WHERE project_id=$1 AND app_name=$2 AND task_type='autofix' AND status='running'`,
		projectID, "fonbet-value").Scan(&running); err != nil {
		t.Fatalf("count running: %v", err)
	}
	if running != 1 {
		t.Fatalf("running autofix rows=%d want 1", running)
	}
}

// TestFailCloudTask_ReleasesTheSlot proves the guard is not a one-way door: a
// claim that never reached a real run must free the app for a genuine retry.
func TestFailCloudTask_ReleasesTheSlot(t *testing.T) {
	pool := autofixGuardPool(t)
	projectID, envID, actorID := seedAutofixTarget(t, pool)
	h := &Handler{pool: pool}
	in := autofixLaunch{ProjectID: projectID, EnvID: envID, AppName: "fonbet-value", ActorID: actorID}

	claim, err := h.claimAutofixRun(context.Background(), in)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	claimID, err := uuid.Parse(claim.ID)
	if err != nil {
		t.Fatalf("parse claim id: %v", err)
	}
	h.failCloudTask(context.Background(), claimID, "no connected git repo for app")

	retry, err := h.claimAutofixRun(context.Background(), in)
	if err != nil {
		t.Fatalf("retry after released claim: %v", err)
	}
	if retry.ID == claim.ID {
		t.Fatal("retry returned the released row instead of a new claim")
	}
}

// TestPlatformCapacityRefusal_ClassifiesTheCause covers the second half of the
// same incident: the app was refused by a platform ceiling no pull request can
// raise. A capacity reason must be recognised, an ambiguous one must not --
// a confident wrong verdict about whose fault it is costs more than silence.
func TestPlatformCapacityRefusal_ClassifiesTheCause(t *testing.T) {
	pool := autofixGuardPool(t)
	projectID, envID, actorID := seedAutofixTarget(t, pool)
	h := &Handler{pool: pool}
	ctx := context.Background()

	writeRefusal := func(t *testing.T, reason string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_events (project_id, environment_id, action, resource_kind, resource_name, outcome, metadata, actor_id)
			 VALUES ($1,$2,$3,'App',$4,$5,jsonb_build_object('refusal',$6::text),$7)`,
			projectID, envID, auditActionAutoscaleApp, "fonbet-value", auditOutcomeFailure, reason, actorID); err != nil {
			t.Fatalf("seed refusal %q: %v", reason, err)
		}
	}

	if _, found := h.platformCapacityRefusal(ctx, projectID, envID, "fonbet-value"); found {
		t.Fatal("no refusal recorded yet, but the cause was reported as platform capacity")
	}

	writeRefusal(t, "limitrange_capped")
	reason, found := h.platformCapacityRefusal(ctx, projectID, envID, "fonbet-value")
	if !found || reason != "limitrange_capped" {
		t.Fatalf("reason=%q found=%v want limitrange_capped/true", reason, found)
	}

	writeRefusal(t, "app_not_ready")
	if reason, found = h.platformCapacityRefusal(ctx, projectID, envID, "fonbet-value"); found {
		t.Fatalf("freshest refusal %q is ambiguous but was reported as platform capacity", reason)
	}
}
