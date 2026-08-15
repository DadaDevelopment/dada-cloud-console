package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation mirrors the columns the agent needs from the operations table.
type Operation struct {
	ID            uuid.UUID
	ActorID       uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID *uuid.UUID
	Action        string
	ResourceKind  string
	ResourceName  string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

// SystemActorID is the fixed-UUID non-loginable user (migration 010) the
// platform files its own operations under: deploy hooks, the app autoscaler,
// preview reaping. An operation carrying it has no human watching its result,
// which is why the render-clobber guard applies to it and not to a deploy a
// person clicked.
var SystemActorID = uuid.MustParse(systemActorID)

// Unattended reports whether the platform, rather than a person, asked for this
// operation.
func (o Operation) Unattended() bool { return o.ActorID == SystemActorID }

const claimBatchSize = 10

// staleProcessingTimeout is how long an operation may sit in Processing before
// another worker may take it over.
//
// A claim flips Created to Processing and nothing else ever touches the row, so
// a pod that dies mid-operation (a rollout, an OOM kill) leaves the operation
// Processing forever: the console shows it running, no worker will look at it
// again, and the user's action is silently lost. Two SetDatabaseShard operations
// were stranded that way by ordinary deploys [live psql, 2026-08-10].
//
// The window is generous on purpose. There is no heartbeat, so a genuinely slow
// operation is only safe from a second worker for this long, and the git work
// these operations do (clone, patch, commit) is idempotent when repeated: a
// patch that is already in place reports no change and commits nothing.
const staleProcessingTimeout = 30 * time.Minute

// held tracks the operations this process has claimed and not yet finished, so
// a shutdown can hand them back instead of leaving them for the stale timeout.
//
// A claim takes a batch of up to claimBatchSize and the dispatcher works
// through it one at a time [worker/dbwatcher.go poll], so a rollout that lands
// mid-batch strands every operation still queued behind the current one --
// none of them was ever started, and all of them wait out staleProcessingTimeout
// before any worker looks at them again. Measured on prod: a user's
// DeployImageVersion sat 31.2 minutes that way, and gitops-agent rolled 11
// times in 7 hours [live psql + kubectl, 2026-08-14], so the window is hit
// often rather than rarely.
var held = struct {
	sync.Mutex
	ids map[uuid.UUID]struct{}
}{ids: map[uuid.UUID]struct{}{}}

func holdClaim(id uuid.UUID) {
	held.Lock()
	held.ids[id] = struct{}{}
	held.Unlock()
}

func releaseClaim(id uuid.UUID) {
	held.Lock()
	delete(held.ids, id)
	held.Unlock()
}

// ReleaseHeldClaims hands every operation this process still holds back to
// Created, and returns how many rows it moved. Call it on SIGTERM, before the
// process exits.
//
// The WHERE clause requires the row to still be Processing: an operation that
// reached a terminal status while the release was in flight keeps its verdict,
// and one another worker has already re-claimed is not stolen back.
//
// The operation currently being dispatched is released too. Re-running it is
// the same retry the stale timeout already produces, only half an hour sooner,
// and the git work these handlers do is idempotent when repeated -- a patch
// already in place commits nothing.
//
// The caller must pass a context that is NOT the one the signal cancelled;
// otherwise every statement here fails before touching the database.
func ReleaseHeldClaims(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	held.Lock()
	ids := make([]uuid.UUID, 0, len(held.ids))
	for id := range held.ids {
		ids = append(ids, id)
	}
	held.ids = map[uuid.UUID]struct{}{}
	held.Unlock()

	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Created', updated_at = NOW()
		WHERE  id = ANY($1) AND status = 'Processing'
	`, ids)
	if err != nil {
		return 0, fmt.Errorf("release held claims: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClaimPending atomically claims up to claimBatchSize operations, marking them
// Processing, and returns them. Uses SKIP LOCKED so multiple replicas can run
// without contention.
//
// It claims Created operations and re-claims ones abandoned in Processing by a
// worker that died, so a rollout mid-operation costs a retry rather than the
// operation.
func ClaimPending(ctx context.Context, pool *pgxpool.Pool) ([]Operation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// gitops-agent owns git rendering for ALL runtimes (k8s Helm apps and VM
	// compose apps alike). The portainer-agent owns VM/endpoint lifecycle,
	// stack deploys, and read-only workload discovery, so those actions are
	// excluded here. The split is purely by action, making the two claim sets
	// disjoint regardless of env.runtime — this list MUST mirror the exclusion of
	// portainer-agent's ClaimPending include list (add new VM actions to both).
	//
	// THIS IS A DENYLIST, AND THAT MAKES IT A LANDMINE FOR EVERY NEW ACTION.
	// Anything not named here is claimed by this agent, and anything it claims
	// that its dispatch switch does not know is failed immediately with
	// "unknown action". So an action owned by a *third* agent must be excluded
	// here on the same commit that introduces it, or the feature is dead on
	// arrival with a confusing error and no retry. portainer-agent, by contrast,
	// uses an allowlist and needs no edit for a foreign action.
	//
	// The ten Box* actions below are owned by box-agent (a separate module, not
	// yet written; see docs/plans/2026-07-29-box-runtime-architecture.md). They
	// are excluded ahead of that agent existing on purpose: until it ships, a box
	// operation sits in Created — visibly pending — instead of being claimed here
	// and marked Failed. Keep this list byte-identical to models.BoxActions in the
	// backend module (the two cannot import each other).
	rows, err := tx.Query(ctx, `
		UPDATE operations
		SET    status = 'Processing', updated_at = NOW()
		WHERE  id IN (
			SELECT o.id FROM operations o
			WHERE  (o.status = 'Created' OR (o.status = 'Processing' AND o.updated_at < NOW() - $2::interval))
			  AND  o.action NOT IN ('CreateAppServer', 'DeleteAppServer', 'DeployStack', 'DiscoverWorkload', 'RestartStack',
			                        'BoxUp', 'SuspendBox', 'ResumeBox', 'DeleteBox',
			                        'AttachBoxDatabase', 'AttachBoxS3', 'DetachBoxAttachment',
			                        'ExposeBox', 'UnexposeBox', 'CrystallizeBox')
			ORDER  BY o.created_at
			LIMIT  $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name, payload, created_at
	`, claimBatchSize, staleProcessingTimeout.String())
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}
	defer rows.Close()

	var ops []Operation
	for rows.Next() {
		var op Operation
		if err := rows.Scan(
			&op.ID, &op.ActorID, &op.ProjectID, &op.EnvironmentID,
			&op.Action, &op.ResourceKind, &op.ResourceName,
			&op.Payload, &op.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning operation: %w", err)
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}
	for _, op := range ops {
		holdClaim(op.ID)
	}
	return ops, nil
}

// MarkCommitted sets status=Committed, records the git commit SHA and path, and
// records the success in audit_events.
//
// The status is written before the audit row on purpose: the row is the
// operation's verdict, and it must not be able to claim success for an
// operation the table still shows as unfinished.
func MarkCommitted(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, sha, gitPath string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Committed', git_commit = $2, git_path = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, sha, gitPath)
	if err != nil {
		return err
	}
	releaseClaim(id)
	recordSuccessAudit(ctx, pool, id, sha)
	return nil
}

// recordSuccessAudit writes an outcome=success audit row for an operation that
// committed without a verdict of its own in audit_events.
//
// A handler's row is written at enqueue time and says only that the user asked:
// writeAuditRow [backend audit.go] stores it as outcome=pending precisely
// because the operation has not finished. This is the row that finishes it. The
// pair reads as intent then result, told apart by outcome, and success in
// audit_events means the manifest actually reached git.
//
// Some operations have no intent row at all -- the repairs this agent runs on
// its own behalf. On prod AttachDefaultDomain had 15 operations against zero
// audit rows [live psql, 30d]; both of its call sites are self-repair,
// reissueDefaultDomainDNS and BackfillMissingDefaultDomains [backend
// domains.go], so no user asked and no handler audited. Those get their first
// row here, and the value is not the click but the explanation: the user's app
// suddenly has a public URL, and until now nothing recorded when or why.
//
// The guard is the absence of a TERMINAL row, not of any row: a pending intent
// must still receive its verdict, while a retried terminal write must not stack
// a second one, and an operation that already failed keeps that verdict. The
// guard is on outcome rather than on the action name because ResizeApp is
// audited under two other names -- UpdateAppProfile for the user path,
// AutoscaleApp for the watcher -- so a name-based guard would miss both.
//
// The row is built from the operations row itself so it cannot disagree with it
// about actor, project, environment or resource, and carries the commit so a
// domain that appeared out of nowhere can be traced to the change that made it.
//
// Best-effort: the operation genuinely succeeded and must not be reported as
// failed by a bookkeeping error.
func recordSuccessAudit(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, sha string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, o.resource_kind, o.resource_name, 'success',
		       jsonb_build_object('phase', 'operation', 'git_commit', $2::text)
		  FROM operations o
		 WHERE o.id = $1
		   AND NOT EXISTS (
			SELECT 1 FROM audit_events a
			 WHERE a.operation_id = o.id
			   AND a.outcome IN ('success', 'failure')
		   )
	`, id, sha); err != nil {
		log.Printf("mark committed: audit row insert failed for op %s: %v", id, err)
	}
}

// MarkNoop ends an operation whose desired state is already in git: nothing was
// committed, and nothing needed to be.
//
// It exists because the dispatcher [worker/dbwatcher.go poll] writes a terminal
// status only for a handler that returns an ERROR, while success is written by
// commitFilesAndRecord -> MarkCommitted. A handler that returns nil without
// committing therefore ends nothing: the row stays Processing, ClaimPending
// re-claims it after staleProcessingTimeout, and the operation loops forever
// while the console shows the user's action as still running. Four handlers took
// that shortcut on their "already the wanted value" branch, and on prod it
// stranded a user's SetDatabaseTier for hours [live psql, 2026-08-15].
//
// The status is Committed rather than a new one because a no-op is a success by
// every reader's definition -- git holds exactly what was asked for -- and
// introducing a status the backend, the console and the admin panel do not know
// would trade a wedged row for an unreadable one. git_commit stays NULL and the
// reason rides the audit row, so the two are told apart without putting error
// text on an operation that did not fail.
func MarkNoop(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, reason string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Committed', updated_at = NOW()
		WHERE  id = $1
	`, id)
	if err != nil {
		return err
	}
	releaseClaim(id)
	recordNoopAudit(ctx, pool, id, reason)
	return nil
}

// recordNoopAudit writes the outcome=success verdict for an operation that had
// nothing to change, carrying the reason so path analysis can tell a no-op apart
// from a commit. Guarded and best-effort for the same reasons as
// recordSuccessAudit.
func recordNoopAudit(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, reason string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, o.resource_kind, o.resource_name, 'success',
		       jsonb_build_object('phase', 'operation', 'noop', true, 'reason', $2::text)
		  FROM operations o
		 WHERE o.id = $1
		   AND NOT EXISTS (
			SELECT 1 FROM audit_events a
			 WHERE a.operation_id = o.id
			   AND a.outcome IN ('success', 'failure')
		   )
	`, id, reason); err != nil {
		log.Printf("mark noop: audit row insert failed for op %s: %v", id, err)
	}
}

