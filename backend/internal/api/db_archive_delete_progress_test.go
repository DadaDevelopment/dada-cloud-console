package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// seedArchiveRun stores a run in the delete phase and returns its id.
func seedArchiveRun(t *testing.T, h *Handler, deleted int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := h.pool.QueryRow(ctx,
		`INSERT INTO db_archive_runs
		   (project_id, environment_id, resource_name, datname, shard,
		    table_name, cutoff_column, cutoff_date, phase, planned_rows, deleted_rows)
		 VALUES ($1::uuid, $2::uuid, 'rig-db', 'rig-db', 'shard-0',
		         'observations', 'created_at', DATE '2026-08-01', 'delete', 100, $3)
		 RETURNING id`,
		uuid.NewString(), uuid.NewString(), deleted,
	).Scan(&id); err != nil {
		t.Fatalf("seed archive run: %v", err)
	}
	t.Cleanup(func() {
		if _, err := h.pool.Exec(context.Background(),
			`DELETE FROM db_archive_runs WHERE id = $1`, id); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return id
}

func archiveRunDeleted(t *testing.T, h *Handler, id uuid.UUID) int64 {
	t.Helper()
	var got int64
	if err := h.pool.QueryRow(context.Background(),
		`SELECT deleted_rows FROM db_archive_runs WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("read deleted_rows: %v", err)
	}
	return got
}

// TestDeleteProgressSurvivesTheDeathOfItsOwnTick is the counter defect seen in
// production: a console rollout killed the worker mid-delete, and the run went
// on reporting the count the previous tick had left behind while millions of
// rows had already been committed away. The rows were gone; only the number
// was wrong, which is worse than useless on a run an owner is watching to
// decide whether their database is being freed at all.
//
// The write must therefore not travel on the context that has just been
// cancelled - that context is cancelled in exactly the case where saving the
// number matters most.
func TestDeleteProgressSurvivesTheDeathOfItsOwnTick(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	w := &dbArchiveWorker{h: h}

	id := seedArchiveRun(t, h, 2_360_000)
	run := archiveRun{ID: id}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := w.recordDeletedErr(ctx, run, 4_180_000); err != nil {
		t.Fatalf("progress write refused after its context died: %v", err)
	}
	if got := archiveRunDeleted(t, h, id); got != 4_180_000 {
		t.Fatalf("deleted_rows = %d after a cancelled tick, want 4180000: the rows are gone from the table but the run still reports the previous tick", got)
	}
}

// TestDeleteProgressStoresTheRunningTotal pins what the delete loop writes
// after every batch: the total deleted so far, not the size of the last batch.
// The loop calls this once per batch, so a writer that added instead of
// replacing would multiply the count on a run that takes many ticks.
func TestDeleteProgressStoresTheRunningTotal(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	w := &dbArchiveWorker{h: h}

	id := seedArchiveRun(t, h, 0)
	run := archiveRun{ID: id}
	ctx := context.Background()

	for _, want := range []int64{20_000, 40_000, 60_000} {
		w.recordDeleted(ctx, run, want)
		if got := archiveRunDeleted(t, h, id); got != want {
			t.Fatalf("deleted_rows = %d after a batch of %d", got, want)
		}
	}
}
