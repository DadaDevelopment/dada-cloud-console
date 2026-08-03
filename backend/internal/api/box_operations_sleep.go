package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// errBoxRuntimeCannotSleep is returned when the wired runtime does not implement
// box.Sleeper. It is deliberately terminal-shaped rather than retryable: a
// runtime that cannot put a box down will not learn how on attempt two, and a
// suspend that retries forever is a box that keeps burning the fleet quota while
// the queue pretends it is being handled.
var errBoxRuntimeCannotSleep = errors.New("the wired box runtime cannot suspend or resume a box")

// boxSleeper returns the wired runtime's sleep half, or an error naming the fact
// that this deployment has none.
func (h *Handler) boxSleeper() (box.Sleeper, error) {
	stack := h.boxStack
	if stack == nil {
		return nil, errors.New("box runtime not configured")
	}
	sleeper, ok := stack.runtime.(box.Sleeper)
	if !ok {
		return nil, errBoxRuntimeCannotSleep
	}
	return sleeper, nil
}

// executeSuspendBox releases a box's running body and keeps its workspace disk.
//
// Sleep is what makes the fleet quota survive real use: the reaper enqueues a
// SuspendBox for every box past its idle timeout or its hard TTL, and while
// nothing consumed those operations an abandoned box held a pod and a 20Gi
// volume until someone deleted it by hand — six of them and no one can create a
// box at all.
//
// slept_at IS THE WHOLE SLEEP CLOCK, and writing it here is not bookkeeping.
// reapSleeping selects on it, so a box put down without a stamp is not merely
// mislabelled — it is INVISIBLE to the only pass that can ever destroy it. It
// never gets a warning, it never gets deleted, and it holds its workspace volume
// forever while the meter bills every minute of it as suspended_disk. That is not
// a hypothetical: on 2026-08-04 the box fleet was 15.6% of the platform bill with
// no external demand and 96% of all metered box minutes were suspended_disk,
// because this UPDATE set the status and left the stamp NULL. The clock has to
// start at the moment the body is released, in the same statement that releases
// it, or it does not start at all.
//
// revokeBoxSessions runs unconditionally, for the same reason executeDeleteBox
// does it: the synchronous SuspendBox handler revokes before enqueueing, but the
// reaper's idle/TTL path and the spend cap never did, so a credential issued
// before the sleep would start working again the moment the box woke up. It is
// idempotent, so doing it twice for the handler's own operations is a no-op.
func (h *Handler) executeSuspendBox(ctx context.Context, payload json.RawMessage) error {
	var p models.SuspendBoxPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode SuspendBox payload: %w", err)
	}
	b, err := h.loadBoxByID(ctx, p.BoxID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load box: %w", err)
	}
	if b.Status == models.BoxStatusSleeping || b.Status == models.BoxStatusDeleting ||
		b.Status == models.BoxStatusDeleted || b.Status == models.BoxStatusFailed {
		return nil
	}
	sleeper, err := h.boxSleeper()
	if err != nil {
		return err
	}
	if _, err := h.revokeBoxSessions(ctx, p.BoxID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if b.InstanceRef != "" {
		inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
		if err := sleeper.Suspend(ctx, inst); err != nil {
			return fmt.Errorf("suspend instance: %w", err)
		}
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Sleeping', slept_at = now(), updated_at = now() WHERE id = $1`,
		p.BoxID); err != nil {
		return fmt.Errorf("mark box sleeping: %w", err)
	}

	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     b.ProjectID,
		EnvironmentID: b.EnvironmentID,
		Action:        models.ActionSuspendBox,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"box_id": p.BoxID, "reason": p.Reason, "claimed_by": "box-operations-worker",
		},
	})
	return nil
}

