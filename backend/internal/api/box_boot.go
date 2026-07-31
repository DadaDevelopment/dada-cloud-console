package api

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// bootBoxInstance is the one ready path: claim a warm body from the pool, bind
// tenant identity to it, program its network, thaw it, run the canary, and
// open its own door. Both doors that bring a box up run through this exact
// call — the synchronous BoxUp handler (boxes_up.go) and the box operations
// worker's ActionBoxUp handling (box_operations_worker.go) for the async
// CreateBox door. Neither is allowed its own copy: a second version of "what
// does ready mean" is a second place for the two doors to disagree about it.
//
// stack.publishBoxPoolGauges is called regardless of outcome, because a claim
// (hit or miss) changes the pool's numbers either way.
//
// The returned mcpURL is the BOX's own endpoint, relayed verbatim rather than
// interpreted: cmd/box-broker runs inside the box and publishes the address it
// actually bound, so the URL is read out of the box. When there is no broker
// (openBoxDoor fails) it falls back to the control-plane surface, and
// boxConnectBlock is what tells the caller that surface is degraded.
func (h *Handler) bootBoxInstance(ctx context.Context, stack *boxRuntimeStack, projectID uuid.UUID, b models.Box, sshPublicKey string) (*box.SpawnResult, string, string, error) {
	spec := box.Spec{
		Image:        b.Image,
		Profile:      b.Profile,
		Region:       b.Region,
		SSHPublicKey: sshPublicKey,
		Env: map[string]string{
			"BOX_NAME":       b.Name,
			"BOX_PROJECT_ID": projectID.String(),
			"BOX_ENV_ID":     b.EnvironmentID.String(),
		},
	}
	res, spawnErr := box.Spawn(ctx, box.Deps{
		Clock:   box.SystemClock{},
		Admit:   box.AllowAll{},
		Pool:    stack.pool,
		Runtime: stack.runtime,
	}, spec)
	stack.publishBoxPoolGauges()
	if spawnErr != nil {
		return res, "", "", spawnErr
	}

	inst := res.Instance
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
	return res, mcpURL, sshHost, nil
}

// markBoxReady writes a booted instance's coordinates onto the box row and
// flips it to Ready. Shared by the same two callers as bootBoxInstance.
func (h *Handler) markBoxReady(ctx context.Context, boxID uuid.UUID, inst *box.Instance, mcpURL, sshHost string) (models.Box, error) {
	var updated models.Box
	row := h.pool.QueryRow(ctx,
		`UPDATE boxes
		    SET status         = 'Ready',
		        instance_ref   = $2,
		        node_ref       = $3,
		        ssh_host       = $4,
		        mcp_url         = $5,
		        error_message  = '',
		        last_active_at = now(),
		        expires_at     = COALESCE(expires_at, now() + (ttl_seconds * INTERVAL '1 second')),
		        updated_at     = now()
		  WHERE id = $1
		 RETURNING `+boxColumns,
		boxID, inst.InstanceRef, inst.NodeRef, sshHost, mcpURL,
	)
	if err := scanBox(row, &updated); err != nil {
		return models.Box{}, err
	}
	return updated, nil
}
