package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newPreviewDeleteCtx(projectID, envID uuid.UUID, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	c.Params = params(projectID, envID)
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

func seedPreviewEnv(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) (envID uuid.UUID, namespace string) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	namespace = "ns-preview-" + suffix
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO environments (project_id, name, namespace, type, is_ephemeral, pr_number, pr_head_branch)
		 VALUES ($1, $2, $3, 'preview', TRUE, 7, 'feature/x') RETURNING id`,
		projectID, "pr-7-"+suffix, namespace,
	).Scan(&envID); err != nil {
		t.Fatalf("seed preview environment: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM environments WHERE id = $1`, envID) })
	return envID, namespace
}

func TestDeletePreviewEnvironment_NonEphemeral_404(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newPreviewDeleteCtx(projectID, envID, claims)
	h := &Handler{pool: pool}
	h.DeletePreviewEnvironment(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404 for a non-ephemeral environment", rec.Code, rec.Body.String())
	}
}

func TestDeletePreviewEnvironment_Ephemeral_202_EnqueuesOperation(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	envID, namespace := seedPreviewEnv(t, pool, projectID)
	claims := godClaims(seedUser(t, pool))

	c, rec := newPreviewDeleteCtx(projectID, envID, claims)
	h := &Handler{pool: pool}
	h.DeletePreviewEnvironment(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202 for an ephemeral environment", rec.Code, rec.Body.String())
	}

	var action, resourceKind string
	var envRef *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT action, resource_kind, environment_id FROM operations
		 WHERE project_id = $1 AND action = 'DeletePreviewEnv' ORDER BY created_at DESC LIMIT 1`,
		projectID,
	).Scan(&action, &resourceKind, &envRef); err != nil {
		t.Fatalf("query created operation: %v", err)
	}
	if action != "DeletePreviewEnv" {
		t.Fatalf("action=%q want DeletePreviewEnv", action)
	}
	if resourceKind != "Environment" {
		t.Fatalf("resource_kind=%q want Environment", resourceKind)
	}
	if envRef == nil || *envRef != envID {
		t.Fatalf("environment_id=%v want %s", envRef, envID)
	}

	var payload models.DeletePreviewEnvPayload
	var payloadRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT payload FROM operations
		 WHERE project_id = $1 AND action = 'DeletePreviewEnv' ORDER BY created_at DESC LIMIT 1`,
		projectID,
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("query operation payload: %v", err)
	}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Namespace != namespace {
		t.Fatalf("payload.Namespace=%q want %q", payload.Namespace, namespace)
	}
	if payload.EnvironmentID != envID.String() {
		t.Fatalf("payload.EnvironmentID=%q want %q", payload.EnvironmentID, envID.String())
	}
}

func TestDeletePreviewEnvironment_ReadOnlyRole_403(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	envID, _ := seedPreviewEnv(t, pool, projectID)

	claims := &auth.Claims{
		UserID: seedUser(t, pool),
		Groups: []string{"/orgs/some-org/projects/" + projectID.String() + "/ReadOnly"},
	}

	c, rec := newPreviewDeleteCtx(projectID, envID, claims)
	h := &Handler{pool: pool}
	h.DeletePreviewEnvironment(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s want 403 for a ReadOnly role", rec.Code, rec.Body.String())
	}
}

func TestDeletePreviewEnvironment_CrossProjectEnv_404(t *testing.T) {
	pool := testOptimisticPool(t)
	projectA, _ := seedOptimisticFixture(t, pool)
	projectB, _ := seedOptimisticFixture(t, pool)
	envIDInB, _ := seedPreviewEnv(t, pool, projectB)
	claims := godClaims(seedUser(t, pool))

	c, rec := newPreviewDeleteCtx(projectA, envIDInB, claims)
	h := &Handler{pool: pool}
	h.DeletePreviewEnvironment(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404 when envId belongs to a different project", rec.Code, rec.Body.String())
	}
}
