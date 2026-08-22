package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// capturingQuerier records the payload an operation was queued with, which is
// the only part of a dry run a unit test can see: the plan itself is produced
// by gitops-agent from git.
type capturingQuerier struct{ payload []byte }

func (q *capturingQuerier) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	for _, a := range args {
		if b, ok := a.([]byte); ok {
			q.payload = b
		}
	}
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return pgx.ErrNoRows }

func queuedPayload(t *testing.T, payload models.DeployImageVersionPayload) models.DeployImageVersionPayload {
	t.Helper()
	q := &capturingQuerier{}
	_, _ = enqueueDeployOp(context.Background(), q, uuid.New(), uuid.New(), uuid.New(), payload)
	if q.payload == nil {
		t.Fatal("no payload reached the insert")
	}
	var got models.DeployImageVersionPayload
	if err := json.Unmarshal(q.payload, &got); err != nil {
		t.Fatalf("payload is not a DeployImageVersionPayload: %v", err)
	}
	return got
}

func TestEnqueueDeployOp_CarriesTheDryRunAskWithoutCarryingValues(t *testing.T) {
	got := queuedPayload(t, envPlanPayload("telemost-bot", "ghcr.io/x:1",
		[]string{"PGHOST"}, []string{"OLD"}, nil))

	if !got.DryRun {
		t.Error("the operation does not declare itself a dry run, so the worker would deploy it for real")
	}
	if len(got.DryRunSetKeys) != 1 || got.DryRunSetKeys[0] != "PGHOST" {
		t.Errorf("the key being written did not reach the render: %v", got.DryRunSetKeys)
	}
	if len(got.DryRunUnsetKeys) != 1 || got.DryRunUnsetKeys[0] != "OLD" {
		t.Errorf("the key being deleted did not reach the render: %v", got.DryRunUnsetKeys)
	}
	if got.Image != "ghcr.io/x:1" {
		t.Errorf("a dry run of an env write must plan against the image the app is running, got %q", got.Image)
	}

	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("s3cret")) {
		t.Error("a value reached the plaintext operation payload")
	}
}

func TestEnqueueRedeployOp_IsNotADryRun(t *testing.T) {
	q := &capturingQuerier{}
	_, _ = enqueueRedeployOp(context.Background(), q, uuid.New(), uuid.New(), uuid.New(), "bot", "ghcr.io/x:1")
	var got models.DeployImageVersionPayload
	if err := json.Unmarshal(q.payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.DryRun || got.DryRunSetKeys != nil || got.DryRunUnsetKeys != nil {
		t.Errorf("an ordinary redeploy was marked as a dry run and would write nothing: %+v", got)
	}
	if got.AppName != "bot" || got.Image != "ghcr.io/x:1" {
		t.Errorf("redeploy payload lost its target: %+v", got)
	}
}

func TestDryRunRequested_OnlyAnExplicitYesTurnsAWriteIntoAQuestion(t *testing.T) {
	for _, yes := range []string{"true", "TRUE", "1", " yes "} {
		if !dryRunRequested(yes) {
			t.Errorf("dry_run=%q was treated as a real write", yes)
		}
	}
	for _, no := range []string{"", "false", "0", "no", "dry"} {
		if dryRunRequested(no) {
			t.Errorf("dry_run=%q silently turned a write into a no-op", no)
		}
	}
}
