package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestNotifyAutofixOutcomeSettlesFeedbackWithoutNotifier(t *testing.T) {
	pool := autofixGuardPool(t)
	feedbackID := uuid.New()
	taskID := uuid.New()
	projectID, envID, _ := seedAutofixTarget(t, pool)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO feedback (id, user_sub, route, message, status, cloud_task_id)
		 VALUES ($1, 'user-1', '/billing', 'cannot pay', 'in_progress', $2)`, feedbackID, taskID); err != nil {
		t.Fatalf("seed feedback: %v", err)
	}

	h := &Handler{pool: pool}
	h.notifyAutofixOutcome(context.Background(), cloudTaskTransition{
		Matched: true,
		ID: taskID,
		ProjectID: projectID,
		EnvironmentID: envID,
		TaskType: "autofix",
		NewStatus: "failed",
		OldStatus: "running",
		Error: "agent unavailable",
	})

	var status, resolution string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, resolution FROM feedback WHERE id=$1`, feedbackID).Scan(&status, &resolution); err != nil {
		t.Fatalf("read feedback: %v", err)
	}
	if status != "new" {
		t.Fatalf("status=%q want new", status)
	}
	if resolution != "auto-fix failed: agent unavailable" {
		t.Fatalf("resolution=%q", resolution)
	}
}
