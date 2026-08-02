package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// TestApplyResourceEnvelopeEnqueuesAResizeNotADeploy pins the decision that made
// the autoscaler able to act at all.
//
// The snapshot it works from is the git-sourced kind: no profile, no resources,
// no env, no volumes -- the shape most apps on this cluster actually have. A
// deploy re-rendered from that snapshot deletes everything it does not contain,
// which is why the agent's clobber guard refuses it and why every resize the
// watcher attempted on such an app failed. ResizeApp carries the numbers and
// nothing else, so the agent can patch the file in git instead of regenerating
// it.
func TestApplyResourceEnvelopeEnqueuesAResizeNotADeploy(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	projectID, envID := seedOptimisticFixture(t, pool)
	appName := "gateway"

	gitSourced, err := json.Marshal(map[string]any{
		"app_name":    appName,
		"image":       "nexus.dada-tuda.ru/dada/gateway-service:develop-18",
		"status":      "Ready",
		"message":     "Synced from git",
		"live_source": "k8s",
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4, NOW())`,
		projectID, envID, appName, gitSourced); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	h := &Handler{pool: pool}
	to := resourceEnvelope{
		CPULimit: "1", MemoryLimit: "1Gi",
		CPUReq: "200m", MemoryReq: "512Mi",
		EphemeralLimit: "1Gi",
	}
	opID, err := h.applyResourceEnvelope(ctx, projectID, envID, appName, to)
	if err != nil {
		t.Fatalf("applyResourceEnvelope: %v", err)
	}

	var action string
	var payloadRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT action, payload FROM operations WHERE id = $1`, opID,
	).Scan(&action, &payloadRaw); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if action != "ResizeApp" {
		t.Errorf("action = %q, want ResizeApp", action)
	}

	var payload models.ResizeAppPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.AppName != appName {
		t.Errorf("payload.app_name = %q, want %q", payload.AppName, appName)
	}
	want := models.AppResourceEnvelope{
		CPURequest: "200m", MemoryRequest: "512Mi",
		CPULimit: "1", MemoryLimit: "1Gi",
		EphemeralLimit: "1Gi",
	}
	if payload.Resources != want {
		t.Errorf("payload.resources = %+v, want %+v", payload.Resources, want)
	}
}

// TestApplyResourceEnvelopeWritesTheEnvelopeIntoTheSnapshot keeps the operation
// and the snapshot in step: a later full re-render reads the snapshot, and one
// that still carries the old size would quietly undo the resize.
func TestApplyResourceEnvelopeWritesTheEnvelopeIntoTheSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	projectID, envID := seedOptimisticFixture(t, pool)
	appName := "web-" + uuid.NewString()[:8]

	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', '{"image":"registry/web:1"}'::jsonb, NOW())`,
		projectID, envID, appName); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	h := &Handler{pool: pool}
	to := resourceEnvelope{CPULimit: "2", MemoryLimit: "2Gi", CPUReq: "500m", MemoryReq: "1Gi"}
	if _, err := h.applyResourceEnvelope(ctx, projectID, envID, appName, to); err != nil {
		t.Fatalf("applyResourceEnvelope: %v", err)
	}

	var summaryRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&summaryRaw); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var cur struct {
		Image     string                     `json:"image"`
		Resources models.AppResourceEnvelope `json:"resources"`
	}
	if err := json.Unmarshal(summaryRaw, &cur); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if cur.Image != "registry/web:1" {
		t.Errorf("image = %q, want it left alone", cur.Image)
	}
	if cur.Resources.CPULimit != "2" || cur.Resources.MemoryLimit != "2Gi" {
		t.Errorf("snapshot resources = %+v, want the new envelope", cur.Resources)
	}
}
