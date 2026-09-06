package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestControlDecodeErrorsGiveSafeRepairInstructions(t *testing.T) {
	srv := &Server{token: testRuntimeToken}
	for _, tt := range []struct{ name, body, code string }{
		{"string version", `{"expected_version":"private-value"}`, "invalid_field_type"},
		{"string facts", `{"patch":{"reported_facts":"private-value"}}`, "invalid_field_type"},
		{"unknown field", `{"private-value":true}`, "unknown_field"},
		{"channel id instead of UUID", `{"patch":{"reported_facts":{"test":{"value":"quote","source_message_id":"private-value"}}}}`, "invalid_request_shape"},
		{"invalid json", `{"expected_version":private-value}`, "invalid_json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/tools/update-state", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+testRuntimeToken)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var result map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
			require.Equal(t, false, result["updated"])
			require.Equal(t, tt.code, result["error_code"])
			require.Contains(t, result["hint"], "incoming_messages[].id")
			require.NotContains(t, rec.Body.String(), "private-value")
		})
	}
}

func TestPGRejectedStateHasActionableErrorsWithoutMutation(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	conv := stateTestConversation(t, store)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	server := httptest.NewServer(NewServer(store.pool, t.TempDir()).Handler())
	defer server.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), conv, time.Now().Add(time.Minute))
	require.NoError(t, err)
	source, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "private-value"})
	require.NoError(t, err)
	for _, tt := range []struct {
		patch StatePatch
		code  string
	}{
		{StatePatch{ReportedFacts: map[string]ReportedFact{"fact": {Value: "private-value", SourceMessageID: uuid.New()}}}, "invalid_source_message"},
		{StatePatch{OpenLoops: map[string]OpenLoop{"loop": {Question: "private-value", SourceMessageID: source.ID, Status: "private-value"}}}, "invalid_patch"},
	} {
		status, out := postRuntime(t, server.URL, "/tools/update-state", map[string]any{"context_token": token, "expected_version": 0, "patch": tt.patch}, testRuntimeToken)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, false, out["updated"])
		require.Equal(t, tt.code, out["error_code"])
		encoded, err := json.Marshal(out)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private-value")
		require.NotContains(t, string(encoded), token)
		state, err := store.GetState(ctx, conv.ID)
		require.NoError(t, err)
		require.Zero(t, state.Version)
		require.Empty(t, state.ReportedFacts)
		require.Empty(t, state.OpenLoops)
	}
}
