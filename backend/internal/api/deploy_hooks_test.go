package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/models"
)

func TestGenerateDeployToken_FormatAndHash(t *testing.T) {
	plaintext, hash, prefix, err := generateDeployToken()
	if err != nil {
		t.Fatalf("generateDeployToken: %v", err)
	}
	if !strings.HasPrefix(plaintext, "dadadh_") {
		t.Fatalf("plaintext=%q want dadadh_ prefix", plaintext)
	}
	if len(plaintext) != len("dadadh_")+40 {
		t.Fatalf("plaintext len=%d want %d", len(plaintext), len("dadadh_")+40)
	}
	if prefix != plaintext[:13] {
		t.Fatalf("prefix=%q want first 13 chars of plaintext (%q)", prefix, plaintext[:13])
	}
	if hash != hashDeployToken(plaintext) {
		t.Fatalf("hash=%q does not match hashDeployToken(plaintext)=%q", hash, hashDeployToken(plaintext))
	}
	if hashDeployToken(plaintext) != hashDeployToken(plaintext) {
		t.Fatal("hashDeployToken is not deterministic")
	}

	plaintext2, hash2, _, err := generateDeployToken()
	if err != nil {
		t.Fatalf("generateDeployToken (2nd): %v", err)
	}
	if plaintext2 == plaintext || hash2 == hash {
		t.Fatal("two generateDeployToken calls produced the same token")
	}
}

func TestClassifyOperationStatus(t *testing.T) {
	cases := []struct {
		status            models.OperationStatus
		terminal, success bool
	}{
		{models.OperationStatusCommitted, true, true},
		{models.OperationStatusReady, true, true},
		{models.OperationStatusFailed, true, false},
		{models.OperationStatusCancelled, true, false},
		{models.OperationStatusCreated, false, false},
		{models.OperationStatusSyncing, false, false},
	}
	for _, tc := range cases {
		terminal, success := classifyOperationStatus(tc.status)
		if terminal != tc.terminal || success != tc.success {
			t.Fatalf("classifyOperationStatus(%q) = (%v,%v) want (%v,%v)", tc.status, terminal, success, tc.terminal, tc.success)
		}
	}
}

func testDeployHooksPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping deploy-hooks DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedDeployApp inserts a fresh project/environment/resource_snapshot("App")
// fixture so the deploy-hooks handlers under test see a real app to bind to.
func seedDeployApp(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID, appName string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	appName = "app-" + suffix

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"deploy-hook-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name) VALUES ($1, $2, 'App', $3)`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed resource_snapshot: %v", err)
	}
	return projectID, envID, appName
}

// insertDeployHook inserts an app_deploy_hooks row directly (bypassing the
// CreateDeployHook HTTP handler, which needs full JWT/authz plumbing this
// test package has no harness for) and returns its id and the matching
// plaintext token.
func insertDeployHook(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string, createdBy *uuid.UUID) (hookID uuid.UUID, token string) {
	t.Helper()
	plaintext, hash, prefix, err := generateDeployToken()
	if err != nil {
		t.Fatalf("generateDeployToken: %v", err)
	}
	err = pool.QueryRow(context.Background(),
		`INSERT INTO app_deploy_hooks (project_id, environment_id, app_name, name, token_hash, token_prefix, created_by)
		 VALUES ($1, $2, $3, 'test-hook', $4, $5, $6) RETURNING id`,
		projectID, envID, appName, hash, prefix, createdBy,
	).Scan(&hookID)
	if err != nil {
		t.Fatalf("insert deploy hook: %v", err)
	}
	return hookID, plaintext
}

func newDeployTokenCtx(method, path, token, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c.Request = req
	return c, rec
}

