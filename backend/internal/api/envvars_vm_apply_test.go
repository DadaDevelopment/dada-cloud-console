package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// markEnvVM flips a seeded environment onto the compose substrate, the runtime
// whose apps are assembled into one per-environment stack.
func markEnvVM(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE environments SET runtime = 'vm' WHERE id = $1`, envID,
	); err != nil {
		t.Fatalf("mark environment vm: %v", err)
	}
}

// TestQueueEnvApply_VMAppliesEnvWithoutReleasingAnImage is the guard for the
// fin-core/findata trap: on the VM substrate the operation's image is not
// re-deployed as-is, it is written into the app's desired snapshot and the whole
// stack is re-assembled from it. An env-apply that carries an image is therefore
// a release of a live customer site as a side effect of saving a variable, and
// the image it would carry is whatever the console last recorded -- which had
// drifted from the tag actually serving traffic.
func TestQueueEnvApply_VMAppliesEnvWithoutReleasingAnImage(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	markEnvVM(t, pool, envID)
	claims := godClaims(seedUser(t, pool))

	appName := "findata-" + uuid.NewString()[:8]
	recordedImage := "nexus.example/proj/" + appName + ":master-1.0.0-44"
	seedAppWithImage(t, pool, projectID, envID, appName, recordedImage)
	seedDeployment(t, pool, envID, appName, nil, recordedImage)

	op := setEnvVarForTest(t, h, projectID, envID, appName, claims)
	if op == nil {
		t.Fatal("SetEnvVar queued nothing for a VM app; the env var never reaches the stack")
	}
	if op.Payload.Image != "" {
		t.Fatalf("env-apply carries image %q; a VM env-apply must release nothing", op.Payload.Image)
	}
}

// TestQueueEnvApply_VMAppliesEnvWithNoDeployHistory covers the adopted app that
// the console never released: resolving an image first skipped the apply
// entirely, so setting an env var on it was a silent no-op.
func TestQueueEnvApply_VMAppliesEnvWithNoDeployHistory(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	markEnvVM(t, pool, envID)
	claims := godClaims(seedUser(t, pool))

	appName := "adopted-" + uuid.NewString()[:8]
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Ready', '{}'::jsonb)`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed adopted app: %v", err)
	}

	if op := setEnvVarForTest(t, h, projectID, envID, appName, claims); op == nil {
		t.Fatal("SetEnvVar queued nothing for an adopted VM app with no deploy history")
	}
}

// queuedOp is the slice of the SetEnvVar response this file asserts on.
type queuedOp struct {
	Payload struct {
		Image string `json:"image"`
	} `json:"payload"`
}

// setEnvVarForTest saves one runtime variable through the handler and returns the
// operation the response carries, or nil when it queued none.
func setEnvVarForTest(t *testing.T, h *Handler, projectID, envID uuid.UUID, appName string, claims *auth.Claims) *queuedOp {
	t.Helper()
	c, rec := newCreateCtx(t, `{"value":"1234","is_secret":false,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "DADA_AUTH_BACKEND"},
		}, claims)
	h.SetEnvVar(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Operation *queuedOp `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	return resp.Operation
}