// executeResumeBox rebuilds a body around a sleeping box's surviving disk and
// puts it back to work.
//
// Resume is not the reverse of Suspend at the runtime layer alone. What survives
// a sleep is the workspace volume; the tenant's environment file, its authorized
// key, the pod's box-name label and the pod IP do not, because they live in a
// container that no longer exists. So this rebinds identity, re-programs the
// network, and only then calls a box ready — proven the same way a fresh box is,
// by executing the canary inside it, because a woken box whose toolchain is not
// where it was is a box the customer would discover broken rather than be told
// about.
//
// Exposures are republished for the same reason: the ingress NetworkPolicy
// selects dada.io/box-exposed on the POD, and a pod that was just recreated does
// not carry it. Without this a box that slept and woke keeps a hostname that
// answers 504 forever. Expose is idempotent, so republishing costs a patch.
//
// expires_at is written rather than coalesced. markBoxReady deliberately keeps
// the first deadline a box was given, which is right for a boot and wrong here:
// a box resumed after its TTL would come back already expired and be put
// straight back to sleep by the next reaper pass.
//
// The sleep clock and BOTH warning stamps are cleared for the same reason, and
// the warnings are the dangerous half. A box that slept for 67 hours has already
// been told twice that it is about to be destroyed; waking it must retract those,
// because a stamp that survives the wake means the next sleep starts with its two
// warnings already spent and the box is deleted six hours in, with nothing sent
// and the customer's only notice being a prototype that is gone. Clearing them
// here is what makes "warned twice before deletion" true per sleep rather than
// once per lifetime.
func (h *Handler) executeResumeBox(ctx context.Context, payload json.RawMessage) error {
	var p models.ResumeBoxPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode ResumeBox payload: %w", err)
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
	if b.Status == models.BoxStatusReady || b.Status == models.BoxStatusIdle {
		return nil
	}
	if b.Status != models.BoxStatusSleeping {
		return nil
	}
	sleeper, err := h.boxSleeper()
	if err != nil {
		return err
	}
	if b.InstanceRef == "" {
		return errors.New("cannot resume a box that never had an instance")
	}

	spec := box.Spec{
		Image:        b.Image,
		Profile:      b.Profile,
		Region:       b.Region,
		SSHPublicKey: p.SSHPublicKey,
		Env: map[string]string{
			"BOX_NAME":       b.Name,
			"BOX_PROJECT_ID": b.ProjectID.String(),
			"BOX_ENV_ID":     b.EnvironmentID.String(),
		},
	}
	inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
	if err := sleeper.Resume(ctx, inst, spec); err != nil {
		return fmt.Errorf("resume instance: %w", err)
	}
	if err := stack.runtime.Bind(ctx, inst, spec); err != nil {
		return fmt.Errorf("rebind resumed box: %w", err)
	}
	if err := stack.runtime.ProgramNetwork(ctx, inst); err != nil {
		return fmt.Errorf("program resumed box network: %w", err)
	}
	if restarter, ok := stack.runtime.(box.ServiceRestarter); ok {
		if err := restarter.RestartServices(ctx, inst); err != nil {
			log.Warn().Err(err).Str("box", b.Name).
				Msg("box: the box is awake but not everything it declared came back up")
		}
	}
	canary, err := stack.runtime.Exec(ctx, inst, box.CanaryCommand)
	if err != nil {
		return fmt.Errorf("canary a resumed box: %w", err)
	}
	if err := box.EvaluateReadiness(canary); err != nil {
		return fmt.Errorf("resumed box is not ready: %w", err)
	}

	mcpURL := fmt.Sprintf("%s/api/v1/box/session/mcp", stack.sessions)
	if addr, err := h.openBoxDoor(ctx, stack, inst, b.ID, b.Name); err != nil {
		logDoorFailure(b.Name, err)
	} else {
		mcpURL = box.BrokerMCPURL(addr)
	}
	sshHost := inst.SSHHost
	if sshHost == "" {
		sshHost = inst.NodeRef
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE boxes
		    SET status               = 'Ready',
		        instance_ref         = $2,
		        node_ref             = $3,
		        ssh_host             = $4,
		        mcp_url              = $5,
		        error_message        = '',
		        last_active_at       = now(),
		        slept_at             = NULL,
		        reap_warned_at       = NULL,
		        reap_final_warned_at = NULL,
		        expires_at           = now() + (ttl_seconds * INTERVAL '1 second'),
		        updated_at           = now()
		  WHERE id = $1`,
		b.ID, inst.InstanceRef, inst.NodeRef, sshHost, mcpURL); err != nil {
		return fmt.Errorf("record resumed box: %w", err)
	}
	h.republishBoxExposures(ctx, stack, b)

	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     b.ProjectID,
		EnvironmentID: b.EnvironmentID,
		Action:        models.ActionResumeBox,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"box_id": p.BoxID, "instance_ref": inst.InstanceRef, "claimed_by": "box-operations-worker",
		},
	})
	return nil
}

// republishBoxExposures re-applies every live exposure of a box to the pod that
// was just rebuilt, so the ingress path finds it again.
//
// Failures are logged rather than returned: the box itself is awake and usable,
// and failing the resume operation over a hostname would retry the whole wake —
// including a second pod delete — to fix a patch. A box whose exposure did not
// come back is visible as a 504 and as this log line.
func (h *Handler) republishBoxExposures(ctx context.Context, stack *boxRuntimeStack, b models.Box) {
	if stack.exposer == nil {
		return
	}
	rows, err := h.pool.Query(ctx,
		`SELECT port FROM box_exposures WHERE box_id = $1 AND withdrawn_at IS NULL`, b.ID)
	if err != nil {
		log.Warn().Err(err).Str("box", b.Name).Msg("box: cannot read exposures of a resumed box")
		return
	}
	defer rows.Close()
	var ports []int
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			log.Warn().Err(err).Str("box", b.Name).Msg("box: cannot read an exposure of a resumed box")
			return
		}
		ports = append(ports, port)
	}
	if err := rows.Err(); err != nil {
		log.Warn().Err(err).Str("box", b.Name).Msg("box: cannot read exposures of a resumed box")
		return
	}
	for _, port := range ports {
		if _, err := stack.exposer.Expose(b.Name, port); err != nil {
			log.Warn().Err(err).Str("box", b.Name).Int("port", port).
				Msg("box: republishing an exposure of a resumed box failed; the hostname will answer 504")
		}
	}
}
