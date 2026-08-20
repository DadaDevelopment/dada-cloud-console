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

// TestUpdateAppStartCommand_RedeployTrue_QueuesRedeployWithCurrentImage is the
// regression gate for the crash-banner repair flow: a first-day user who hits
// the "start command" fix on a crashlooping app must not be told the fix
// "worked" while the app keeps running the old, broken command. Passing
// "redeploy": true must queue a DeployImageVersion operation carrying the
// app's CURRENT image (never a rebuild -- see redeployFrom's doc comment;
// gitops-agent's doDeployImageVersion re-reads resource_snapshots at op time,
// which the same transaction already updated with the new start_command --
// see UpdateAppStartCommand's doc comment and dbwatcher.go's
// appSpec.StartCommand assignment).
func TestUpdateAppStartCommand_RedeployTrue_QueuesRedeployWithCurrentImage(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "crashy-" + uuid.NewString()[:8]
	image := "nexus.example/proj/" + appName + "@sha256:" +
		"2222222222222222222222222222222222222222222222222222222222222222"
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	c, rec := newCreateCtx(t, `{"start_command":"python agent.py","redeploy":true}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.UpdateAppStartCommand(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
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
	if resp.Operation == nil {
		t.Fatalf("no operation queued in response; the crash-banner save would report success while the app keeps running the old command. body=%s", rec.Body.String())
	}
	if resp.Operation.Action != "DeployImageVersion" {
		t.Fatalf("operation action = %q, want DeployImageVersion", resp.Operation.Action)
	}
	if resp.Operation.Payload.Image != image {
		t.Fatalf("queued redeploy image = %q, want the app's current image %q (a rebuild is never triggered)", resp.Operation.Payload.Image, image)
	}

	var startCommand string
	if err := pool.QueryRow(context.Background(),
		`SELECT summary_json->>'start_command' FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&startCommand); err != nil {
		t.Fatalf("read back snapshot: %v", err)
	}
	if startCommand != "python agent.py" {
		t.Fatalf("snapshot start_command = %q, want %q", startCommand, "python agent.py")
	}

	var opCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations WHERE project_id = $1 AND environment_id = $2 AND resource_name = $3 AND action = 'DeployImageVersion'`,
		projectID, envID, appName,
	).Scan(&opCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if opCount != 1 {
		t.Fatalf("operations row count = %d, want 1", opCount)
	}
}

// TestUpdateAppStartCommand_RedeployOmitted_DoesNotQueue pins the default
// (plain settings-page editor) behaviour: omitting "redeploy" must still save
// the value without forcing a re-render, exactly as before this feature --
// the UpdateAppProfile incident this endpoint's doc comment warns about
// (2026-08-02, internal/telemost-bot's hand-maintained values.yaml torn apart
// by a config save that always redeployed) must not come back for an
// unrelated start-command edit made outside the crash-repair flow.
func TestUpdateAppStartCommand_RedeployOmitted_DoesNotQueue(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	appName := "quiet-" + uuid.NewString()[:8]
	image := "nexus.example/proj/" + appName + "@sha256:" +
		"3333333333333333333333333333333333333333333333333333333333333333"
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	c, rec := newCreateCtx(t, `{"start_command":"python agent.py"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.UpdateAppStartCommand(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if _, hasOp := resp["operation"]; hasOp {
		t.Fatalf("UpdateAppStartCommand queued a redeploy without redeploy:true; body=%s", rec.Body.String())
	}

	var opCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations WHERE project_id = $1 AND environment_id = $2 AND resource_name = $3 AND action = 'DeployImageVersion'`,
		projectID, envID, appName,
	).Scan(&opCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if opCount != 0 {
		t.Fatalf("operations row count = %d, want 0 (no redeploy without opt-in)", opCount)
	}
}
