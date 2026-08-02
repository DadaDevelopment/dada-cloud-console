package api

import (
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
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
