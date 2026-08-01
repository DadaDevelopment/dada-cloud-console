package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAuditRefusalKeepsOneRowPerReason is the flood guard: the watcher ticks
// every 15 minutes, so an app parked against its namespace quota must leave one
// row per cause, not four an hour. A different cause is a different claim, and
// so is another app.
func TestAuditRefusalKeepsOneRowPerReason(t *testing.T) {
	pool := testAuditPool(t)
	_, projectID := seedAuditActor(t, pool)
	ctx := context.Background()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := &appAutoscaleWatcher{h: &Handler{pool: pool, cache: cache.New(mr.Addr())}}
	defer w.h.cache.Close()

	st := appProfileState{Profile: "small", Image: "img:1"}
	s := starvedPod{Namespace: "ns", Pod: "p-1", Reason: "cpu", Ratio: 0.42}

	w.auditRefusal(ctx, projectID, st, "ns", "app-a", "quota_blocked", s, map[string]any{"detail": "cpu"})
	w.auditRefusal(ctx, projectID, st, "ns", "app-a", "quota_blocked", s, map[string]any{"detail": "cpu"})
	if got := countAutoscaleAudit(t, pool, projectID); got != 1 {
		t.Fatalf("a repeated refusal must claim once, got %d rows", got)
	}

	w.auditRefusal(ctx, projectID, st, "ns", "app-a", "at_ceiling", s, nil)
	if got := countAutoscaleAudit(t, pool, projectID); got != 2 {
		t.Fatalf("a refusal with a new cause is news, got %d rows", got)
	}

	w.auditRefusal(ctx, projectID, st, "ns", "app-b", "quota_blocked", s, nil)
	if got := countAutoscaleAudit(t, pool, projectID); got != 3 {
		t.Fatalf("another app must not be swallowed by the first app's claim, got %d rows", got)
	}
}

// TestAuditRefusalRecordsFailureOutcome pins what the row has to say: support
// reads outcome to tell "the platform never tried" from "the platform tried and
// the quota refused", and the cause lives in metadata.refusal.
func TestAuditRefusalRecordsFailureOutcome(t *testing.T) {
	pool := testAuditPool(t)
	_, projectID := seedAuditActor(t, pool)

	w := &appAutoscaleWatcher{h: &Handler{pool: pool}}
	st := appProfileState{Profile: "medium"}
	s := starvedPod{Namespace: "ns", Pod: "p-9", Reason: "memory", Ratio: 0.99}

	w.auditRefusal(context.Background(), projectID, st, "ns", "app-c", "resize_failed", s, map[string]any{"to_profile": "large"})

	outcome, meta := fetchAutoscaleAudit(t, pool, projectID)
	if outcome != auditOutcomeFailure {
		t.Fatalf("a refusal must be outcome=failure, got %q", outcome)
	}
	if meta["refusal"] != "resize_failed" {
		t.Fatalf("expected the cause in metadata.refusal, got %v", meta["refusal"])
	}
	if meta["from_profile"] != "medium" || meta["to_profile"] != "large" {
		t.Fatalf("the attempted move must survive, got %v -> %v", meta["from_profile"], meta["to_profile"])
	}
	if meta["pressure"] != "memory" {
		t.Fatalf("expected the starvation signal to survive, got %v", meta["pressure"])
	}
}

// TestAuditRefusalWritesWithoutRedis is the fail-open half: a cache outage must
// degrade to duplicate history, never to silence.
func TestAuditRefusalWritesWithoutRedis(t *testing.T) {
	pool := testAuditPool(t)
	_, projectID := seedAuditActor(t, pool)

	w := &appAutoscaleWatcher{h: &Handler{pool: pool}}
	st := appProfileState{Profile: "small"}
	s := starvedPod{Namespace: "ns", Pod: "p-2", Reason: "cpu", Ratio: 0.3}

	w.auditRefusal(context.Background(), projectID, st, "ns", "app-d", "quota_unreadable", s, nil)

	if got := countAutoscaleAudit(t, pool, projectID); got != 1 {
		t.Fatalf("a disabled cache must not gate the row, got %d", got)
	}
}

func countAutoscaleAudit(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE project_id = $1 AND action = $2`,
		projectID, auditActionAutoscaleApp,
	).Scan(&n); err != nil {
		t.Fatalf("count AutoscaleApp rows: %v", err)
	}
	return n
}

func fetchAutoscaleAudit(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) (string, map[string]any) {
	t.Helper()
	var outcome string
	var metaRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT outcome, metadata FROM audit_events WHERE project_id = $1 AND action = $2 ORDER BY created_at DESC LIMIT 1`,
		projectID, auditActionAutoscaleApp,
	).Scan(&outcome, &metaRaw); err != nil {
		t.Fatalf("fetch AutoscaleApp row: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	return outcome, meta
}
