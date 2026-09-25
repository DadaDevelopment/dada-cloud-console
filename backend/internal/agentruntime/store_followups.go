package agentruntime

import (
	"context"

	"github.com/google/uuid"
)

// idleLadderStopped is an idle_step no follow-up ladder reaches.
const idleLadderStopped = 1 << 20

// followupStopper is the *pgStore surface that ends a conversation's
// follow-up ladder, reached by type assertion like PendingRuntimeMessages, so
// ConversationStore stays as it is.
type followupStopper interface {
	StopIdleLadder(ctx context.Context, id uuid.UUID) error
}

// StopIdleLadder moves the follow-up ladder past its last step, so no
// follow-up goes out in this idle period; the client's next message re-arms
// the ladder as always (ClearIdleFlag).
func (s *pgStore) StopIdleLadder(ctx context.Context, conversationID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE conversations SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('idle_step', $2::int)
		WHERE id = $1
	`, conversationID, idleLadderStopped)
	return err
}
