package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// boxOperationPollInterval is how often each replica looks for pending box
// operations.
const boxOperationPollInterval = 5 * time.Second

// boxOperationClaimBatch bounds how many rows one tick claims, so a burst of
// enqueues (every idle box in a project sleeping at once, say) cannot make one
// tick run unboundedly long and starve the next poll.
const boxOperationClaimBatch = 10

// boxOperationTimeout bounds ONE operation. Without it a call that never
// returns — an exec stream the API server keeps open, a wait on a pod that
// never schedules — leaves its row in Reconciling with nothing that can move
// it, and, because tick executes its batch in series, wedges every other box
// operation this replica would have run behind it. Six minutes is above the
// slowest honest path (a cold resume waiting on a pod pull) and far below the
// hours a hang would otherwise cost.
const boxOperationTimeout = 6 * time.Minute

// boxOperationLease is how long a row may sit in Reconciling before another
// tick treats it as abandoned and claims it again.
//
// A replica that is killed mid-operation cannot write a terminal status, and
// the claim query only ever looked for Created — so its row stayed Reconciling
// forever, which is the same stuck-forever bug this worker was built to end,
// one status further along. The lease is longer than the timeout on purpose:
// a live operation must never be stolen from the replica still running it.
const boxOperationLease = 2 * boxOperationTimeout

// boxOperationMaxAttempts is how many times a box operation is retried before
// it is given up on and marked Failed, so a persistently failing operation
// cannot recreate the "stuck in Created forever" bug this worker exists to
// fix under a different terminal-less status.
const boxOperationMaxAttempts = 3

// errBoxOperationUnimplemented marks an action this worker recognizes but does
// not yet execute: AttachBoxDatabase, AttachBoxS3, DetachBoxAttachment,
// ExposeBox, UnexposeBox and CrystallizeBox are enqueued by their own handlers
// but have no consumer yet. Left for follow-up work rather than folded into
// this change; tracked in docs/plans/2026-08-01-box-operations-worker.md.
// It is wrapped, never retried: an action nothing implements cannot succeed on
// attempt two.
var errBoxOperationUnimplemented = errors.New("box operation action not implemented by the worker")

// boxOperationsWorker claims and executes box lifecycle operations enqueued
// into the operations table (resource_kind='Box') by boxes.go, box_meter.go
// and box_reaper.go. Nothing consumed them before this worker: gitops-agent's
// own claim query (gitops-agent/internal/db/operations.go) deliberately
// excludes every Box action, on the understanding that a separate worker in
// this process would claim them. Until now none did, so a box created or
// deleted through the async doors sat in status 'Created' forever — no
// instance ever came up, nothing was ever destroyed, no operation ever
// reached a terminal status.
//
// Claiming uses FOR UPDATE SKIP LOCKED (claim below) rather than the advisory
// lock box_reaper.go takes for its own pass: the reaper's side effects
// (enqueueing, mailing) are not idempotent per box, so it needs exactly one
// replica running a whole pass, while this worker's unit of work is one row —
// SKIP LOCKED lets both console replicas run the same claim loop
// concurrently, each taking a disjoint slice of the queue, matching how
// gitops-agent already claims its own slice of this same table.
// The timeout field overrides boxOperationTimeout; zero means the constant.
// Only tests set it, so a hang can be proven in milliseconds rather than the
// six minutes production waits.
type boxOperationsWorker struct {
	h       *Handler
	timeout time.Duration
}

// opTimeout is the deadline one operation runs under.
func (w *boxOperationsWorker) opTimeout() time.Duration {
	if w.timeout > 0 {
		return w.timeout
	}
	return boxOperationTimeout
}

// StartBoxOperationsWorker starts the polling loop, or does nothing when the
// box runtime is not configured — the same switch every other box surface
// answers to (BOX_LOCAL_ROOT / BOX_CLUSTER_NAMESPACE, see initBoxRuntime).
func (h *Handler) StartBoxOperationsWorker(ctx context.Context) {
	if h.boxStack == nil {
		return
	}
	w := &boxOperationsWorker{h: h}
	go func() {
		ticker := time.NewTicker(boxOperationPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.tick(ctx)
			}
		}
	}()
	log.Info().Dur("interval", boxOperationPollInterval).Msg("box: operations worker started")
}

// boxOperationRow is one claimed operations row, trimmed to what dispatch
// needs.
type boxOperationRow struct {
	ID       uuid.UUID
	Action   string
	Payload  json.RawMessage
	Attempts int
}

