package agentruntime

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Refusal counters (plan 2026-09-25, Q5/Q6): runtime-owned, in
// conversations.metadata next to questions_in_row, for the same reason --
// they exist to limit the model, so the model must have no way to write
// them. Whether a turn is a refusal, and for which reason, is the check's
// refusal_* signal; no text is compared here.
const (
	// obstacleRefusalsKey is map refusal signal id -> how many inputs
	// carried it.
	obstacleRefusalsKey = "obstacle_refusals"
	// obstacleRefusalMsgsKey is map refusal signal id -> sorted message_ids
	// of the last counted input: attempt 1 and silence recovery see the same
	// pending input, and the same input must not count twice.
	obstacleRefusalMsgsKey = "obstacle_refusal_msgs"
	// refusalsPausedKey marks a pause taken while the counters run; the
	// first turn after the pause is lifted resets the counters.
	refusalsPausedKey = "obstacle_refusals_paused"
	// refusalHandoffAtDefault is AGENT_RUNTIME_REFUSAL_HANDOFF_AT unset: the
	// second refusal for one reason hands off (Q5).
	refusalHandoffAtDefault = 2
	// idleLadderStopped is an idle_step no ladder reaches.
	idleLadderStopped = 1 << 20
)

// counterStore is the *pgStore surface of store_counters.go, reached by type
// assertion like PendingRuntimeMessages: ConversationStore stays as it is.
type counterStore interface {
	RecordRefusal(ctx context.Context, id uuid.UUID, key string, msgIDs []string) (int, error)
	MarkRefusalsPaused(ctx context.Context, id uuid.UUID) error
	ResetRefusalsAfterPause(ctx context.Context, id uuid.UUID) (bool, error)
	StopIdleLadder(ctx context.Context, id uuid.UUID) error
}

// markRefusalsPaused records a pause for resetRefusalsAfterResume; nothing
// is written while the counters are off.
func (r *Runtime) markRefusalsPaused(ctx context.Context, conv Conversation) {
	if !r.flags.ScriptCounters {
		return
	}
	store, ok := r.store.(counterStore)
	if !ok {
		return
	}
	if err := store.MarkRefusalsPaused(ctx, conv.ID); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: refusal pause mark not recorded")
	}
}
