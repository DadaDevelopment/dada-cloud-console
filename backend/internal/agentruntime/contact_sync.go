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
)

// ContactSync is service-owned, never an LLM effect. Only conversations that
// actually receive input enter the durable retry queue; no historical backfill.
type ContactSync struct {
	store                  *pgStore
	endpoint, token, agent string
	client                 *http.Client
	locks                  [64]sync.Mutex
}

func contactSyncFromEnv(store *pgStore) *ContactSync {
	endpoint := os.Getenv("AGENT_CONTACT_CRM_URL")
	if endpoint == "" {
		return nil
	}
	return &ContactSync{store: store, endpoint: endpoint, token: os.Getenv("AGENT_PAUSE_CRM_TOKEN"), agent: os.Getenv("AGENT_CONTACT_CRM_AGENT"), client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (s *ContactSync) Ensure(ctx context.Context, conv Conversation) error {
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
	// Reload receipt under the lock; a retry loop and inbound call can overlap.
	var receipt []byte
	if err := s.store.pool.QueryRow(ctx, `SELECT COALESCE(metadata->'crm_contact_sync','{}'::jsonb) FROM conversations WHERE id=$1`, conv.ID).Scan(&receipt); err != nil {
		return err
	}
	var prior struct {
		Status   string `json:"status"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(receipt, &prior); err != nil {
		return err
	}
	if prior.Status == "completed" && prior.Username == conv.ActorUsername {
		return nil
	}
	if err := s.save(ctx, conv, "pending", ""); err != nil {
		return err
	}
	pid, callErr := s.create(ctx, conv)
	status := "completed"
	if callErr != nil {
		status = "failed"
	}
	// Cancellation leaves the already persisted pending receipt for the worker.
	if err := s.save(ctx, conv, status, pid); err != nil {
		return err
	}
	// CRM downtime must not pause the dialogue. Persisted failure is retried.
	return nil
}
func (s *ContactSync) save(ctx context.Context, conv Conversation, status, pid string) error {
	value, _ := json.Marshal(map[string]any{"status": status, "person_id": pid, "username": conv.ActorUsername, "attempted_at": time.Now().UTC()})
	_, err := s.store.pool.Exec(ctx, `UPDATE conversations SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{crm_contact_sync}',$2::jsonb,true) WHERE id=$1`, conv.ID, value)
	return err
}
func (s *ContactSync) create(ctx context.Context, conv Conversation) (string, error) {
	if len(s.token) < 32 || strings.TrimSpace(s.agent) == "" {
		return "", fmt.Errorf("contact integration not configured")
	}
	firstName, _ := conv.ActorMetadata["first_name"].(string)
	body, _ := json.Marshal(map[string]any{"conversation_id": conv.ID, "agent_name": conv.AgentName, "channel": conv.Channel, "external_id": conv.ExternalID, "username": conv.ActorUsername, "first_name": firstName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("contact integration unavailable")
	}
	defer resp.Body.Close()
	var result struct {
		Applied  bool   `json:"applied"`
		PersonID string `json:"person_id"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result) != nil || !result.Applied {
		return "", fmt.Errorf("contact not confirmed")
	}
	if id, err := uuid.Parse(result.PersonID); err != nil || id == uuid.Nil {
		return "", fmt.Errorf("invalid contact receipt")
	}
	return result.PersonID, nil
}
func (s *Server) ReconcileContacts(ctx context.Context, limit int) (int, error) {
	syncer := s.runtime.contacts
	if syncer == nil {
		return 0, nil
	}
	if limit < 1 || limit > 5 {
		limit = 5
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	rows, err := syncer.store.pool.Query(ctx, `SELECT id FROM conversations WHERE agent_name=$1 AND channel='telegram' AND metadata->'crm_contact_sync'->>'status' IN ('pending','failed') ORDER BY metadata->'crm_contact_sync'->>'attempted_at',id LIMIT $2`, syncer.agent, limit)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
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
	for i, id := range ids {
		conv, err := syncer.store.GetConversation(ctx, id)
		if err != nil {
			return i, err
		}
		if err = syncer.Ensure(ctx, conv); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