// MarkFailed sets status=Failed with an error message, and records the failure
// in audit_events.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Failed', error_code = $2, error_message = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, code, message)
	if err != nil {
		return err
	}
	releaseClaim(id)
	recordFailureAudit(ctx, pool, id, code, message)
	return nil
}

// recordFailureAudit writes an outcome=failure audit row for an operation that
// died inside the worker. Every audit row an API handler writes is recorded at
// enqueue time, when nothing is known yet except that the user asked -- so an
// action that is accepted and then fails asynchronously stays outcome=success in
// audit_events forever, and path analysis counts a failed deploy, a failed
// database, a failed move as things that worked. This is the only terminal
// failure path for operations the gitops agent runs [dbwatcher.go poll].
//
// The row is built from the operations row itself so it cannot disagree with it
// about actor, project, environment or resource, and it carries the same action
// as the success row: the pair reads as intent then result, distinguished by
// outcome. The NOT EXISTS guard keeps a retried MarkFailed from stacking rows.
//
// Best-effort: the operation is already marked Failed and must not be resurrected
// by a bookkeeping error.
func recordFailureAudit(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, o.resource_kind, o.resource_name, 'failure',
		       jsonb_build_object('reason', $2::text, 'error', left($3::text, 300), 'phase', 'operation')
		  FROM operations o
		 WHERE o.id = $1
		   AND NOT EXISTS (
			SELECT 1 FROM audit_events a
			 WHERE a.operation_id = o.id AND a.outcome = 'failure'
		   )
	`, id, code, message); err != nil {
		log.Printf("mark failed: audit row insert failed for op %s: %v", id, err)
	}
}

// EnqueueDeployStack creates a follow-up DeployStack operation for a compose
// app, copying actor/project/environment from the parent (render) operation.
// The portainer-agent claims and executes it (CreateStackFromGit / RedeployStack).
//
// The parent id is carried in the payload because the parent terminates at
// "Committed" the moment compose.yaml is in git, long before the VM has pulled
// anything. Without the link a caller polling the parent reads a green deploy
// while the stack redeploy is still running -- or has already failed, which is
// how a build went SUCCESS while fin-core/findata kept serving the previous
// image.
func EnqueueDeployStack(ctx context.Context, pool *pgxpool.Pool, parentOpID uuid.UUID, appName string, volumes []string) (uuid.UUID, error) {
	if volumes == nil {
		volumes = []string{}
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT actor_id, project_id, environment_id, 'DeployStack', 'App', $2::text, 'Created',
		       jsonb_build_object('app_name', $2::text, 'volumes', $3::jsonb,
		                          'parent_operation_id', $1::text)
		FROM operations WHERE id = $1::uuid
		RETURNING id`,
		parentOpID, appName, volumes,
	).Scan(&id)
	return id, err
}

// systemActorID is the fixed-UUID non-loginable user (migration 010) used as the
// actor for agent-initiated operations that have no originating user request.
const systemActorID = "00000000-0000-0000-0000-000000000000"

// EnqueueDeployStackBySlug creates a DeployStack operation for a compose app
// identified by project/env slugs, attributed to the system actor. Used by the
// editor save path (which has no originating user/operation). Returns
// pgx.ErrNoRows if the project/env slugs don't resolve.
func EnqueueDeployStackBySlug(ctx context.Context, pool *pgxpool.Pool, projectSlug, envSlug, appName string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT $4::uuid, p.id, e.id, 'DeployStack', 'App', $3::text, 'Created',
		       jsonb_build_object('app_name', $3::text)
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = $1 AND e.name = $2
		RETURNING id`,
		projectSlug, envSlug, appName, systemActorID,
	).Scan(&id)
	return id, err
}
