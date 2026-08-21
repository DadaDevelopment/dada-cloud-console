package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestUpdateAppPort_ValidPort_WritesSnapshotAndQueuesRedeploy is the
// regression gate for the affiliate-site class of bug: an app whose
// framework autodetect picked the wrong default port (targetPort 4173
// against a process on 3000) is permanently 502ing and, before this
// endpoint, had no lever to fix inside the product. A valid port must land
// in resource_snapshots.summary_json (what gitops-agent's
// deployPortAndWorker reads into renderer.AppSpec.Port) and, unlike
// start-command's opt-in redeploy, must always queue a DeployImageVersion
// operation carrying the app's CURRENT image so the fix reaches the running
// pods without a separate manual redeploy step.
func TestUpdateAppPort_ValidPort_WritesSnapshotAndQueuesRedeploy(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "affiliate-" + uuid.NewString()[:8]
	image := "nexus.example/proj/" + appName + "@sha256:" +
		"4444444444444444444444444444444444444444444444444444444444444444"
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	c, rec := newCreateCtx(t, `{"port":3000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.UpdateAppPort(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Port      int `json:"port"`
		Operation *struct {
			Action  string `json:"action"`
			Payload struct {
				Image string `json:"image"`
			} `json:"payload"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if resp.Port != 3000 {
		t.Fatalf("response port = %d, want 3000", resp.Port)
	}
	if resp.Operation == nil {
		t.Fatalf("no operation queued; the app keeps sending traffic to the wrong port until an unrelated redeploy happens. body=%s", rec.Body.String())
	}
	if resp.Operation.Action != "DeployImageVersion" {
		t.Fatalf("operation action = %q, want DeployImageVersion", resp.Operation.Action)
	}
	if resp.Operation.Payload.Image != image {
		t.Fatalf("queued redeploy image = %q, want the app's current image %q", resp.Operation.Payload.Image, image)
	}

	var portVal int
	if err := pool.QueryRow(context.Background(),
		`SELECT (summary_json->>'port')::int FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&portVal); err != nil {
		t.Fatalf("read back snapshot: %v", err)
	}
	if portVal != 3000 {
		t.Fatalf("snapshot port = %d, want 3000", portVal)
	}
}

// TestUpdateAppPort_OutOfRange_Rejected pins the validation contract the
// frontend branches on by err.code, not error prose: an out-of-range port
// must fail with 400 and code "invalid_port", and must not touch the
// snapshot.
func TestUpdateAppPort_OutOfRange_Rejected(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "badport-" + uuid.NewString()[:8]
	image := "nexus.example/proj/" + appName + "@sha256:" +
		"5555555555555555555555555555555555555555555555555555555555555555"
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	c, rec := newCreateCtx(t, `{"port":70000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.UpdateAppPort(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if resp.Code != "invalid_port" {
		t.Fatalf("error code = %q, want invalid_port; body=%s", resp.Code, rec.Body.String())
	}

	var opCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations WHERE project_id = $1 AND environment_id = $2 AND resource_name = $3 AND action = 'DeployImageVersion'`,
		projectID, envID, appName,
	).Scan(&opCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if opCount != 0 {
		t.Fatalf("operations row count = %d, want 0 (rejected request must not queue a redeploy)", opCount)
	}
}
