package api

import (
	"context"
	"testing"
)

// TestOwnMoveIsMarkedDoneOnTheCopyTheConsoleStillReads covers the split
// bookkeeping of moving the console's own database: the 'done' row is written
// on the new shard, but every worker tick keeps reading the old copy until the
// DSN is flipped by hand. Without the repeat write that copy still says
// 'cutover', the next tick re-runs a cutover whose subscription is already
// gone, and a finished move ends up recorded as failed.
func TestOwnMoveIsMarkedDoneOnTheCopyTheConsoleStillReads(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()

	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO db_moves (datname, owner_role, source_shard, target_shard, phase)
		 VALUES ($1, 'svc-test', 'shard-1', 'shard-0', 'cutover') RETURNING id::text`,
		"own-move-test",
	).Scan(&id); err != nil {
		t.Fatalf("seed move: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM db_moves WHERE id = $1::uuid`, id); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	w := &dbMoveWorker{h: &Handler{pool: pool}}
	w.markOwnMoveDoneOnOldCopy(ctx, dbMove{ID: id, Datname: "own-move-test"})

	var phase string
	var cutoverAt *string
	if err := pool.QueryRow(ctx,
		`SELECT phase, cutover_at::text FROM db_moves WHERE id = $1::uuid`, id).Scan(&phase, &cutoverAt); err != nil {
		t.Fatalf("read move: %v", err)
	}
	if phase != "done" {
		t.Fatalf("phase = %q, want done: the next tick would re-run a finished cutover", phase)
	}
	if cutoverAt == nil {
		t.Fatal("cutover_at is still null, so the move reads as never having landed")
	}
}
