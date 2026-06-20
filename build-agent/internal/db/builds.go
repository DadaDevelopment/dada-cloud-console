package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Build status enum (migration 013):
// queued | detecting | building | pushing | success | failed | canceled.
const (
	StatusQueued    = "queued"
	StatusDetecting = "detecting"
	StatusBuilding  = "building"
	StatusPushing   = "pushing"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// Build mirrors the columns the agent needs from the builds table
// (migration 013_git_build_deploy.sql). Note: builds has no project_id column —
// the project is resolved via git_repos (see LoadRepo). environment_id is
// NOT NULL in the schema.
type Build struct {
	ID            uuid.UUID
	GitRepoID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	CommitSHA     string
	Branch        string
	Trigger       string // push | pr | manual | rollback
	Status        string
	ImageURI      string // pinned on success: harbor.../<proj>/<app>@sha256:<digest>
	ForkUnsafe    bool   // fork-PR safety: never inject secrets when true
	CreatedAt     time.Time
}

// ClaimQueued atomically claims the next queued build (one at a time so the
// in-proc scheduler controls real concurrency) using FOR UPDATE SKIP LOCKED.
// It moves the build queued→detecting and stamps started_at. Returns (nil, nil)
// when the queue is empty.
func ClaimQueued(ctx context.Context, pool *pgxpool.Pool) (*Build, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		UPDATE builds
		SET    status = 'detecting', started_at = NOW(), updated_at = NOW()
		WHERE  id = (
			SELECT id FROM builds
			WHERE  status = 'queued'
			ORDER  BY created_at
			LIMIT  1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, git_repo_id, environment_id, app_name,
		          commit_sha, branch, trigger, status, created_at
	`)

	var b Build
	if err := row.Scan(
		&b.ID, &b.GitRepoID, &b.EnvironmentID, &b.AppName,
		&b.CommitSHA, &b.Branch, &b.Trigger, &b.Status, &b.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // empty queue
		}
		return nil, fmt.Errorf("claim build: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return &b, nil
}

// Transition is a compare-and-set status update: it only succeeds when the row
// is still in `from`. Used for every state-machine edge so supersession /
// cancellation races resolve to a single winner. Returns true when it changed a
// row.
func Transition(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, from, to string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = $3, updated_at = NOW()
		WHERE  id = $1 AND status = $2
	`, id, from, to)
	if err != nil {
		return false, fmt.Errorf("transition build %s %s->%s: %w", id, from, to, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkFailed moves a build to failed, recording the error message and
// finished_at. Compare-and-set on `from` so it loses cleanly against a concurrent
// cancel/supersede. Returns true when it changed a row.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, from, errMsg string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = 'failed', error_message = $3, finished_at = NOW(), updated_at = NOW()
		WHERE  id = $1 AND status = $2
	`, id, from, errMsg)
	if err != nil {
		return false, fmt.Errorf("mark failed %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkCanceled moves any non-terminal build to canceled (used by supersession).
func MarkCanceled(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = 'canceled', finished_at = NOW(), updated_at = NOW()
		WHERE  id = $1 AND status IN ('queued','detecting','building','pushing')
	`, id)
	if err != nil {
		return false, fmt.Errorf("mark canceled %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// FinishSuccess pins the immutable image URI and stamps finished_at as part of
// the pushing→success transition.
func FinishSuccess(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, imageURI string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = 'success', image_uri = $2, finished_at = NOW(), updated_at = NOW()
		WHERE  id = $1 AND status = 'pushing'
	`, id, imageURI)
	if err != nil {
		return false, fmt.Errorf("finish success %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// PinImage records the immutable image URI on a build without changing status.
func PinImage(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, imageURI string) error {
	_, err := pool.Exec(ctx, `UPDATE builds SET image_uri = $2, updated_at = NOW() WHERE id = $1`, id, imageURI)
	if err != nil {
		return fmt.Errorf("pin image: %w", err)
	}
	return nil
}

// AppendLog appends one captured+redacted log line to builds_logs (live/recent
// store; pruned and replaced by an object-store ref on terminal). seq is a
// per-build monotone counter computed in-statement so concurrent appends from a
// single build's streamer stay ordered.
//
// TODO(wave-3): batch inserts; gzip→object store on terminal, then prune rows.
func AppendLog(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID, line string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO builds_logs (build_id, seq, line)
		VALUES ($1, COALESCE((SELECT MAX(seq)+1 FROM builds_logs WHERE build_id = $1), 0), $2)
	`, buildID, line)
	if err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	return nil
}

// InsertBuildFromWebhook idempotently enqueues a build for a (repo, commit).
// ON CONFLICT (git_repo_id, commit_sha) DO NOTHING gives webhook idempotency.
// Returns the build id (existing or new).
func InsertBuildFromWebhook(ctx context.Context, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA, commitMessage, branch, trigger string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, commit_message, branch, trigger, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued')
		ON CONFLICT (git_repo_id, commit_sha) DO UPDATE SET commit_sha = EXCLUDED.commit_sha
		RETURNING id
	`, gitRepoID, envID, appName, commitSHA, commitMessage, branch, trigger).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert build: %w", err)
	}
	return id, nil
}

// RecentLogs returns the most recent log lines for a build in seq order, for WS
// backlog replay on (re)connect. limit caps the number of returned lines.
func RecentLogs(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := pool.Query(ctx, `
		SELECT line FROM (
			SELECT seq, line FROM builds_logs
			WHERE build_id = $1
			ORDER BY seq DESC
			LIMIT $2
		) t ORDER BY seq ASC
	`, buildID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent logs: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SupersededBuilds returns the ids of older non-terminal builds for the same
// repo+branch as `keep`, so the runner can cancel them (Vercel supersession).
func SupersededBuilds(ctx context.Context, pool *pgxpool.Pool, gitRepoID uuid.UUID, branch string, keep uuid.UUID) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT id FROM builds
		WHERE  git_repo_id = $1 AND branch = $2 AND id <> $3
		  AND  status IN ('queued','detecting','building','pushing')
	`, gitRepoID, branch, keep)
	if err != nil {
		return nil, fmt.Errorf("superseded builds: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
