package agentruntime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) ConversationStore {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping agentruntime store test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return NewPGStore(pool)
}

func TestGetOrCreateConversation(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	channel := "telegram"
	externalID := "123456"
	actor := Actor{
		ExternalID: "user-789",
		Username:   "testuser",
		Metadata:   map[string]any{"first_name": "Test"},
	}

	conv, created, err := store.GetOrCreateConversation(ctx, agentName, channel, externalID, actor)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, agentName, conv.AgentName)
	require.Equal(t, channel, conv.Channel)
	require.Equal(t, externalID, conv.ExternalID)
	require.Equal(t, actor.ExternalID, conv.ActorExternalID)
	require.Equal(t, actor.Username, conv.ActorUsername)
	require.Equal(t, "active", conv.Status)

	conv2, created2, err := store.GetOrCreateConversation(ctx, agentName, channel, externalID, actor)
	require.NoError(t, err)
	require.False(t, created2)
	require.Equal(t, conv.ID, conv2.ID)
}

func TestSaveAndGetRecentMessages(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "user-1", Username: "testuser"}
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-1", actor)
	require.NoError(t, err)

	msg1, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "hello"})
	require.NoError(t, err)
	require.Equal(t, "user", msg1.Role)
	require.Equal(t, "hello", msg1.Content)

	msg2, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: "hi there", Metadata: map[string]any{"tokens": 10}})
	require.NoError(t, err)
	require.Equal(t, "assistant", msg2.Role)

	msgs, err := store.GetRecentMessages(ctx, conv.ID, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "user", msgs[0].Role)
	require.Equal(t, "assistant", msgs[1].Role)
}

func TestUpdateMetadata(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "user-1", Username: "testuser"}
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-1", actor)
	require.NoError(t, err)

	metadata := map[string]any{"crm_person_id": "person-123", "tags": []string{"vip"}}
	err = store.UpdateMetadata(ctx, conv.ID, metadata)
	require.NoError(t, err)

	updated, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	require.Equal(t, "person-123", updated.Metadata["crm_person_id"])
}

func TestListIdleConversations(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	externalID := "chat-idle-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "user-1", Username: "testuser"}

	conv1, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", externalID, actor)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = store.(*pgStore).pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
	})

	threshold := time.Now().Add(time.Hour)

	idle, err := store.ListIdleConversations(ctx, agentName, threshold)
	require.NoError(t, err)
	require.NotEmpty(t, idle)

	found := false
	for _, c := range idle {
		if c.ID == conv1.ID {
			found = true
			break
		}
	}
	require.True(t, found)

	touchCutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	err = store.Touch(ctx, conv1.ID)
	require.NoError(t, err)

	idle2, err := store.ListIdleConversations(ctx, agentName, touchCutoff)
	require.NoError(t, err)
	for _, c := range idle2 {
		require.NotEqual(t, conv1.ID, c.ID)
	}
}

func TestSaveMessageCanonicalFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "user-1", Username: "testuser"}
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-canon", actor)
	require.NoError(t, err)

	sentAt := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	msg, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:             "user",
		Content:          "hello",
		ChannelMessageID: "1001",
		ThreadID:         "55",
		SourceSentAt:     &sentAt,
		ChannelMetadata:  map[string]any{"chat_type": "private"},
	})
	require.NoError(t, err)
	require.Equal(t, "1001", msg.ChannelMessageID)
	require.Equal(t, "55", msg.ThreadID)
	require.NotNil(t, msg.SourceSentAt)
	require.WithinDuration(t, sentAt, *msg.SourceSentAt, time.Second)
	require.Equal(t, "private", msg.ChannelMetadata["chat_type"])
	require.Empty(t, msg.Entities)
	require.Empty(t, msg.Attachments)
	require.Nil(t, msg.ReplyToMessageID)
}

func TestFindMessageByChannelIDAndReplyResolution(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "test-agent-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "user-1", Username: "testuser"}
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-reply", actor)
	require.NoError(t, err)

	original, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:             "assistant",
		Content:          "which jurisdiction are you in?",
		ChannelMessageID: "2001",
	})
	require.NoError(t, err)

	found, err := store.FindMessageByChannelID(ctx, conv.ID, "2001")
	require.NoError(t, err)
	require.Equal(t, original.ID, found.ID)

	reply, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:                    "user",
		Content:                 "Kazakhstan",
		ChannelMessageID:        "2002",
		ReplyToChannelMessageID: "2001",
	})
	require.NoError(t, err)
	require.NotNil(t, reply.ReplyToMessageID)
	require.Equal(t, original.ID, *reply.ReplyToMessageID)

	_, err = store.FindMessageByChannelID(ctx, conv.ID, "does-not-exist")
	require.ErrorIs(t, err, ErrMessageNotFound)

	danglingReply, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:                    "user",
		Content:                 "orphan reply",
		ChannelMessageID:        "2003",
		ReplyToChannelMessageID: "9999",
	})
	require.NoError(t, err)
	require.Nil(t, danglingReply.ReplyToMessageID)
}
