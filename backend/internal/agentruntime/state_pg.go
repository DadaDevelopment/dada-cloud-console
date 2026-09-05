package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var _ StateStore = (*pgStore)(nil)

const runtimeStateColumns = `version, agent_enabled, reported_facts, open_loops, active_skills, pause_reason, crm_status_sync`

func scanRuntimeState(row rowScanner) (RuntimeState, error) {
	var state RuntimeState
	err := row.Scan(&state.Version, &state.AgentEnabled, &state.ReportedFacts, &state.OpenLoops,
		&state.ActiveSkills, &state.PauseReason, &state.CRMStatusSync)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrConversationNotFound
	}
	return state, err
}

// State rows are lazy-created only for an existing conversation. This also
// serializes two callers' first write through the unique conversation key.
func ensureRuntimeState(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO conversation_runtime_state(conversation_id)
 SELECT id FROM conversations WHERE id=$1 ON CONFLICT (conversation_id) DO NOTHING`, id)
	return err
}

func (s *pgStore) GetState(ctx context.Context, id uuid.UUID) (RuntimeState, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeState{}, err
	}
	defer tx.Rollback(ctx)
	if err = ensureRuntimeState(ctx, tx, id); err != nil {
		return RuntimeState{}, err
	}
	state, err := scanRuntimeState(tx.QueryRow(ctx, `SELECT `+runtimeStateColumns+` FROM conversation_runtime_state WHERE conversation_id=$1`, id))
	if err != nil {
		return RuntimeState{}, err
	}
	return state, tx.Commit(ctx)
}

// All system and model writes share this lock, so pausing cannot be lost to a
// concurrent facts patch. No caller can replace the entire state document.
func (s *pgStore) mutateState(ctx context.Context, id uuid.UUID, mutate func(pgx.Tx, *RuntimeState) (bool, error)) (RuntimeState, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeState{}, err
	}
	defer tx.Rollback(ctx)
	if err = ensureRuntimeState(ctx, tx, id); err != nil {
		return RuntimeState{}, err
	}
	state, err := scanRuntimeState(tx.QueryRow(ctx, `SELECT `+runtimeStateColumns+` FROM conversation_runtime_state WHERE conversation_id=$1 FOR UPDATE`, id))
	if err != nil {
		return RuntimeState{}, err
	}
	changed, err := mutate(tx, &state)
	if err != nil {
		return RuntimeState{}, err
	}
	if changed {
		facts, err := json.Marshal(state.ReportedFacts)
		if err != nil {
			return RuntimeState{}, err
		}
		loops, err := json.Marshal(state.OpenLoops)
		if err != nil {
			return RuntimeState{}, err
		}
		skills, err := json.Marshal(state.ActiveSkills)
		if err != nil {
			return RuntimeState{}, err
		}
		state.Version++
		_, err = tx.Exec(ctx, `UPDATE conversation_runtime_state SET version=$2, agent_enabled=$3,
   reported_facts=$4, open_loops=$5, active_skills=$6, pause_reason=$7, crm_status_sync=$8, updated_at=now()
   WHERE conversation_id=$1`, id, state.Version, state.AgentEnabled, facts, loops, skills, state.PauseReason, state.CRMStatusSync)
		if err != nil {
			return RuntimeState{}, err
		}
	}
	return state, tx.Commit(ctx)
}

func (s *pgStore) ApplyState(ctx context.Context, id uuid.UUID, version int64, patch StatePatch) (RuntimeState, error) {
	if err := validateStatePatch(patch); err != nil {
		return RuntimeState{}, err
	}
	return s.mutateState(ctx, id, func(tx pgx.Tx, state *RuntimeState) (bool, error) {
		if state.Version != version {
			return false, ErrStateConflict
		}
		evidence := map[uuid.UUID]bool{}
		for _, fact := range patch.ReportedFacts {
			evidence[fact.SourceMessageID] = true
		}
		for _, loop := range patch.OpenLoops {
			evidence[loop.SourceMessageID] = true
		}
		for sourceID := range evidence {
			var source uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM conversation_messages WHERE id=$1 AND conversation_id=$2 AND role='user' FOR SHARE`, sourceID, id).Scan(&source)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, ErrInvalidStateEvidence
			}
			if err != nil {
				return false, err
			}
		}
		if err := mergeStatePatch(state, patch); err != nil {
			return false, err
		}
		return len(patch.ReportedFacts)+len(patch.OpenLoops) != 0, nil
	})
}

func (s *pgStore) ActivateSkill(ctx context.Context, id uuid.UUID, name, content, digest string) (RuntimeState, error) {
	if err := validateActiveSkill(name, content, digest); err != nil {
		return RuntimeState{}, err
	}
	return s.mutateState(ctx, id, func(_ pgx.Tx, state *RuntimeState) (bool, error) {
		skill := ActiveSkill{Content: content, Digest: digest}
		if existing, ok := state.ActiveSkills[name]; ok && existing == skill {
			return false, nil
		}
		if _, exists := state.ActiveSkills[name]; !exists && len(state.ActiveSkills) >= MaxActiveSkills {
			return false, fmt.Errorf("%w: too many active skills", ErrInvalidStatePatch)
		}
		state.ActiveSkills[name] = skill
		return true, nil
	})
}

func (s *pgStore) PauseAgent(ctx context.Context, id uuid.UUID, reason string) (RuntimeState, error) {
	if !boundedStateText(reason, 1024) {
		return RuntimeState{}, fmt.Errorf("%w: invalid pause reason", ErrInvalidStatePatch)
	}
	return s.mutateState(ctx, id, func(_ pgx.Tx, state *RuntimeState) (bool, error) {
		if !state.AgentEnabled {
			return false, nil
		}
		state.AgentEnabled, state.PauseReason, state.CRMStatusSync = false, reason, "pending"
		return true, nil
	})
}

func (s *pgStore) MarkPauseCRMSync(ctx context.Context, id uuid.UUID, status string) (RuntimeState, error) {
	if status != "pending" && status != "completed" && status != "failed" {
		return RuntimeState{}, fmt.Errorf("%w: invalid CRM sync status", ErrInvalidStatePatch)
	}
	return s.mutateState(ctx, id, func(_ pgx.Tx, state *RuntimeState) (bool, error) {
		if state.AgentEnabled {
			return false, fmt.Errorf("%w: agent is not paused", ErrInvalidStatePatch)
		}
		// Successful external synchronization is terminal; a delayed failed retry
		// must not overwrite it. PauseAgent itself is idempotent and never retries CRM.
		if state.CRMStatusSync == "completed" || state.CRMStatusSync == status {
			return false, nil
		}
		state.CRMStatusSync = status
		return true, nil
	})
}
