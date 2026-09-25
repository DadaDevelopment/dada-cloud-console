package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordRefusal adds one to metadata.obstacle_refusals[key] and returns the
// count after the statement. Every message id a refusal was counted on is
// kept per key in obstacle_refusal_msgs (a set, grown on each count); a call
// whose non-empty message_ids meet that set for the same key (attempt 1 of
// the same turn, silence recovery replaying it, a replay that picked up one
// more client message) leaves everything as it is and returns the current
// count. Two reasons signalled on one input are two keys and both count.
// Dedup and increment are one UPDATE, so a replay racing the original cannot
// count twice either.
func (s *pgStore) RecordRefusal(ctx context.Context, conversationID uuid.UUID, key string, msgIDs []string) (int, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, errors.New("record refusal: empty key")
	}
	ids := append([]string{}, msgIDs...)
	sort.Strings(ids)
	rawIDs, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	seen := `CASE WHEN jsonb_typeof(metadata->'` + obstacleRefusalMsgsKey + `'->$2::text) = 'array' THEN metadata->'` + obstacleRefusalMsgsKey + `'->$2::text ELSE '[]'::jsonb END`
	var count int
	err = s.pool.QueryRow(ctx, `
		UPDATE conversations SET metadata = CASE
			WHEN jsonb_array_length($3::jsonb) > 0 AND (`+seen+`) ?| ARRAY(SELECT jsonb_array_elements_text($3::jsonb)) THEN metadata
			ELSE jsonb_set(jsonb_set(COALESCE(metadata, '{}'::jsonb), '{`+obstacleRefusalsKey+`}',
					CASE WHEN jsonb_typeof(metadata->'`+obstacleRefusalsKey+`') = 'object' THEN metadata->'`+obstacleRefusalsKey+`' ELSE '{}'::jsonb END
					|| jsonb_build_object($2::text,
						CASE WHEN jsonb_typeof(metadata->'`+obstacleRefusalsKey+`'->$2::text) = 'number'
							THEN floor((metadata->'`+obstacleRefusalsKey+`'->>$2::text)::numeric)::int ELSE 0 END + 1), true),
				'{`+obstacleRefusalMsgsKey+`}',
					CASE WHEN jsonb_typeof(metadata->'`+obstacleRefusalMsgsKey+`') = 'object' THEN metadata->'`+obstacleRefusalMsgsKey+`' ELSE '{}'::jsonb END
					|| jsonb_build_object($2::text, (`+seen+`) || $3::jsonb), true)
			END
		WHERE id = $1
		RETURNING CASE WHEN jsonb_typeof(metadata->'`+obstacleRefusalsKey+`'->$2::text) = 'number'
			THEN floor((metadata->'`+obstacleRefusalsKey+`'->>$2::text)::numeric)::int ELSE 0 END
	`, conversationID, key, string(rawIDs)).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("record refusal: conversation %s not found", conversationID)
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

// MarkRefusalsPaused notes that the conversation was paused while the
// refusal counters run, so the turn after an operator lifts the pause can
// start them over.
func (s *pgStore) MarkRefusalsPaused(ctx context.Context, conversationID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE conversations SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('`+refusalsPausedKey+`', true)
		WHERE id = $1
	`, conversationID)
	return err
}

// ResetRefusalsAfterPause drops the refusal counters and the pause mark when
// the mark is there, and reports whether it was.
func (s *pgStore) ResetRefusalsAfterPause(ctx context.Context, conversationID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE conversations SET metadata = metadata - '`+obstacleRefusalsKey+`' - '`+obstacleRefusalMsgsKey+`' - '`+refusalsPausedKey+`'
		WHERE id = $1 AND metadata ? '`+refusalsPausedKey+`'
	`, conversationID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
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
