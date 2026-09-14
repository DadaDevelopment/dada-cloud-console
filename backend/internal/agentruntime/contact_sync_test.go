package agentruntime

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

// A misconfigured integration (short token) makes create() fail
// deterministically with no network call, so the failure path is
// reachable without a mock CRM server.
func TestContactSync_Ensure_LogsSwallowedFailure(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "contact-sync-test"
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "5001",
		Actor{ExternalID: "5001", Username: "client", Metadata: map[string]any{"first_name": "Иван"}})
	require.NoError(t, err)

	syncer := &ContactSync{store: store, endpoint: "http://unused.invalid", token: "too-short", agent: agent,
		client: &http.Client{Timeout: time.Second}}

	var buf bytes.Buffer
	prior := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prior })

	require.NoError(t, syncer.Ensure(ctx, conv))

	require.Contains(t, buf.String(), "agentruntime: contact sync failed")
	require.Contains(t, buf.String(), "contact integration not configured")
	require.Contains(t, buf.String(), conv.ID.String())

	var status string
	err = store.pool.QueryRow(ctx, `SELECT metadata->'crm_contact_sync'->>'status' FROM conversations WHERE id=$1`, conv.ID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
}

func TestPGContactRejectedRequestIsNotRetried(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "crm-contact-" + uuid.NewString()
	var calls atomic.Int32
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"applied":false,"error":"invalid_contact_request"}`))
	}))
	defer crm.Close()
	rt := NewRuntime(store, testHooks{}, runFunc(func(context.Context, AgentRunRequest) (string, error) { return "Привет!", nil }), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.contacts = &ContactSync{store: store, endpoint: crm.URL, token: testRuntimeToken, agent: agent, client: crm.Client()}
	srv := &Server{runtime: rt}
	req := MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "synthA-1789262794", Actor: Actor{Username: ""}, Messages: []InboundMessage{{Content: "Привет", ChannelMessageID: "1"}}}
	out, err := rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.False(t, out.Suppressed)
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", req.ExternalID, req.Actor)
	require.NoError(t, err)
	receipt := conv.Metadata["crm_contact_sync"].(map[string]any)
	require.Equal(t, "rejected", receipt["status"])
	require.EqualValues(t, 1, calls.Load())

	n, err := srv.ReconcileContacts(ctx, 5)
	require.NoError(t, err)
	require.Zero(t, n, "a rejected request must leave the retry queue")
	req.Messages = []InboundMessage{{Content: "Ещё раз", ChannelMessageID: "2"}}
	_, err = rt.ProcessMessage(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load(), "the next turn must not re-send a rejected contact")
}
