package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Failure capture for the database endpoints, against a real database.
//
// These three handlers wrote their audit rows with a raw INSERT that ran only
// on the happy path, so every refusal was invisible: a user who tried to create
// a database and was bounced on a bad name, and a user who never tried at all,
// left the same trace — none. The path analysis reads that as "nobody wanted a
// database", which is the opposite of what happened.
//
// The credential reveal matters most: a run of refusals against one database is
// the shape a probe makes, and it was the one read with no record of being
// refused.
func lastAuditRow(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, action string) (outcome, reason string, envID *uuid.UUID) {
	t.Helper()
	var meta map[string]any
	var env *uuid.UUID
	err := pool.QueryRow(context.Background(),
		`SELECT outcome, metadata, environment_id FROM audit_events
		  WHERE project_id = $1 AND action = $2
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, action,
	).Scan(&outcome, &meta, &env)
	if err != nil {
		t.Fatalf("expected a %s audit row, got error: %v", action, err)
	}
	r, _ := meta["reason"].(string)
	return outcome, r, env
}

func routeDatabaseCall(t *testing.T, method, route, path, body string, claims *auth.Claims, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, route, func(c *gin.Context) {
		if claims != nil {
			auth.SetClaims(c, claims)
		}
		handler(c)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateServiceDatabase_RejectionIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/databases"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/environments/:envId/databases", path,
		`{"name":"Not_A_Kube_Name","database":"app"}`, godClaims(userID), h.CreateServiceDatabase)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, gotEnv := lastAuditRow(t, pool, projectID, "CreateServiceDatabase")
	if outcome != auditOutcomeFailure {
		t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
	}
	if reason != "invalid_name" {
		t.Errorf("reason = %q, want invalid_name", reason)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}

func TestDeleteServiceDatabase_UnknownNameIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/databases/ghost"
	rec := routeDatabaseCall(t, http.MethodDelete,
		"/projects/:projectId/environments/:envId/databases/:name", path,
		"", godClaims(userID), h.DeleteServiceDatabase)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "DeleteServiceDatabase")
	if outcome != auditOutcomeFailure || reason != "not_found" {
		t.Errorf("audit row = (%q, %q), want (failure, not_found)", outcome, reason)
	}
}

// A reveal without the explicit reveal=true confirmation is the cheapest probe
// there is, and it used to leave no trace at all.
func TestGetDatabaseCredentials_RefusalIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/databases/pg/credentials"
	rec := routeDatabaseCall(t, http.MethodGet,
		"/projects/:projectId/environments/:envId/databases/:name/credentials", path,
		"", godClaims(userID), h.GetDatabaseCredentials)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, auditActionRevealDBCreds)
	if outcome != auditOutcomeFailure || reason != "reveal_not_confirmed" {
		t.Errorf("audit row = (%q, %q), want (failure, reveal_not_confirmed)", outcome, reason)
	}
}
