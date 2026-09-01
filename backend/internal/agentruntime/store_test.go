package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) ConversationStore {
	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	pool, err := db.Connect(ctx, "")
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

	msg1, err := store.SaveMessage(ctx, conv.ID, "user", "hello", nil)
	require.NoError(t, err)
	require.Equal(t, "user", msg1.Role)
	require.Equal(t, "hello", msg1.Content)

	msg2, err := store.SaveMessage(ctx, conv.ID, "assistant", "hi there", map[string]any{"tokens": 10})
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
	actor := Actor{ExternalID: "user-1", Username: "testuser"}

	conv1, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-idle", actor)
	require.NoError(t, err)

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

	err = store.Touch(ctx, conv1.ID)
	require.NoError(t, err)

	idle2, err := store.ListIdleConversations(ctx, agentName, threshold)
	require.NoError(t, err)
	for _, c := range idle2 {
		require.NotEqual(t, conv1.ID, c.ID)
	}
}