func TestDeployTrigger_HappyPath_EnqueuesOperation(t *testing.T) {
	pool := testDeployHooksPool(t)
	projectID, envID, appName := seedDeployApp(t, pool)
	hookID, token := insertDeployHook(t, pool, projectID, envID, appName, nil)

	h := &Handler{pool: pool}
	c, rec := newDeployTokenCtx(http.MethodPost, "/api/v1/deploy", token, `{"image":"nginx:1.25"}`)
	h.DeployTrigger(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var resp deployTriggerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.OperationID == uuid.Nil {
		t.Fatal("operation_id is nil")
	}

	var action, resourceName string
	var actorID uuid.UUID
	var status string
	err := pool.QueryRow(context.Background(),
		`SELECT action, resource_name, actor_id, status FROM operations WHERE id = $1`, resp.OperationID,
	).Scan(&action, &resourceName, &actorID, &status)
	if err != nil {
		t.Fatalf("query created operation: %v", err)
	}
	if action != "DeployImageVersion" {
		t.Fatalf("action=%q want DeployImageVersion", action)
	}
	if resourceName != appName {
		t.Fatalf("resource_name=%q want %q", resourceName, appName)
	}
	if actorID != systemDeployActorID {
		t.Fatalf("actor_id=%s want system actor %s (hook.created_by was NULL)", actorID, systemDeployActorID)
	}

	var lastUsedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT last_used_at FROM app_deploy_hooks WHERE id = $1`, hookID,
	).Scan(&lastUsedAt); err != nil {
		t.Fatalf("query hook last_used_at: %v", err)
	}
	if lastUsedAt == nil {
		t.Fatal("last_used_at was not updated after a successful trigger")
	}
}

func TestDeployTrigger_RevokedToken_401(t *testing.T) {
	pool := testDeployHooksPool(t)
	projectID, envID, appName := seedDeployApp(t, pool)
	hookID, token := insertDeployHook(t, pool, projectID, envID, appName, nil)

	if _, err := pool.Exec(context.Background(),
		`UPDATE app_deploy_hooks SET revoked_at = now() WHERE id = $1`, hookID,
	); err != nil {
		t.Fatalf("revoke hook: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newDeployTokenCtx(http.MethodPost, "/api/v1/deploy", token, `{"image":"nginx:1.25"}`)
	h.DeployTrigger(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d body=%s want 401 for a revoked token", rec.Code, rec.Body.String())
	}
}

func TestDeployTrigger_UnknownToken_401(t *testing.T) {
	pool := testDeployHooksPool(t)

	h := &Handler{pool: pool}
	c, rec := newDeployTokenCtx(http.MethodPost, "/api/v1/deploy", "dadadh_"+strings.Repeat("0", 40), `{"image":"nginx:1.25"}`)
	h.DeployTrigger(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d body=%s want 401 for an unknown token", rec.Code, rec.Body.String())
	}
}

func TestDeployTrigger_MissingToken_401(t *testing.T) {
	pool := testDeployHooksPool(t)

	h := &Handler{pool: pool}
	c, rec := newDeployTokenCtx(http.MethodPost, "/api/v1/deploy", "", `{"image":"nginx:1.25"}`)
	h.DeployTrigger(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d body=%s want 401 for a missing token", rec.Code, rec.Body.String())
	}
}

func TestGetDeployOperation_CrossAppOperation_404(t *testing.T) {
	pool := testDeployHooksPool(t)

	projectA, envA, appA := seedDeployApp(t, pool)
	_, tokenA := insertDeployHook(t, pool, projectA, envA, appA, nil)

	projectB, envB, appB := seedDeployApp(t, pool)
	_, tokenB := insertDeployHook(t, pool, projectB, envB, appB, nil)

	h := &Handler{pool: pool}

	triggerCtx, triggerRec := newDeployTokenCtx(http.MethodPost, "/api/v1/deploy", tokenB, `{"image":"nginx:1.25"}`)
	h.DeployTrigger(triggerCtx)
	if triggerRec.Code != http.StatusAccepted {
		t.Fatalf("seed trigger for app B failed: code=%d body=%s", triggerRec.Code, triggerRec.Body.String())
	}
	var triggerResp deployTriggerResponse
	if err := json.Unmarshal(triggerRec.Body.Bytes(), &triggerResp); err != nil {
		t.Fatalf("decode seed trigger response: %v", err)
	}
	opBID := triggerResp.OperationID

	crossCtx, crossRec := newDeployTokenCtx(http.MethodGet, "/api/v1/deploy/operations/"+opBID.String(), tokenA, "")
	crossCtx.Params = gin.Params{{Key: "operationId", Value: opBID.String()}}
	h.GetDeployOperation(crossCtx)
	if crossRec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404 when app A's token polls app B's operation", crossRec.Code, crossRec.Body.String())
	}

	ownCtx, ownRec := newDeployTokenCtx(http.MethodGet, "/api/v1/deploy/operations/"+opBID.String(), tokenB, "")
	ownCtx.Params = gin.Params{{Key: "operationId", Value: opBID.String()}}
	h.GetDeployOperation(ownCtx)
	if ownRec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200 when app B's own token polls its own operation", ownRec.Code, ownRec.Body.String())
	}
	var statusResp deployOperationStatusResponse
	if err := json.Unmarshal(ownRec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusResp.Status != string(models.OperationStatusCreated) {
		t.Fatalf("status=%q want %q", statusResp.Status, models.OperationStatusCreated)
	}
	if statusResp.Terminal {
		t.Fatal("freshly created operation reported terminal=true")
	}
}