// tick claims a batch of pending box operations and executes each in turn, so
// one operation's failure never blocks another's.
func (w *boxOperationsWorker) tick(ctx context.Context) {
	ops, err := w.claim(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("box worker: claim failed")
		return
	}
	for _, op := range ops {
		w.execute(ctx, op)
	}
}

// claim atomically takes up to boxOperationClaimBatch pending box operations
// and marks them Reconciling, using FOR UPDATE SKIP LOCKED so a concurrent
// claim from the other console replica takes a disjoint set of rows instead
// of blocking on this one's row locks or double-processing one of them. The
// attempt counter is incremented in the same statement, so the retry budget
// is charged even if the process crashes before a terminal write.
func (w *boxOperationsWorker) claim(ctx context.Context) ([]boxOperationRow, error) {
	rows, err := w.h.pool.Query(ctx, `
		UPDATE operations
		   SET status = $1, attempts = attempts + 1, updated_at = now()
		 WHERE id IN (
			SELECT id FROM operations
			 WHERE resource_kind = $3
			   AND (status = $2
			        OR (status = $1 AND updated_at < now() - ($5 * INTERVAL '1 second')))
			 ORDER BY created_at
			 LIMIT $4
			   FOR UPDATE SKIP LOCKED
		 )
		RETURNING id, action, payload, attempts`,
		string(models.OperationStatusReconciling), string(models.OperationStatusCreated),
		models.ResourceKindBox, boxOperationClaimBatch, boxOperationLease.Seconds(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ops []boxOperationRow
	for rows.Next() {
		var op boxOperationRow
		if err := rows.Scan(&op.ID, &op.Action, &op.Payload, &op.Attempts); err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// execute dispatches one claimed operation to its handler and writes the
// resulting status: Ready on success, Created (retried on the next tick, by
// any replica) on a failure still within budget, or Failed once the budget or
// an unimplemented action makes a retry pointless.
//
// The handler runs under a boxOperationTimeout deadline, but the status write
// deliberately runs on the parent: once the deadline fires, the derived
// context is dead, and writing the outcome through it would fail — leaving the
// row in Reconciling, which is exactly the wedge the deadline exists to end.
func (w *boxOperationsWorker) execute(parent context.Context, op boxOperationRow) {
	h := w.h
	ctx, cancel := context.WithTimeout(parent, w.opTimeout())
	defer cancel()
	var err error
	switch op.Action {
	case models.ActionBoxUp:
		err = h.executeBoxUp(ctx, op.Payload)
	case models.ActionDeleteBox:
		err = h.executeDeleteBox(ctx, op.Payload)
	case models.ActionSuspendBox:
		err = h.executeSuspendBox(ctx, op.Payload)
	case models.ActionResumeBox:
		err = h.executeResumeBox(ctx, op.Payload)
	default:
		err = fmt.Errorf("%w: %s", errBoxOperationUnimplemented, op.Action)
		log.Warn().Str("operation", op.ID.String()).Str("action", op.Action).
			Msg("box worker: unhandled box operation action")
	}

	if err == nil {
		w.markTerminal(parent, op.ID, models.OperationStatusReady, "")
		return
	}

	if errors.Is(err, errBoxOperationUnimplemented) || op.Attempts >= boxOperationMaxAttempts {
		w.markTerminal(parent, op.ID, models.OperationStatusFailed, err.Error())
		reason := "max_attempts"
		if errors.Is(err, errBoxOperationUnimplemented) {
			reason = "unimplemented_action"
		}
		recordOperationFailureAudit(parent, w.h.pool, op.ID, reason, err.Error())
		log.Warn().Err(err).Str("operation", op.ID.String()).Str("action", op.Action).
			Int("attempts", op.Attempts).Msg("box worker: operation failed terminally")
		return
	}
	w.markTerminal(parent, op.ID, models.OperationStatusCreated, err.Error())
	log.Warn().Err(err).Str("operation", op.ID.String()).Str("action", op.Action).
		Int("attempts", op.Attempts).Msg("box worker: operation failed, will retry")
}

// markTerminal writes an operation's status and error_message. Created is a
// legal value here too, for the retry case, where the write is terminal only
// for this tick.
func (w *boxOperationsWorker) markTerminal(ctx context.Context, id uuid.UUID, status models.OperationStatus, errMsg string) {
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE operations SET status = $2, error_message = $3, updated_at = now() WHERE id = $1`,
		id, string(status), errMsg); err != nil {
		log.Warn().Err(err).Str("operation", id.String()).Msg("box worker: failed to write operation status")
	}
}

// executeBoxUp brings a claimed box up through bootBoxInstance — the exact
// path the synchronous BoxUp door uses — and records the result on the box
// row. A box that moved on between enqueue and claim (already ready, or
// already being torn down) is treated as success: a stale operation on a box
// that is no longer pending is not a failure of the operation itself.
func (h *Handler) executeBoxUp(ctx context.Context, payload json.RawMessage) error {
	var p models.BoxUpPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode BoxUp payload: %w", err)
	}
	stack := h.boxStack
	if stack == nil {
		return errors.New("box runtime not configured")
	}
	b, err := h.loadBoxByID(ctx, p.BoxID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load box: %w", err)
	}
	if b.Status == models.BoxStatusDeleting || b.Status == models.BoxStatusDeleted ||
		b.Status == models.BoxStatusFailed || b.Status == models.BoxStatusReady || b.Status == models.BoxStatusIdle {
		return nil
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Booting', updated_at = now() WHERE id = $1`, p.BoxID); err != nil {
		return fmt.Errorf("mark box booting: %w", err)
	}

	res, mcpURL, sshHost, spawnErr := h.bootBoxInstance(ctx, stack, b.ProjectID, b, p.SSHPublicKey)
	if spawnErr != nil {
		h.failBox(context.Background(), p.BoxID, spawnErr.Error())
		return spawnErr
	}
	updated, err := h.markBoxReady(ctx, p.BoxID, res.Instance, mcpURL, sshHost)
	if err != nil {
		return fmt.Errorf("record ready box: %w", err)
	}

	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     b.ProjectID,
		EnvironmentID: b.EnvironmentID,
		Action:        models.ActionBoxUp,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"box_id": updated.ID, "instance_ref": res.Instance.InstanceRef,
			"pool": poolLabelFor(res.PoolHit), "time_to_ready_ms": res.Timeline.Total().Milliseconds(),
			"claimed_by": "box-operations-worker",
		},
	})
	return nil
}

