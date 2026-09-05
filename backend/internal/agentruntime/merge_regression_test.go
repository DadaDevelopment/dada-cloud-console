package agentruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHookManagementRequiresAuthentication(t *testing.T) {
	for _, token := range []string{"", testRuntimeToken} {
		srv := &Server{token: token}
		for _, route := range []struct{ method, path string }{{"GET", "/hooks"}, {"POST", "/hooks"}, {"DELETE", "/hooks/invalid"}} {
			req := httptest.NewRequest(route.method, route.path, nil)
			res := httptest.NewRecorder()
			srv.Handler().ServeHTTP(res, req)
			expected := http.StatusUnauthorized
			if token == "" {
				expected = http.StatusServiceUnavailable
			}
			require.Equal(t, expected, res.Code)
		}
	}
}

func TestPGIdleSuppressesPausedConversationBeforeAndDuringRun(t *testing.T) {
	for _, stopDuring := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-paused", true: "stop-during-run"}[stopDuring], func(t *testing.T) {
			store := setupTestStore(t).(*pgStore)
			ctx := context.Background()
			conv := stateTestConversation(t, store)
			calls, delivered := 0, 0
			model := runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
				calls++
				require.Equal(t, "runtime-"+conv.ID.String(), run.ContextID)
				claims, err := verifyContextToken([]byte(testRuntimeToken), run.ConversationContext.ContextToken, time.Now())
				require.NoError(t, err)
				require.Equal(t, conv.ID, claims.ConversationID)
				_, err = store.PauseAgent(ctx, conv.ID, "customer stopped")
				require.NoError(t, err)
				return "must never be delivered", nil
			})
			rt := NewRuntime(store, &noopHooks{}, model, nil)
			rt.contextKey = []byte(testRuntimeToken)
			if !stopDuring {
				_, err := store.PauseAgent(ctx, conv.ID, "customer stopped")
				require.NoError(t, err)
			}
			scheduler := NewIdleScheduler(store.pool, rt, model, &fakeOutbound{onSend: func(_, _, _ string) { delivered++ }}, time.Second)
			scheduler.invoke(ctx, idleHookRow{ConversationID: conv.ID.String(), AgentName: conv.AgentName, ChatExternalID: conv.ExternalID, IdleMinutes: 30})
			require.Zero(t, delivered)
			if stopDuring {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
			messages, err := store.GetRecentMessages(ctx, conv.ID, 20)
			require.NoError(t, err)
			for _, m := range messages {
				require.NotEqual(t, "assistant", m.Role)
			}
		})
	}
}

func TestPGRuntimePreservesAttachmentsAndRearmsIdle(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	require.NoError(t, store.UpdateMetadata(ctx, conv.ID, map[string]any{"idle_fired_at": "earlier"}))
	calls := 0
	model := runFunc(func(_ context.Context, run AgentRunRequest) (string, error) {
		calls++
		require.Len(t, run.Messages, 1)
		require.Len(t, run.Messages[0].Attachments, 1)
		attachment := run.Messages[0].Attachments[0].(map[string]any)
		require.Equal(t, "image", attachment["kind"])
		require.Equal(t, "receipt.png", attachment["file_name"])
		require.Contains(t, renderAgentRun(run), "receipt.png")
		return "received", nil
	})
	rt := NewRuntime(store, &noopHooks{}, model, nil)
	rt.contextKey = []byte(testRuntimeToken)
	req := MessageRequest{AgentName: conv.AgentName, Channel: conv.Channel, ExternalID: conv.ExternalID,
		Messages: []InboundMessage{{Content: "receipt", ChannelMessageID: "attachment-1", Attachment: &RuntimeAttachment{Kind: "image", FileName: "receipt.png"}}}}
	response, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "received", response.Text)
	reloaded, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	require.NotContains(t, reloaded.Metadata, "idle_fired_at")
	response, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.True(t, response.Suppressed)
	require.Equal(t, 1, calls)
}
