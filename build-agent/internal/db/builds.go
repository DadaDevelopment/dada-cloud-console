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
	ImageURI      string     // pinned on success: harbor.../<proj>/<app>@sha256:<digest>
	PRNumber      *int       // set for trigger='pr' builds (preview deployments); nil otherwise
	ForkUnsafe    bool       // fork-PR safety: never inject secrets when true
	TriggeredBy   *uuid.UUID // human who triggered a manual build; nil for push/webhook
	CreatedAt     time.Time
}

// ReclaimBuild is an in-flight build plus the Jenkins references needed to
// re-attach to its still-running job after an agent restart.
type ReclaimBuild struct {
	Build
	JenkinsQueueID     *int64
	JenkinsBuildNumber *int
}

// SetJenkinsQueueID records the Jenkins queue item id for a build. Best-effort:
// callers treat an error as non-fatal (the build proceeds, it just will not
// survive an agent restart).
func SetJenkinsQueueID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, queueID int64) error {
	_, err := pool.Exec(ctx, `UPDATE builds SET jenkins_queue_id = $2, updated_at = NOW() WHERE id = $1`, id, queueID)
	if err != nil {
		return fmt.Errorf("set jenkins queue id %s: %w", id, err)
	}
	return nil
}

// SetJenkinsBuildNumber records the resolved Jenkins build number for a build.
// Best-effort, like SetJenkinsQueueID.
func SetJenkinsBuildNumber(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, number int) error {
	_, err := pool.Exec(ctx, `UPDATE builds SET jenkins_build_number = $2, updated_at = NOW() WHERE id = $1`, id, number)
	if err != nil {
		return fmt.Errorf("set jenkins build number %s: %w", id, err)
	}
	return nil
}

// SetHeadCommit records the real HEAD commit sha (and message, if none is
// already stored) resolved for a manual build whose commit_sha column holds the
// synthetic "manual-<ts>" placeholder (see migration 091). It never clobbers an
// existing commit_message, since a webhook-originated row may already have one.
// Best-effort: callers should log and continue on error rather than fail the
// build over a display detail.
func SetHeadCommit(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID, sha, message string) error {
	_, err := pool.Exec(ctx, `
		UPDATE builds
		SET    head_sha = $2, commit_message = COALESCE(NULLIF(commit_message, ''), $3), updated_at = NOW()
		WHERE  id = $1
	`, buildID, sha, message)
	if err != nil {
		return fmt.Errorf("set head commit %s: %w", buildID, err)
	}
	return nil
}

// MarkPushing moves a build to pushing from building (or leaves it pushing).
// Unlike a strict building→pushing Transition it tolerates an already-pushing
// row so restart reconciliation can re-drive a build that died mid-confirm. It
// returns false (no error) when the row is no longer in-flight (superseded /
// canceled), which the caller uses to stop without deploying.
func MarkPushing(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = 'pushing', updated_at = NOW()
		WHERE  id = $1 AND status IN ('building','pushing')
	`, id)
	if err != nil {
		return false, fmt.Errorf("mark pushing %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CurrentStatus returns a build's current status. Used to tell a shutdown
// (row still in-flight) from a supersession (row already canceled) when a build
// context is canceled.
func CurrentStatus(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (string, error) {
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM builds WHERE id = $1`, id).Scan(&status); err != nil {
		return "", fmt.Errorf("current status %s: %w", id, err)
	}
	return status, nil
}

