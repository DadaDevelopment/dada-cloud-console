package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// StateSync mirrors the runtime-owned conversation state into one CRM note per
// conversation. It is service-owned, never an LLM effect: the push runs after
// the turn, a failed push is recorded in the receipt and retried on the next
// turn, and CRM downtime never blocks the dialogue.
type StateSync struct {
	store                  *pgStore
	endpoint, token, agent string
	client                 *http.Client
	locks                  [64]sync.Mutex
}

func stateSyncFromEnv(store *pgStore) *StateSync {
	endpoint := os.Getenv("AGENT_STATE_CRM_URL")
	if endpoint == "" {
		return nil
	}
	return &StateSync{store: store, endpoint: endpoint, token: os.Getenv("AGENT_PAUSE_CRM_TOKEN"), agent: os.Getenv("AGENT_CONTACT_CRM_AGENT"), client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type stateReceipt struct {
	Version     int64     `json:"version"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary"`
	AttemptedAt time.Time `json:"attempted_at"`
}

// Push sends the snapshot when it is newer than the last confirmed one or the
// operator summary changed. An empty summary keeps the one from the previous
// receipt, so the hand-off text written at escalation survives later turns.
func (s *StateSync) Push(ctx context.Context, conv Conversation, state RuntimeState, summary string) error {
	if conv.AgentName != s.agent || conv.Channel != "telegram" {
		return nil
	}
	lock := &s.locks[int(conv.ID[0])%len(s.locks)]
	for !lock.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer lock.Unlock()
	var receipt []byte
	if err := s.store.pool.QueryRow(ctx, `SELECT COALESCE(metadata->'crm_state_sync','{}'::jsonb) FROM conversations WHERE id=$1`, conv.ID).Scan(&receipt); err != nil {
		return err
	}
	var prior stateReceipt
	if err := json.Unmarshal(receipt, &prior); err != nil {
		return err
	}
	if summary == "" {
		summary = prior.Summary
	}
	if prior.Status == "completed" && prior.Version >= state.Version && prior.Summary == summary {
		return nil
	}
	status := "completed"
	if err := s.send(ctx, conv, state, summary); err != nil {
		status = "failed"
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Int64("version", state.Version).Msg("agentruntime: CRM state note not updated")
	}
	value, _ := json.Marshal(stateReceipt{Version: state.Version, Status: status, Summary: summary, AttemptedAt: time.Now().UTC()})
	_, err := s.store.pool.Exec(ctx, `UPDATE conversations SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{crm_state_sync}',$2::jsonb,true) WHERE id=$1`, conv.ID, value)
	return err
}

func (s *StateSync) send(ctx context.Context, conv Conversation, state RuntimeState, summary string) error {
	if len(s.token) < 32 || strings.TrimSpace(s.agent) == "" {
		return fmt.Errorf("state integration not configured")
	}
	facts, loops := state.ReportedFacts, state.OpenLoops
	if facts == nil {
		facts = map[string]ReportedFact{}
	}
	if loops == nil {
		loops = map[string]OpenLoop{}
	}
	body, _ := json.Marshal(map[string]any{
		"conversation_id": conv.ID, "agent_name": conv.AgentName, "channel": conv.Channel, "external_id": conv.ExternalID, "username": conv.ActorUsername,
		"state":   map[string]any{"version": state.Version, "agent_enabled": state.AgentEnabled, "pause_reason": state.PauseReason, "reported_facts": facts, "open_loops": loops},
		"summary": summary, "updated_at": time.Now().UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("state integration unavailable")
	}
	defer resp.Body.Close()
	var result struct {
		Applied bool   `json:"applied"`
		NoteID  string `json:"note_id"`
		Error   string `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result) != nil || resp.StatusCode != http.StatusOK || !result.Applied {
		return fmt.Errorf("state note not confirmed: %d %s", resp.StatusCode, result.Error)
	}
	if id, err := uuid.Parse(result.NoteID); err != nil || id == uuid.Nil {
		return fmt.Errorf("invalid state receipt")
	}
	return nil
}

// mirrorState pushes the snapshot off the request path. The receipt carries
// the retry to the next turn, so nothing here is awaited by the caller.
func (r *Runtime) mirrorState(ctx context.Context, conv Conversation, state RuntimeState, summary string) {
	if r.stateSync == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := r.stateSync.Push(ctx, conv, state, summary); err != nil {
			log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: CRM state receipt not saved")
		}
	}()
}
