package agentruntime

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

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
