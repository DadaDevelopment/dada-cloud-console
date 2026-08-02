package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Three handlers that mutated silently, against a real database.
//
// MoveApp changes which project owns an app, SetNamespacePolicy changes how much
// CPU and memory a namespace may take, AssignPlan changes what an org pays. All
// three wrote an operations row or a billing row and no audit row, so the path
// analysis could not say who asked for the change, and a refusal left nothing at
// all — the same blank as a user who never tried.
func TestMoveApp_SameProjectRejectionIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/whatever/move"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/environments/:envId/apps/:appName/move", path,
		`{"target_project_id":"`+projectID.String()+`"}`, godClaims(userID), h.MoveApp)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, gotEnv := lastAuditRow(t, pool, projectID, "MoveApp")
	if outcome != auditOutcomeFailure || reason != "target_equals_source" {
		t.Errorf("audit row = (%q, %q), want (failure, target_equals_source)", outcome, reason)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}

func TestSetNamespacePolicy_MalformedBodyIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/namespace-policy"
	rec := routeDatabaseCall(t, http.MethodPut,
		"/projects/:projectId/environments/:envId/namespace-policy", path,
		`{`, godClaims(userID), h.SetNamespacePolicy)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "SetNamespacePolicy")
	if outcome != auditOutcomeFailure || reason != "malformed_body" {
		t.Errorf("audit row = (%q, %q), want (failure, malformed_body)", outcome, reason)
	}
}

// A plan key that does not exist is what a mistyped support-driven override
// looks like, and money changes are the ones worth being able to reconstruct.
func TestAssignPlan_UnknownPlanIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/billing/plan"
	rec := routeDatabaseCall(t, http.MethodPut,
		"/projects/:projectId/billing/plan", path,
		`{"plan":"no-such-plan"}`, godClaims(userID), h.AssignPlan)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "AssignPlan")
	if outcome != auditOutcomeFailure || reason != "unknown_plan" {
		t.Errorf("audit row = (%q, %q), want (failure, unknown_plan)", outcome, reason)
	}
}

// The retry button is what a user presses when the platform already failed them
// once, and pressing it left no trace at all -- neither the press nor a refusal.
// That is exactly the moment the path analysis needs to see.
func TestRetryOperation_NonFailedIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	opID := seedOperation(t, pool, userID, projectID, envID, "Created")

	path := "/projects/" + projectID.String() + "/operations/" + opID.String() + "/retry"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/operations/:operationId/retry", path,
		"", godClaims(userID), h.RetryOperation)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "RetryOperation")
	if outcome != auditOutcomeFailure || reason != "not_failed" {
		t.Errorf("audit row = (%q, %q), want (failure, not_failed)", outcome, reason)
	}
}

func TestRetryOperation_SuccessIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	opID := seedOperation(t, pool, userID, projectID, envID, "Failed")

	path := "/projects/" + projectID.String() + "/operations/" + opID.String() + "/retry"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/operations/:operationId/retry", path,
		"", godClaims(userID), h.RetryOperation)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "RetryOperation")
	if outcome != auditOutcomeSuccess || reason != "" {
		t.Errorf("audit row = (%q, %q), want (success, no reason)", outcome, reason)
	}
}

// Auto-fix is the platform's own promise to repair a broken deploy. A launch
// that never reaches DadaAgent looked identical to a user who never asked.
func TestTriggerAutofix_LaunchFailureIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/ghost/autofix"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/environments/:envId/apps/:appName/autofix", path,
		`{"error":"boom"}`, godClaims(userID), h.TriggerAutofix)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, gotEnv := lastAuditRow(t, pool, projectID, "TriggerAutofix")
	if outcome != auditOutcomeFailure || reason != "launch_failed" {
		t.Errorf("audit row = (%q, %q), want (failure, launch_failed)", outcome, reason)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}

// seedOperation inserts an operations row in the given status and returns its id.
func seedOperation(t *testing.T, pool *pgxpool.Pool, actorID, projectID, envID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateApp', 'App', 'seeded', $4, '{}'::jsonb) RETURNING id`,
		actorID, projectID, envID, status,
	).Scan(&id); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return id
}

// A box is where a beginner's first session happens, and its two most useful
// verbs left no trace: publishing a port and attaching a database. A user who
// asked for a URL on a platform with no box runtime, or for a database the
// wired adapter cannot provision, produced the same blank as a user who never
// opened the product.
func TestExposeBox_NoRuntimeIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID := seedBoxFixture(t, pool)

	c, rec := newBoxCtx(t, http.MethodPost, `{"port":8080}`, boxParams(projectID, "ghost-box"), godClaims(userID))
	h.ExposeBox(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	outcome, reason, _ := lastAuditRow(t, pool, projectID, models.ActionExposeBox)
	if outcome != auditOutcomeFailure || reason != "box_runtime_unavailable" {
		t.Errorf("audit row = (%q, %q), want (failure, box_runtime_unavailable)", outcome, reason)
	}
}

func TestExposeBox_UnknownBoxIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}, boxStack: &boxRuntimeStack{}}
	userID := seedUser(t, pool)
	projectID := seedBoxFixture(t, pool)

	c, rec := newBoxCtx(t, http.MethodPost, `{"port":8080}`, boxParams(projectID, "ghost-box"), godClaims(userID))
	h.ExposeBox(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	outcome, reason, _ := lastAuditRow(t, pool, projectID, models.ActionExposeBox)
	if outcome != auditOutcomeFailure || reason != "box_not_found" {
		t.Errorf("audit row = (%q, %q), want (failure, box_not_found)", outcome, reason)
	}
}

// The attach refusal that matters most is the one on a REAL box: the adapter is
// wired, the box is Ready, and the platform still cannot hand over a database.
// That row now carries the box's environment, which is what ties it to the rest
// of the box's lifecycle.
func TestAttachBoxDatabase_NoAttachProviderIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}, boxStack: &boxRuntimeStack{}}
	userID := seedUser(t, pool)
	projectID, boxID, _ := seedBoxWithInstanceRef(t, pool, models.BoxStatusReady)

	var boxName string
	var envID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT name, environment_id FROM boxes WHERE id = $1`, boxID).Scan(&boxName, &envID); err != nil {
		t.Fatalf("read seeded box: %v", err)
	}

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"db"}`, boxParams(projectID, boxName), godClaims(userID))
	h.AttachBoxDatabase(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	outcome, reason, gotEnv := lastAuditRow(t, pool, projectID, models.ActionAttachBoxDatabase)
	if outcome != auditOutcomeFailure || reason != "attach_provider_unavailable" {
		t.Errorf("audit row = (%q, %q), want (failure, attach_provider_unavailable)", outcome, reason)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}
