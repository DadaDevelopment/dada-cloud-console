package agentruntime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// syncPausedCRM is shared by the stop tool and the service retry loop. The
// durable pause always precedes this call; no integration outcome enables it.
func (s *Server) syncPausedCRM(ctx context.Context, conv Conversation) (RuntimeState, error) {
	lock := &s.pauseSyncLocks[int(conv.ID[0])%len(s.pauseSyncLocks)]
	for !lock.TryLock() {
		select {
		case <-ctx.Done():
			return RuntimeState{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer lock.Unlock()
	state, err := s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || state.AgentEnabled || state.CRMStatusSync == "completed" {
		return state, err
	}
	if store, ok := s.runtime.store.(*pgStore); ok {
		// Also touch repeated failed attempts: MarkPauseCRMSync deliberately
		// does not mutate an unchanged status. This durable ordering survives
		// restarts and prevents a failing first batch starving later rows.
		_, err = store.pool.Exec(ctx, `UPDATE conversation_runtime_state SET updated_at=now()
WHERE conversation_id=$1 AND NOT agent_enabled AND crm_status_sync IN ('pending','failed')`, conv.ID)
		if err != nil {
			return state, err
		}
	}
	syncStatus := "completed"
	if s.pauseCRM == nil || s.pauseCRM.SetPaused(ctx, conv, state.PauseReason) != nil {
		syncStatus = "failed"
	}
	return s.runtime.states.MarkPauseCRMSync(ctx, conv.ID, syncStatus)
}

// ReconcilePaused retries only persisted pending/failed CRM status writes. It
// never invokes an agent, sends a customer message or changes agent_enabled.
// The service runs one loop; external status SET is idempotent across restarts.
func (s *Server) ReconcilePaused(ctx context.Context, limit int) (int, error) {
	store, ok := s.runtime.store.(*pgStore)
	if !ok {
		return 0, errors.New("pause retry requires PostgreSQL state")
	}
	if limit < 1 || limit > 5 {
		limit = 5
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	rows, err := store.pool.Query(ctx, `SELECT conversation_id FROM conversation_runtime_state
WHERE NOT agent_enabled AND crm_status_sync IN ('pending','failed')
ORDER BY updated_at, conversation_id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	ids := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	attempted := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return attempted, err
		}
		conv, err := store.GetConversation(ctx, id)
		if err != nil {
			return attempted, err
		}
		attempted++
		if _, err := s.syncPausedCRM(ctx, conv); err != nil {
			return attempted, err
		}
	}
	return attempted, nil
}
