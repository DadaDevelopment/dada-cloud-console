package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrConversationNotFound = errors.New("agentruntime: conversation not found")

type Actor struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type Conversation struct {
	ID              uuid.UUID      `json:"id"`
	AgentName       string         `json:"agent_name"`
	Channel         string         `json:"channel"`
	ExternalID      string         `json:"external_id"`
	ActorExternalID string         `json:"actor_external_id"`
	ActorUsername   string         `json:"actor_username"`
	ActorMetadata   map[string]any `json:"actor_metadata"`
	Metadata        map[string]any `json:"metadata"`
	Status          string         `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type Message struct {
	ID             uuid.UUID      `json:"id"`
	ConversationID uuid.UUID      `json:"conversation_id"`
	Role           string         `json:"role"`
	Content        string         `json:"content"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

type ConversationStore interface {
	GetOrCreateConversation(ctx context.Context, agentName, channel, externalID string, actor Actor) (conv Conversation, created bool, err error)
	GetConversation(ctx context.Context, id uuid.UUID) (Conversation, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata map[string]any) error
	Touch(ctx context.Context, id uuid.UUID) error
	ListIdleConversations(ctx context.Context, agentName string, threshold time.Time) ([]Conversation, error)

	SaveMessage(ctx context.Context, conversationID uuid.UUID, role, content string, metadata map[string]any) (Message, error)
	GetRecentMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]Message, error)
}

type pgStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) ConversationStore {
	return &pgStore{pool: pool}
}

func (s *pgStore) GetOrCreateConversation(ctx context.Context, agentName, channel, externalID string, actor Actor) (Conversation, bool, error) {
	actorMeta, _ := json.Marshal(actor.Metadata)
	if actorMeta == nil {
		actorMeta = []byte("{}")
	}

	var conv Conversation
	var created bool

	err := s.pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO conversations (agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (agent_name, channel, external_id) DO NOTHING
			RETURNING id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, true AS created
		)
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, COALESCE(created, false)
		FROM inserted
		UNION ALL
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, false
		FROM conversations
		WHERE agent_name = $1 AND channel = $2 AND external_id = $3
		LIMIT 1
	`, agentName, channel, externalID, actor.ExternalID, actor.Username, actorMeta).Scan(
		&conv.ID, &conv.AgentName, &conv.Channel, &conv.ExternalID,
		&conv.ActorExternalID, &conv.ActorUsername, &conv.ActorMetadata,
		&conv.Metadata, &conv.Status, &conv.CreatedAt, &conv.UpdatedAt, &created,
	)
	if err != nil {
		return Conversation{}, false, err
	}

	return conv, created, nil
}

func (s *pgStore) GetConversation(ctx context.Context, id uuid.UUID) (Conversation, error) {
	var conv Conversation
	err := s.pool.QueryRow(ctx, `
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at
		FROM conversations WHERE id = $1
	`, id).Scan(
		&conv.ID, &conv.AgentName, &conv.Channel, &conv.ExternalID,
		&conv.ActorExternalID, &conv.ActorUsername, &conv.ActorMetadata,
		&conv.Metadata, &conv.Status, &conv.CreatedAt, &conv.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrConversationNotFound
	}
	return conv, err
}

func (s *pgStore) UpdateMetadata(ctx context.Context, id uuid.UUID, metadata map[string]any) error {
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE conversations SET metadata = $2, updated_at = NOW() WHERE id = $1
	`, id, metaJSON)
	return err
}

func (s *pgStore) Touch(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *pgStore) ListIdleConversations(ctx context.Context, agentName string, threshold time.Time) ([]Conversation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at
		FROM conversations
		WHERE agent_name = $1 AND status = 'active' AND updated_at < $2
		ORDER BY updated_at ASC
	`, agentName, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var conv Conversation
		if err := rows.Scan(
			&conv.ID, &conv.AgentName, &conv.Channel, &conv.ExternalID,
			&conv.ActorExternalID, &conv.ActorUsername, &conv.ActorMetadata,
			&conv.Metadata, &conv.Status, &conv.CreatedAt, &conv.UpdatedAt,
		); err != nil {
			return nil, err
		}
		convs = append(convs, conv)
	}
	return convs, rows.Err()
}

func (s *pgStore) SaveMessage(ctx context.Context, conversationID uuid.UUID, role, content string, metadata map[string]any) (Message, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metaJSON, _ := json.Marshal(metadata)

	var msg Message
	err := s.pool.QueryRow(ctx, `
		INSERT INTO conversation_messages (conversation_id, role, content, metadata)
		VALUES ($1, $2, $3, $4)
		RETURNING id, conversation_id, role, content, metadata, created_at
	`, conversationID, role, content, metaJSON).Scan(
		&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Metadata, &msg.CreatedAt,
	)
	return msg, err
}

func (s *pgStore) GetRecentMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, conversation_id, role, content, metadata, created_at
		FROM conversation_messages
		WHERE conversation_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Metadata, &msg.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}

	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	return msgs, rows.Err()
}
