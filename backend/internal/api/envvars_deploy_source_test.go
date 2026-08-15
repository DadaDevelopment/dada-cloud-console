package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestQueueEnvApply_PrefersDeploymentsLedgerOverStaleSnapshot is the
// regression gate for the megafactory incident (2026-08-13/14): the app's
// resource_snapshots cache was frozen on an old image digest after a real
// deploy had already moved it forward, and every env-var save silently
// redeployed that stale digest over the working one. deployments is written
// once, transactionally, by the code paths that actually enact a deploy
// (never by queueEnvApply itself), so its most recent row must win over the
// snapshot cache.
func TestQueueEnvApply_PrefersDeploymentsLedgerOverStaleSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "megafactory-" + uuid.NewString()[:8]
	staleImage := "nexus.example/proj/" + appName + "@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	freshImage := "nexus.example/proj/" + appName + "@sha256:1111111111111111111111111111111111111111111111111111111111111111"

	seedAppWithImage(t, pool, projectID, envID, appName, staleImage)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO deployments (environment_id, app_name, image_uri, trigger, is_current, created_at)
		 VALUES ($1, $2, $3, 'manual', false, now() - interval '1 hour')`,
		envID, appName, staleImage,
	); err != nil {
		t.Fatalf("seed stale deployment: %v", err)
	}
	seedDeployment(t, pool, envID, appName, nil, freshImage)

	c, rec := newCreateCtx(t, `{"value":"1234","is_secret":false,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "BOT_TOKEN"},
		}, claims)
	h.SetEnvVar(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Operation struct {
			Payload struct {
				Image string `json:"image"`
			} `json:"payload"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if resp.Operation.Payload.Image != freshImage {
		t.Fatalf("queued redeploy image = %q, want the fresh deployments-ledger image %q (not the stale snapshot cache %q)",
			resp.Operation.Payload.Image, freshImage, staleImage)
	}
}

// TestQueueEnvApply_FallsBackToSnapshotWhenNoDeploymentsRow covers an app
// deployed only through a direct-image path (UpdateAppImage, the CI
// deploy-hook) that never wrote a deployments row: queueEnvApply must still
// fall back to the resource_snapshots cache exactly as it always has, rather
// than refusing to redeploy at all.
func TestQueueEnvApply_FallsBackToSnapshotWhenNoDeploymentsRow(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "direct-image-" + uuid.NewString()[:8]
	image := "nexus.example/proj/" + appName + "@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	c, rec := newCreateCtx(t, `{"value":"x","is_secret":false,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "FOO"},
		}, claims)
	h.SetEnvVar(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Operation struct {
			Payload struct {
				Image string `json:"image"`
			} `json:"payload"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if resp.Operation.Payload.Image != image {
		t.Fatalf("queued redeploy image = %q, want the snapshot fallback image %q", resp.Operation.Payload.Image, image)
	}
}

// TestQueueEnvApply_NoRedeployWhenNoImageAnywhere covers a bare app with
// neither a deployments row nor a snapshot image (e.g. an upload whose first
// build hasn't finished): saving env vars must not queue a redeploy, since
// there is nothing to deploy and no authoritative image to invent one from.
func TestQueueEnvApply_NoRedeployWhenNoImageAnywhere(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "bare-app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)

	c, rec := newCreateCtx(t, `{"value":"x","is_secret":false,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "FOO"},
		}, claims)
	h.SetEnvVar(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if _, hasOp := resp["operation"]; hasOp {
		t.Fatalf("SetEnvVar queued a redeploy for a bare app with no known image; body=%s", rec.Body.String())
	}
}

// seedAppWithImage inserts an App snapshot carrying summary_json.image, the
// shape queueEnvApply's snapshot fallback reads.
func seedAppWithImage(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, image string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Ready', jsonb_build_object('image', $4::text))`,
		projectID, envID, name, image,
	); err != nil {
		t.Fatalf("seed app with image: %v", err)
	}
}
