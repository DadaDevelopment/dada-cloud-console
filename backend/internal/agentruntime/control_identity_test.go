package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func postRuntimeIdentity(t *testing.T, base, path string, body any, agent, endUser string) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testRuntimeToken)
	req.Header.Set("Content-Type", "application/json")
	if agent != "" {
		req.Header.Set(agentHeader, agent)
	}
	if endUser != "" {
		req.Header.Set(endUserHeader, endUser)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return res.StatusCode, out
}

func TestPGRuntimeToolsResolveConversationFromIdentityHeaders(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	conv := stateTestConversation(t, store)
	other := stateTestConversation(t, store)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	server := httptest.NewServer(NewServer(store.pool, t.TempDir()).Handler())
	defer server.Close()
	endUser := conv.Channel + ":" + conv.ExternalID

	status, out := postRuntimeIdentity(t, server.URL, "/tools/update-state",
		map[string]any{"context_token": "garbage-copied-by-model", "expected_version": 0, "patch": StatePatch{}}, conv.AgentName, endUser)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["updated"])
	version := int64(out["state"].(map[string]any)["version"].(float64))

	status, out = postRuntimeIdentity(t, server.URL, "/tools/update-state",
		map[string]any{"context_token": "", "expected_version": version, "patch": StatePatch{}}, conv.AgentName, endUser)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["updated"])

	otherState, err := store.GetState(context.Background(), other.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), otherState.Version)

	for name, tt := range map[string]struct{ agent, endUser, code string }{
		"foreign agent":          {other.AgentName, endUser, "unknown_conversation"},
		"unknown end user":       {conv.AgentName, conv.Channel + ":nobody", "unknown_conversation"},
		"end user without colon": {conv.AgentName, conv.ExternalID, "malformed_identity_headers"},
		"agent header only":      {conv.AgentName, "", "malformed_identity_headers"},
	} {
		t.Run(name, func(t *testing.T) {
			status, out := postRuntimeIdentity(t, server.URL, "/tools/update-state",
				map[string]any{"context_token": "", "expected_version": version, "patch": StatePatch{}}, tt.agent, tt.endUser)
			require.Equal(t, http.StatusForbidden, status, out)
			require.Equal(t, "invalid runtime context", out["error"])
			require.Equal(t, tt.code, out["error_code"])
		})
	}

	token, err := issueContextToken([]byte(testRuntimeToken), conv, time.Now().Add(time.Minute))
	require.NoError(t, err)
	status, out = postRuntime(t, server.URL, "/tools/update-state", map[string]any{"context_token": token, "expected_version": version, "patch": StatePatch{}}, testRuntimeToken)
	require.Equal(t, http.StatusOK, status, out)
	status, out = postRuntime(t, server.URL, "/tools/update-state", map[string]any{"context_token": token[:len(token)-1], "expected_version": version, "patch": StatePatch{}}, testRuntimeToken)
	require.Equal(t, http.StatusForbidden, status, out)
	require.Equal(t, "invalid_context_token", out["error_code"])
}
