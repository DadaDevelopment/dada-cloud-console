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
var ErrMessageNotFound = errors.New("agentruntime: message not found")

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

// Message is one turn of conversation_messages, widened (Agent Harness v2,
// Step 1) to carry channel-native identity: ChannelMessageID is the
// provider's own message id (Telegram message_id as text), ThreadID is a
// forum/topic id when the channel has one, SourceSentAt is when the sender
// actually sent it (distinct from CreatedAt, which is when the platform
// persisted it -- the two diverge under load or after an outage replay).
// ReplyToMessageID is a self-FK to another Message.ID in the same
// conversation, resolved from the provider's reply id via
// FindMessageByChannelID before insert -- it is never the provider's own id.
// Entities and Attachments are reserved for the link-resolver and media
// steps; both default to an empty slice and are not populated yet.
type Message struct {
	ID               uuid.UUID      `json:"id"`
	ConversationID   uuid.UUID      `json:"conversation_id"`
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	Metadata         map[string]any `json:"metadata"`
	ChannelMessageID string         `json:"channel_message_id,omitempty"`
	ThreadID         string         `json:"thread_id,omitempty"`
	SourceSentAt     *time.Time     `json:"source_sent_at,omitempty"`
	ReplyToMessageID *uuid.UUID     `json:"reply_to_message_id,omitempty"`
	Entities         []any          `json:"entities"`
	Attachments      []any          `json:"attachments"`
	EditedAt         *time.Time     `json:"edited_at,omitempty"`
	DeletedAt        *time.Time     `json:"deleted_at,omitempty"`
	ChannelMetadata  map[string]any `json:"channel_metadata"`
	CreatedAt        time.Time      `json:"created_at"`
}

// SaveMessageInput is what SaveMessage accepts. Role and Content are
// required; every other field is optional and left at its zero value for
// callers (tests, internal system messages) that have no channel identity to
// carry. ReplyToChannelMessageID is the *provider's* id of the message being
// replied to -- SaveMessage resolves it to a Message.ID via
// FindMessageByChannelID and stores that UUID in ReplyToMessageID; a
// provider id that does not resolve is stored as no reply rather than
// failing the whole save, since a dangling reply reference must never lose
// the message itself.
type SaveMessageInput struct {
	Role                    string
	Content                 string
	Metadata                map[string]any
	ChannelMessageID        string
	ThreadID                string
	SourceSentAt            *time.Time
	ReplyToChannelMessageID string
	ChannelMetadata         map[string]any

	// Entities carries structured link metadata (Agent Harness v2, Step 5):
	// each entry is a {url, title} object persisted into the row's entities
	// JSONB column. Reserved Attachments equivalent: pass nil when none.
	Entities []any

	// Attachments carries media descriptors (Agent Harness v2, Step 6):
	// zero or one {kind, ...} object per message persisted into the row's
	// attachments JSONB column.
	Attachments []any
}

type ConversationStore interface {
	GetOrCreateConversation(ctx context.Context, agentName, channel, externalID string, actor Actor) (conv Conversation, created bool, err error)
	GetConversation(ctx context.Context, id uuid.UUID) (Conversation, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata map[string]any) error
	Touch(ctx context.Context, id uuid.UUID) error
	ListIdleConversations(ctx context.Context, agentName string, threshold time.Time) ([]Conversation, error)

	SaveMessage(ctx context.Context, conversationID uuid.UUID, input SaveMessageInput) (Message, error)
	GetRecentMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]Message, error)
	FindMessageByChannelID(ctx context.Context, conversationID uuid.UUID, channelMessageID string) (Message, error)

	// ClearIdleFlag re-arms idle hooks for the conversation: it removes the
	// idle_fired_at metadata key so a conversation.active->idle transition
	// can fire again after real user activity.
	ClearIdleFlag(ctx context.Context, conversationID uuid.UUID) error

	// FinishConversation retires a conversation without deleting it: history
	// rows stay for audit, but the identity tuple is released so the next
	// inbound message from the same user opens a fresh conversation.
	FinishConversation(ctx context.Context, conversationID uuid.UUID) error
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
			ON CONFLICT (agent_name, channel, external_id) WHERE status = 'active' DO NOTHING
			RETURNING id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, true AS created
		)
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, COALESCE(created, false)
		FROM inserted
		UNION ALL
		SELECT id, agent_name, channel, external_id, actor_external_id, actor_username, actor_metadata, metadata, status, created_at, updated_at, false
		FROM conversations
		WHERE agent_name = $1 AND channel = $2 AND external_id = $3 AND status = 'active'
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

