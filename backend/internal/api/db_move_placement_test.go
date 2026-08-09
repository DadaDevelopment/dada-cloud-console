package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestRecordMovePlacementEnqueuesShardPatch runs the placement write against the
// real schema.
//
// The lookup it performs is plain SQL, so a column that does not exist on
// resource_snapshots is not a compile error: the move still reports success, the
// router override still redirects traffic, and the only symptom is that no
// SetDatabaseShard operation is ever enqueued -- the CR silently keeps naming the
// shard the data left. This test fails on that, which a unit test with a fake
// pool cannot do.
func TestRecordMovePlacementEnqueuesShardPatch(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	projectID, envID := seedOptimisticFixture(t, pool)

	datname := "moved" + uuid.NewString()[:8]
	crName := "db-" + uuid.NewString()[:8]
	summary, err := json.Marshal(map[string]any{
		"spec": map[string]any{"database": datname, "appRef": "some-app"},
	})
	if err != nil {
		t.Fatalf("encode summary: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at, first_seen_at)
		 VALUES ($1, $2, 'ServiceDatabaseV2', $3, 'Ready', $4, NOW(), NOW())`,
		projectID, envID, crName, summary,
	); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	h := &Handler{pool: pool}
	h.recordMovePlacement(ctx, datname, "shard-0")

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM operations
		 WHERE environment_id = $1 AND action = 'SetDatabaseShard' AND resource_name = $2`,
		envID, crName,
	).Scan(&payload); err != nil {
		t.Fatalf("the move enqueued no SetDatabaseShard operation, so the CR keeps the old shard: %v", err)
	}

	var got struct {
		Name   string `json:"name"`
		AppRef string `json:"app_ref"`
		Shard  string `json:"shard"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Name != crName || got.AppRef != "some-app" || got.Shard != "shard-0" {
		t.Fatalf("payload = %+v, want name=%s appRef=some-app shard=shard-0", got, crName)
	}

	h.recordMovePlacement(ctx, datname, "shard-0")
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM operations
		 WHERE environment_id = $1 AND action = 'SetDatabaseShard' AND resource_name = $2`,
		envID, crName,
	).Scan(&count); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if count != 1 {
		t.Fatalf("operations enqueued = %d, want 1: a retried cutover must not queue the same patch twice", count)
	}
}
