package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RepointCommit moves every record of oldSHA to newSHA after the git layer
// replayed a stranded commit onto the remote under a new hash, so an operation
// marked Committed names a commit that exists on the remote branch.
func RepointCommit(ctx context.Context, pool *pgxpool.Pool, oldSHA, newSHA string) (int64, error) {
	tag, err := pool.Exec(ctx, `UPDATE operations SET git_commit = $2, updated_at = NOW() WHERE git_commit = $1`, oldSHA, newSHA)
	if err != nil {
		return 0, fmt.Errorf("repoint operations %s -> %s: %w", oldSHA, newSHA, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE git_commits SET sha = $2 WHERE sha = $1`, oldSHA, newSHA); err != nil {
		return tag.RowsAffected(), fmt.Errorf("repoint git_commits %s -> %s: %w", oldSHA, newSHA, err)
	}
	return tag.RowsAffected(), nil
}

// RequeueStrandedOperation puts an operation back in the queue when the commit
// it was recorded against had to be discarded without reaching the remote.
// Only an operation still marked Committed on exactly that SHA is touched: one
// that was already released, re-run or failed is left alone.
func RequeueStrandedOperation(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, droppedSHA string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Created', git_commit = NULL, updated_at = NOW()
		WHERE  id = $1 AND status = 'Committed' AND git_commit = $2
	`, id, droppedSHA)
	if err != nil {
		return false, fmt.Errorf("requeue operation %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordSyncOutcome keeps the git watcher's health on its git_sync_state row:
// a run of failures grows consecutive_failures and keeps the last error, a
// success resets the run. The backend exports these as metrics, which is what
// makes a wedged clone page within minutes instead of being found by hand.
func RecordSyncOutcome(ctx context.Context, pool *pgxpool.Pool, repoURL, branch string, syncErr error) error {
	var msg *string
	if syncErr != nil {
		s := syncErr.Error()
		msg = &s
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO git_sync_state (repo_url, branch, last_sha, consecutive_failures, last_error, last_attempt_at, last_success_at)
		VALUES ($1, $2, '', CASE WHEN $3::text IS NULL THEN 0 ELSE 1 END, $3, NOW(),
		        CASE WHEN $3::text IS NULL THEN NOW() END)
		ON CONFLICT (repo_url, branch) DO UPDATE
		SET consecutive_failures = CASE WHEN $3::text IS NULL THEN 0 ELSE git_sync_state.consecutive_failures + 1 END,
		    last_error           = COALESCE($3, git_sync_state.last_error),
		    last_attempt_at      = NOW(),
		    last_success_at      = CASE WHEN $3::text IS NULL THEN NOW() ELSE git_sync_state.last_success_at END
	`, repoURL, branch, msg)
	if err != nil {
		return fmt.Errorf("record sync outcome for %s@%s: %w", repoURL, branch, err)
	}
	return nil
}
