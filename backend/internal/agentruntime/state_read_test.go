package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func getRuntimeState(t *testing.T, base string, q url.Values, token string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/state?"+q.Encode(), nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestPGGetStateReadsConversationByIdentity(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	conv := stateTestConversation(t, store)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	server := httptest.NewServer(NewServer(store.pool, t.TempDir()).Handler())
	defer server.Close()

	q := url.Values{"agent_name": {conv.AgentName}, "channel": {conv.Channel}, "external_id": {conv.ExternalID}}
	status, out := getRuntimeState(t, server.URL, q, testRuntimeToken)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, conv.ID.String(), out["conversation_id"])
	state := out["state"].(map[string]any)
	require.Equal(t, true, state["agent_enabled"])
	require.Contains(t, state, "reported_facts")
	require.Contains(t, state, "active_skills")
	require.NotContains(t, out, "last_assistant", "no reply saved yet")

	_, err := store.SaveMessage(context.Background(), conv.ID, SaveMessageInput{Role: "assistant", Content: "Номер счёта и почту принял"})
	require.NoError(t, err)
	status, out = getRuntimeState(t, server.URL, q, testRuntimeToken)
	require.Equal(t, http.StatusOK, status, out)
	last := out["last_assistant"].(map[string]any)
	require.Equal(t, "Номер счёта и почту принял", last["text"])
	require.NotEmpty(t, last["created_at"])

	status, _ = getRuntimeState(t, server.URL, url.Values{"agent_name": {conv.AgentName}, "channel": {conv.Channel}, "external_id": {"nobody"}}, testRuntimeToken)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = getRuntimeState(t, server.URL, url.Values{"agent_name": {conv.AgentName}}, testRuntimeToken)
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = getRuntimeState(t, server.URL, q, "")
	require.Equal(t, http.StatusUnauthorized, status)
}
