package api

import (
	"context"
	"testing"
)

// TestCurrentShardOfPrefersTheMoveOverTheSnapshot pins the precedence a move
// must start from. The CR snapshot keeps naming the shard the data left until
// Crossplane reconciles; a move started from that name would replicate out of
// an instance no client is being sent to any more, and the copy it produced
// would be missing every write made after the earlier cutover.
func TestCurrentShardOfPrefersTheMoveOverTheSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	h := &Handler{pool: pool}

	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO db_moves (datname, owner_role, source_shard, target_shard, phase)
		 VALUES ($1, 'svc-test', 'shard-1', 'shard-0', 'done') RETURNING id::text`,
		"placement-precedence-test",
	).Scan(&id); err != nil {
		t.Fatalf("seed move: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM db_moves WHERE id = $1::uuid`, id); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	shard, err := h.currentShardOf(ctx, "placement-precedence-test")
	if err != nil {
		t.Fatalf("currentShardOf: %v", err)
	}
	if shard != "shard-0" {
		t.Fatalf("currentShardOf = %q, want shard-0: the finished move outranks the CR", shard)
	}
}

// TestCurrentShardOfRefusesADatabaseItCannotPlace covers the case that used to
// be typed in by hand: a database with neither a CR snapshot nor a finished
// move has no known placement, and guessing one would point the schema copy at
// an arbitrary instance.
func TestCurrentShardOfRefusesADatabaseItCannotPlace(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}

	if _, err := h.currentShardOf(context.Background(), "no-such-database-anywhere"); err == nil {
		t.Fatal("currentShardOf returned a shard for a database with no placement at all")
	}
}