// InFlightBuilds returns every non-terminal build together with its Jenkins
// references, so a freshly started agent can re-attach to jobs the previous
// instance was tracking. Ordered oldest-first.
func InFlightBuilds(ctx context.Context, pool *pgxpool.Pool) ([]ReclaimBuild, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, git_repo_id, environment_id, app_name, commit_sha, branch,
		       trigger, status, pr_number, fork_unsafe, triggered_by, created_at,
		       jenkins_queue_id, jenkins_build_number
		FROM   builds
		WHERE  status IN ('detecting','building','pushing')
		ORDER  BY started_at ASC NULLS FIRST
	`)
	if err != nil {
		return nil, fmt.Errorf("list in-flight builds: %w", err)
	}
	defer rows.Close()

	var out []ReclaimBuild
	for rows.Next() {
		var rb ReclaimBuild
		if err := rows.Scan(
			&rb.ID, &rb.GitRepoID, &rb.EnvironmentID, &rb.AppName, &rb.CommitSHA, &rb.Branch,
			&rb.Trigger, &rb.Status, &rb.PRNumber, &rb.ForkUnsafe, &rb.TriggeredBy, &rb.CreatedAt,
			&rb.JenkinsQueueID, &rb.JenkinsBuildNumber,
		); err != nil {
			return nil, fmt.Errorf("scan in-flight build: %w", err)
		}
		out = append(out, rb)
	}
	return out, rows.Err()
}

// SuccessBuildsMissingDeploy returns, per repo+branch, the single newest
// successful build when that build's deploy handoff never landed: it has a
// pinned image but no deployments row. HandoffDeploy failed for it (e.g. a
// transient DB error rolled it back), leaving the app NotDeployed with nothing
// to retry.
//
// The "no deployment" filter must be applied AFTER selecting the newest build
// per repo+branch, not inside the selection: an older success build that also
// lacks a deployment is superseded by the newer one and must NOT be reconciled,
// or the reconciler would deploy every historical build one per tick (newest
// first), ending on the oldest image.
func SuccessBuildsMissingDeploy(ctx context.Context, pool *pgxpool.Pool) ([]Build, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, git_repo_id, environment_id, app_name, commit_sha,
		       branch, trigger, status, image_uri, pr_number, fork_unsafe, triggered_by, created_at
		FROM (
			SELECT DISTINCT ON (b.git_repo_id, b.branch)
			       b.id, b.git_repo_id, b.environment_id, b.app_name, b.commit_sha,
			       b.branch, b.trigger, b.status, b.image_uri, b.pr_number, b.fork_unsafe, b.triggered_by, b.created_at
			FROM   builds b
			WHERE  b.status = 'success' AND b.image_uri IS NOT NULL AND b.image_uri <> ''
			  AND  b.created_at > NOW() - make_interval(days => 7)
			ORDER  BY b.git_repo_id, b.branch, b.created_at DESC
		) latest
		WHERE NOT EXISTS (SELECT 1 FROM deployments d WHERE d.build_id = latest.id)
	`)
	if err != nil {
		return nil, fmt.Errorf("success builds missing deploy: %w", err)
	}
	defer rows.Close()

	var out []Build
	for rows.Next() {
		var b Build
		if err := rows.Scan(
			&b.ID, &b.GitRepoID, &b.EnvironmentID, &b.AppName, &b.CommitSHA,
			&b.Branch, &b.Trigger, &b.Status, &b.ImageURI, &b.PRNumber, &b.ForkUnsafe, &b.TriggeredBy, &b.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan missing-deploy build: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RowQuerier is satisfied by both *pgxpool.Pool and *pgxpool.Conn, so callers can
// run a check on the same connection that holds an advisory lock.
type RowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DeploymentExistsForBuild reports whether a deployments row already references
// the build. Used by the deploy reconciler to re-check under an advisory lock so
// a rolling two-pod overlap cannot double-enqueue a deploy.
func DeploymentExistsForBuild(ctx context.Context, q RowQuerier, buildID uuid.UUID) (bool, error) {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployments WHERE build_id = $1)`, buildID).Scan(&exists); err != nil {
		return false, fmt.Errorf("deployment exists for build %s: %w", buildID, err)
	}
	return exists, nil
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
		          commit_sha, branch, trigger, status, pr_number, fork_unsafe, triggered_by, created_at
	`)

	var b Build
	if err := row.Scan(
		&b.ID, &b.GitRepoID, &b.EnvironmentID, &b.AppName,
		&b.CommitSHA, &b.Branch, &b.Trigger, &b.Status, &b.PRNumber, &b.ForkUnsafe, &b.TriggeredBy, &b.CreatedAt,
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

// RequeueForRetry sends an in-flight build back to the queue for another attempt
// after a transient failure (Jenkins/ingress 503, timeout, external ABORTED),
// recording the reason and bounding retries at maxAttempts. It clears the
// started/finished timestamps and the Jenkins refs so the retry gets a fresh run
// (and restart reconciliation does not re-attach to the dead run). Returns true
// when the build was re-queued; false (with no error) when attempts are
// exhausted or the row is no longer in-flight, so the caller can fail it with a
// recorded reason instead.
func RequeueForRetry(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, reason string, maxAttempts int) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    status = 'queued',
		       attempt = attempt + 1,
		       error_message = $2,
		       started_at = NULL,
		       finished_at = NULL,
		       jenkins_queue_id = NULL,
		       jenkins_build_number = NULL,
		       updated_at = NOW()
		WHERE  id = $1
		  AND  status IN ('detecting','building','pushing')
		  AND  attempt < $3
	`, id, reason, maxAttempts)
	if err != nil {
		return false, fmt.Errorf("requeue for retry %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// buildFinishedAuditAction/Kind name the terminal-verdict audit row written by
// MarkFailedWithReason, MarkCanceled and FinishSuccess. Before this, the only
// audit row a build ever got was TriggerBuild at enqueue time (backend
// builds.go) -- the actual outcome (success, failure, why) never left a trace,
// so a user path read as "TriggerBuild then silence" whether the build failed,
// hung, or the user just never checked back.
const (
	buildFinishedAuditAction = "BuildFinished"
	buildFinishedAuditKind   = "Build"
)

// buildFinishedActorExpr resolves who a BuildFinished row is attributed to,
// mirrored from handoffActor's fallback (deploy.go): a real console click
// (triggered_by) wins, then the repo's owner (git_repos.created_by) for a
// push/webhook build, then the system user for a repo connected before
// migration 037 ever recorded an owner. audit_events.actor_id is NOT NULL, so
// this must always resolve to something -- $%d is the SystemUserID parameter.
func buildFinishedActorExpr(paramIdx int) string {
	return fmt.Sprintf("COALESCE(u.triggered_by, g.created_by, $%d::uuid)", paramIdx)
}

// MarkFailedWithReason moves a build to failed, recording the error message,
// the classified fail_reason code (empty when the failure was not
// classified), and finished_at, and -- in the SAME statement -- writes the
// BuildFinished/failure audit row. One statement, not "update then insert":
// two separate statements can diverge (the update commits, the audit write
// is lost to a crash/network blip in between), which is exactly the gap this
// change exists to close. The audit INSERT is itself an INNER JOIN off the
// UPDATE's RETURNING via CTEs, so it only ever runs, and only ever runs once,
// when this call is the one that actually won the failed transition -- a
// second call against an already-failed row updates zero rows, so its CTE
// chain inserts zero audit rows too. Compare-and-set on `from` so it loses
// cleanly against a concurrent cancel/supersede. Returns true when it changed
// a row.
func MarkFailedWithReason(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, from, errMsg, failReason string) (bool, error) {
	var affected int
	err := pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE builds
			SET    status = 'failed', error_message = $3, fail_reason = NULLIF($4, ''), finished_at = NOW(), updated_at = NOW()
			WHERE  id = $1 AND status = $2
			RETURNING id, git_repo_id, environment_id, app_name, branch, commit_sha, triggered_by, attempt, fail_reason, started_at, finished_at
		),
		actor AS (
			SELECT u.*, g.project_id, `+buildFinishedActorExpr(5)+` AS actor_id
			FROM   updated u
			JOIN   git_repos g ON g.id = u.git_repo_id
		),
		audit_ins AS (
			INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata)
			SELECT actor_id, project_id, environment_id, '`+buildFinishedAuditAction+`', '`+buildFinishedAuditKind+`', app_name, 'failure',
			       jsonb_strip_nulls(jsonb_build_object(
			           'build_id', id, 'status', 'failed', 'fail_reason', fail_reason, 'attempt', attempt,
			           'duration_seconds', ROUND(EXTRACT(EPOCH FROM (finished_at - started_at))::numeric, 2),
			           'branch', branch, 'commit_sha', commit_sha
			       ))
			FROM   actor
			RETURNING 1
		)
		SELECT count(*) FROM updated u LEFT JOIN audit_ins a ON true
	`, id, from, errMsg, failReason, SystemUserID).Scan(&affected)
	if err != nil {
		return false, fmt.Errorf("mark failed %s: %w", id, err)
	}
	return affected == 1, nil
}

