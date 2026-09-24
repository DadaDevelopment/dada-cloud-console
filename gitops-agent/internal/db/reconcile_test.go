package db

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRequeueStrandedOperationOnlyTouchesTheLostCommit replays the operation
// of 2026-09-24: ResizeApp 9e5be247 sat Committed on 25781add, a SHA that only
// existed on the agent's PVC. Once that commit is dropped the operation must go
// back to Created; an operation committed on any other SHA must not move.
func TestRequeueStrandedOperationOnlyTouchesTheLostCommit(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	projectID, envID := seedReleaseProject(t, ctx, pool)

	stranded := seedOperationWithAge(t, ctx, pool, projectID, envID, "stranded-resize", "Committed", time.Minute)
	other := seedOperationWithAge(t, ctx, pool, projectID, envID, "delivered-resize", "Committed", time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = ANY($1)`, []uuid.UUID{stranded, other})
	execReap(t, ctx, pool, `UPDATE operations SET git_commit = '25781addf1627040ad8d82cc499e7ac3cf655ed8' WHERE id = $1`, stranded)
	execReap(t, ctx, pool, `UPDATE operations SET git_commit = '39895d6d2fbf72cc4bc6cf5d5b620135d8547442' WHERE id = $1`, other)

	ok, err := RequeueStrandedOperation(ctx, pool, stranded, "25781addf1627040ad8d82cc499e7ac3cf655ed8")
	if err != nil || !ok {
		t.Fatalf("requeue = %v, %v; want the stranded operation re-queued", ok, err)
	}
	if got := operationStatus(t, ctx, pool, stranded); got != "Created" {
		t.Fatalf("stranded operation is %s, want Created", got)
	}
	if ok, _ := RequeueStrandedOperation(ctx, pool, other, "25781addf1627040ad8d82cc499e7ac3cf655ed8"); ok {
		t.Fatal("an operation committed on a different SHA was re-queued")
	}
	if got := operationStatus(t, ctx, pool, other); got != "Committed" {
		t.Fatalf("delivered operation is %s, want Committed", got)
	}
}

func TestRepointCommitFollowsReplayedSHA(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	projectID, envID := seedReleaseProject(t, ctx, pool)

	op := seedOperationWithAge(t, ctx, pool, projectID, envID, "replayed-resize", "Committed", time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = $1`, op)
	oldSHA, newSHA := uuid.NewString()[:32]+"aaaaaaaa", uuid.NewString()[:32]+"bbbbbbbb"
	execReap(t, ctx, pool, `UPDATE operations SET git_commit = $2 WHERE id = $1`, op, oldSHA)

	n, err := RepointCommit(ctx, pool, oldSHA, newSHA)
	if err != nil || n != 1 {
		t.Fatalf("repoint = %d, %v; want one operation moved", n, err)
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT git_commit FROM operations WHERE id = $1`, op).Scan(&got); err != nil || got != newSHA {
		t.Fatalf("git_commit = %q, %v; want %q", got, err, newSHA)
	}
}

// TestRecordSyncOutcomeCountsARunAndResetsOnSuccess pins the numbers the
// DadaGitopsSyncWedged alert reads: every failed tick grows the run, the last
// error is kept for the page, and one good sync clears it.
func TestRecordSyncOutcomeCountsARunAndResetsOnSuccess(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	repo := "https://example.invalid/" + uuid.NewString() + ".git"
	defer execReap(t, ctx, pool, `DELETE FROM git_sync_state WHERE repo_url = $1`, repo)

	wedge := errors.New("pulling: non-fast-forward update")
	for i := 0; i < 3; i++ {
		if err := RecordSyncOutcome(ctx, pool, repo, "console-migration", wedge); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	var failures int
	var lastErr *string
	var lastSuccess *time.Time
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT consecutive_failures, last_error, last_success_at FROM git_sync_state
			WHERE repo_url = $1 AND branch = 'console-migration'`, repo).Scan(&failures, &lastErr, &lastSuccess); err != nil {
			t.Fatalf("read sync state: %v", err)
		}
	}
	read()
	if failures != 3 || lastErr == nil || *lastErr != wedge.Error() || lastSuccess != nil {
		t.Fatalf("after 3 failures: failures=%d last_error=%v last_success=%v", failures, lastErr, lastSuccess)
	}

	if err := RecordSyncOutcome(ctx, pool, repo, "console-migration", nil); err != nil {
		t.Fatalf("record success: %v", err)
	}
	read()
	if failures != 0 || lastSuccess == nil {
		t.Fatalf("after success: failures=%d last_success=%v", failures, lastSuccess)
	}
	if sha, err := GetSyncState(ctx, pool, repo, "console-migration"); err != nil || sha != "" {
		t.Fatalf("health rows must not invent a sync cursor: %q %v", sha, err)
	}
}