func (s *pgStore) FinishConversation(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET status = 'finished', updated_at = NOW() WHERE id = $1`, id)
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

// messageColumns is the shared SELECT list for every reader below, so the
// column order and the Scan order in scanMessage never drift apart.
const messageColumns = `id, conversation_id, role, content, metadata,
	channel_message_id, thread_id, source_sent_at, reply_to_message_id,
	entities, attachments, edited_at, deleted_at, channel_metadata, created_at`

func scanMessage(row rowScanner) (Message, error) {
	var msg Message
	var channelMessageID, threadID *string
	var entitiesJSON, attachmentsJSON []byte

	err := row.Scan(
		&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Metadata,
		&channelMessageID, &threadID, &msg.SourceSentAt, &msg.ReplyToMessageID,
		&entitiesJSON, &attachmentsJSON, &msg.EditedAt, &msg.DeletedAt,
		&msg.ChannelMetadata, &msg.CreatedAt,
	)
	if err != nil {
		return Message{}, err
	}
	if channelMessageID != nil {
		msg.ChannelMessageID = *channelMessageID
	}
	if threadID != nil {
		msg.ThreadID = *threadID
	}
	_ = json.Unmarshal(entitiesJSON, &msg.Entities)
	_ = json.Unmarshal(attachmentsJSON, &msg.Attachments)
	if msg.Entities == nil {
		msg.Entities = []any{}
	}
	if msg.Attachments == nil {
		msg.Attachments = []any{}
	}
	return msg, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *pgStore) SaveMessage(ctx context.Context, conversationID uuid.UUID, input SaveMessageInput) (Message, error) {
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metaJSON, _ := json.Marshal(metadata)

	channelMeta := input.ChannelMetadata
	if channelMeta == nil {
		channelMeta = map[string]any{}
	}
	channelMetaJSON, _ := json.Marshal(channelMeta)

	var channelMessageID *string
	if input.ChannelMessageID != "" {
		channelMessageID = &input.ChannelMessageID
	}
	var threadID *string
	if input.ThreadID != "" {
		threadID = &input.ThreadID
	}

	var replyToID *uuid.UUID
	if input.ReplyToChannelMessageID != "" {
		if resolved, err := s.FindMessageByChannelID(ctx, conversationID, input.ReplyToChannelMessageID); err == nil {
			replyToID = &resolved.ID
		}
	}

	entities := input.Entities
	if entities == nil {
		entities = []any{}
	}
	entitiesJSON, _ := json.Marshal(entities)

	attachments := input.Attachments
	if attachments == nil {
		attachments = []any{}
	}
	attachmentsJSON, _ := json.Marshal(attachments)

	row := s.pool.QueryRow(ctx, `
		INSERT INTO conversation_messages (
			conversation_id, role, content, metadata,
			channel_message_id, thread_id, source_sent_at, reply_to_message_id,
			channel_metadata, entities, attachments
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+messageColumns, conversationID, input.Role, input.Content, metaJSON,
		channelMessageID, threadID, input.SourceSentAt, replyToID, channelMetaJSON, entitiesJSON, attachmentsJSON,
	)
	return scanMessage(row)
}

func (s *pgStore) GetRecentMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+messageColumns+`
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
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}

	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	return msgs, rows.Err()
}

func (s *pgStore) FindMessageByChannelID(ctx context.Context, conversationID uuid.UUID, channelMessageID string) (Message, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+messageColumns+`
		FROM conversation_messages
		WHERE conversation_id = $1 AND channel_message_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, conversationID, channelMessageID)

	msg, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrMessageNotFound
	}
	return msg, err
}

// ClearIdleFlag removes the idle_fired_at metadata key (Agent Harness v2,
// Step 7): every real inbound user message re-arms the conversation's idle
// hooks, so a 30-minute follow-up fires once per idle period, not once per
// conversation lifetime.
func (s *pgStore) ClearIdleFlag(ctx context.Context, conversationID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE conversations
		SET metadata = metadata - 'idle_fired_at'
		WHERE id = $1
	`, conversationID)
	return err
}