// MarkCanceled moves any non-terminal build to canceled (used by supersession)
// and, in the same statement, writes the BuildFinished/canceled audit row.
//
// outcome is 'canceled', not 'failure': the only caller in this codebase is
// supersede() (runner.go), which cancels a build because a newer commit on
// the same repo+branch superseded it -- a system decision, not something the
// user got wrong. Counting it as a failure would misread every fast-follow
// push as a broken build. audit_events.outcome carries no CHECK constraint
// (migration 068), so this needs no schema change.
func MarkCanceled(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (bool, error) {
	var affected int
	err := pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE builds
			SET    status = 'canceled', finished_at = NOW(), updated_at = NOW()
			WHERE  id = $1 AND status IN ('queued','detecting','building','pushing')
			RETURNING id, git_repo_id, environment_id, app_name, branch, commit_sha, triggered_by, attempt, started_at, finished_at
		),
		actor AS (
			SELECT u.*, g.project_id, `+buildFinishedActorExpr(2)+` AS actor_id
			FROM   updated u
			JOIN   git_repos g ON g.id = u.git_repo_id
		),
		audit_ins AS (
			INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata)
			SELECT actor_id, project_id, environment_id, '`+buildFinishedAuditAction+`', '`+buildFinishedAuditKind+`', app_name, 'canceled',
			       jsonb_strip_nulls(jsonb_build_object(
			           'build_id', id, 'status', 'canceled', 'attempt', attempt,
			           'duration_seconds', ROUND(EXTRACT(EPOCH FROM (finished_at - started_at))::numeric, 2),
			           'branch', branch, 'commit_sha', commit_sha
			       ))
			FROM   actor
			RETURNING 1
		)
		SELECT count(*) FROM updated u LEFT JOIN audit_ins a ON true
	`, id, SystemUserID).Scan(&affected)
	if err != nil {
		return false, fmt.Errorf("mark canceled %s: %w", id, err)
	}
	return affected == 1, nil
}

// ReapStuckBuilds fails builds left in a non-terminal in-flight state
// (detecting/building/pushing) whose started_at is older than olderThan. These
// are orphans: the Runner tracking them in-process died (restart/OOM/eviction)
// before writing a terminal status, and nothing re-claims a non-'queued' build,
// so the row would hang forever and the UI would show "Building" with no Retry.
// olderThan is chosen above BuildTimeout so a still-live build (which self-fails
// at its own timeout) is never reaped out from under itself, and so a brief
// two-pod overlap during a rolling deploy cannot kill the outgoing pod's build.
// Returns the reaped ids for logging.
//
// The BuildFinished audit row is written in the SAME statement, for the same
// reason MarkFailedWithReason does it (see there): a build killed by an agent
// restart is exactly the case where a follow-up write is most likely to be
// lost, and without the row the user's path reads as "TriggerBuild then
// silence" -- indistinguishable from a build that is still running. This
// reaps a set, not one row, so the CTE chain fans out over every reaped row.
func ReapStuckBuilds(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		WITH updated AS (
			UPDATE builds
			SET    status = 'failed',
			       error_message = 'build orphaned: build-agent restarted before completion; retry',
			       fail_reason = 'platform_error',
			       finished_at = NOW(), updated_at = NOW()
			WHERE  status IN ('detecting','building','pushing')
			  AND  started_at < NOW() - make_interval(secs => $1)
			RETURNING id, git_repo_id, environment_id, app_name, branch, commit_sha, triggered_by, attempt, started_at, finished_at
		),
		actor AS (
			SELECT u.*, g.project_id, `+buildFinishedActorExpr(2)+` AS actor_id
			FROM   updated u
			JOIN   git_repos g ON g.id = u.git_repo_id
		),
		audit_ins AS (
			INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata)
			SELECT actor_id, project_id, environment_id, '`+buildFinishedAuditAction+`', '`+buildFinishedAuditKind+`', app_name, 'failure',
			       jsonb_strip_nulls(jsonb_build_object(
			           'build_id', id, 'status', 'failed', 'fail_reason', 'platform_error', 'attempt', attempt,
			           'duration_seconds', ROUND(EXTRACT(EPOCH FROM (finished_at - started_at))::numeric, 2),
			           'branch', branch, 'commit_sha', commit_sha, 'reaped', true
			       ))
			FROM   actor
			RETURNING 1
		)
		SELECT id FROM updated
	`, olderThan.Seconds(), SystemUserID)
	if err != nil {
		return nil, fmt.Errorf("reap stuck builds: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan reaped build: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FinishSuccess pins the immutable image URI and stamps finished_at as part of
// the pushing→success transition, writing the BuildFinished/success audit row
// in the same statement (see MarkFailedWithReason for why it must be the same
// statement, not a follow-up write).
func FinishSuccess(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, imageURI string) (bool, error) {
	var affected int
	err := pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE builds
			SET    status = 'success', image_uri = $2, finished_at = NOW(), updated_at = NOW()
			WHERE  id = $1 AND status = 'pushing'
			RETURNING id, git_repo_id, environment_id, app_name, branch, commit_sha, triggered_by, attempt, started_at, finished_at
		),
		actor AS (
			SELECT u.*, g.project_id, `+buildFinishedActorExpr(3)+` AS actor_id
			FROM   updated u
			JOIN   git_repos g ON g.id = u.git_repo_id
		),
		audit_ins AS (
			INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata)
			SELECT actor_id, project_id, environment_id, '`+buildFinishedAuditAction+`', '`+buildFinishedAuditKind+`', app_name, 'success',
			       jsonb_strip_nulls(jsonb_build_object(
			           'build_id', id, 'status', 'success', 'attempt', attempt,
			           'duration_seconds', ROUND(EXTRACT(EPOCH FROM (finished_at - started_at))::numeric, 2),
			           'branch', branch, 'commit_sha', commit_sha
			       ))
			FROM   actor
			RETURNING 1
		)
		SELECT count(*) FROM updated u LEFT JOIN audit_ins a ON true
	`, id, imageURI, SystemUserID).Scan(&affected)
	if err != nil {
		return false, fmt.Errorf("finish success %s: %w", id, err)
	}
	return affected == 1, nil
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
