package api

import (
	"context"
	"strings"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Opening the box's own door.
//
// D6 says the customer's agent talks to the BOX, not to us: their source, their
// prompts and their model credentials must not traverse our control plane. This
// file is the control plane's whole part in that — push the digests of the box's
// live sessions into the box, start cmd/box-broker there, and publish the address
// the broker actually bound. After that the control plane is off the path of every
// command, which is the point.
//
// It runs AFTER box.Spawn returns, never inside it. The ready path is the one thing
// the product's headline number measures and its shape is pinned by
// readypath_golden_test.go; a serial step added there is a latency regression by
// construction. A box is ready when a command has executed inside it — the door it
// will be reached through afterwards is not part of that fact.

// liveBoxSessionDigests returns the sha256 digests of every session that can still
// open this box.
//
// The set is the query, not an accumulated list, and that is what makes the digest
// file inside the box a mirror rather than a log: a session that expired or was
// revoked simply is not in the next answer, so pushing this set is also how a
// revocation lands.
// The expiry is carried along rather than left implicit: the box enforces it
// against its own clock, so a session that simply lapses stops opening the door
// without anything having to call anything.
func (h *Handler) liveBoxSessionDigests(ctx context.Context, boxID uuid.UUID) ([]box.SessionDigest, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT token_hash, expires_at FROM box_sessions
		  WHERE box_id = $1 AND revoked_at IS NULL AND expires_at > now()`, boxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var digests []box.SessionDigest
	for rows.Next() {
		var d box.SessionDigest
		if err := rows.Scan(&d.Hash, &d.ExpiresAt); err != nil {
			return nil, err
		}
		digests = append(digests, d)
	}
	return digests, rows.Err()
}

// syncBoxDoor makes the box's digest file match the live session set.
//
// Called after minting a session and after revoking one. A box with no broker is
// not an error here: there is no door to keep in sync, and the caller has already
// been told the box has none.
func (h *Handler) syncBoxDoor(ctx context.Context, stack *boxRuntimeStack, inst *box.Instance, boxID uuid.UUID) error {
	if stack == nil || !stack.runtime.BrokerConfigured() || inst == nil || inst.InstanceRef == "" {
		return nil
	}
	digests, err := h.liveBoxSessionDigests(ctx, boxID)
	if err != nil {
		return err
	}
	return stack.runtime.InstallSessionDigests(ctx, inst, digests)
}

// openBoxDoor installs the box's credentials and starts its endpoint, returning the
// address the broker bound.
//
// The digests go in BEFORE the broker starts. The other order has a window in which
// a listener is up with an empty credential file, and a door that is open and
// refuses everyone is indistinguishable in a log from a door that is open and
// refuses the right people.
func (h *Handler) openBoxDoor(ctx context.Context, stack *boxRuntimeStack, inst *box.Instance, boxID uuid.UUID, boxName string) (string, error) {
	if !stack.runtime.BrokerConfigured() {
		return "", box.ErrNoBroker
	}
	if err := h.syncBoxDoor(ctx, stack, inst, boxID); err != nil {
		return "", err
	}
	return stack.runtime.StartBroker(ctx, inst, boxName)
}

// controlPlaneMCPPath is the path of the control-plane fallback surface. It is
// matched rather than remembered so "is this URL the box's own?" has one answer
// derived from the URL itself.
const controlPlaneMCPPath = "/api/v1/box/session/mcp"

// boxOwnsItsMCPURL reports whether a box's published MCP URL is the box's own
// endpoint rather than the control-plane fallback.
//
// This is what the connect block's `available` flag is computed from, and it is
// computed rather than asserted on purpose: the previous version of that flag was a
// hardcoded false, which was true when written and would have quietly stayed false
// after the broker landed.
func boxOwnsItsMCPURL(mcpURL string) bool {
	return mcpURL != "" && !strings.Contains(mcpURL, controlPlaneMCPPath)
}

// logDoorFailure records that a box came up without its own endpoint.
//
// Warn and not error: the box is usable through the named fallback, so this is a
// degraded box rather than a failed one. It is logged at all because a fleet
// silently falling back to the control plane would be D6 eroding without a single
// failing test.
func logDoorFailure(boxName string, err error) {
	log.Warn().Err(err).Str("box", boxName).
		Msg("box: the box has no endpoint of its own; the control-plane session surface is the fallback (D6)")
}