// executeDeleteBox destroys a claimed box's instance and tombstones its row.
//
// revokeBoxSessions runs unconditionally here, not only for operations that
// came from the synchronous DeleteBox handler (which already revokes before
// enqueueing): the reaper's own delete path (box_reaper.go: enqueueDelete)
// never revoked sessions, so a box the reaper put to sleep for good kept every
// live credential valid until something else happened to withdraw it.
// revokeBoxSessions is idempotent, so calling it again for the handler's own
// operations is a no-op rather than a double-revoke.
func (h *Handler) executeDeleteBox(ctx context.Context, payload json.RawMessage) error {
	var p models.DeleteBoxPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode DeleteBox payload: %w", err)
	}
	b, err := h.loadBoxByID(ctx, p.BoxID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load box: %w", err)
	}
	if b.Status == models.BoxStatusDeleted {
		return nil
	}

	if _, err := h.revokeBoxSessions(ctx, p.BoxID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}

	if stack := h.boxStack; stack != nil && b.InstanceRef != "" {
		inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
		if err := stack.runtime.Destroy(ctx, inst); err != nil {
			return fmt.Errorf("destroy instance: %w", err)
		}
		stack.publishBoxPoolGauges()
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Deleted', deleted_at = now(), updated_at = now() WHERE id = $1`,
		p.BoxID); err != nil {
		return fmt.Errorf("mark box deleted: %w", err)
	}

	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     b.ProjectID,
		EnvironmentID: b.EnvironmentID,
		Action:        models.ActionDeleteBox,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata:      p,
	})
	return nil
}

// loadBoxByID loads a box by primary key, including deleted/tombstoned rows —
// unlike resolveBox, which is keyed by (project, name) and deliberately hides
// Deleted boxes from callers addressing a box by name. The worker operates by
// id and needs to see a Deleted row to make delete idempotent.
func (h *Handler) loadBoxByID(ctx context.Context, boxID uuid.UUID) (models.Box, error) {
	var b models.Box
	row := h.pool.QueryRow(ctx, `SELECT `+boxColumns+` FROM boxes WHERE id = $1`, boxID)
	if err := scanBox(row, &b); err != nil {
		return models.Box{}, err
	}
	return b, nil
}
