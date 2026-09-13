package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMaybeAckEscalationSilence_FiresOnlyForGenuineEscalationPause(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	rt := NewRuntime(store, nil, nil, nil)

	cases := []struct {
		name        string
		pauseReason string
		wantFired   bool
	}{
		{"genuine escalation", "escalated: E_DEPOSIT_HANDOFF", true},
		{"courtesy stop", "customer requested no further replies", false},
		{"hook failure", "integration hook failed: message.received", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conv, _, err := store.GetOrCreateConversation(ctx, "escalation-ack-test-"+uuid.NewString(), "telegram", "1", Actor{ExternalID: "1", Username: "client"})
			require.NoError(t, err)
			var sent []string
			rt.outbound = func(_ context.Context, _, _, text, _ string) error {
				sent = append(sent, text)
				return nil
			}

			rt.maybeAckEscalationSilence(ctx, conv, RuntimeState{PauseReason: tc.pauseReason})

			if tc.wantFired {
				require.Equal(t, []string{escalationSilenceAck}, sent)
			} else {
				require.Empty(t, sent)
			}
		})
	}
}

func TestMaybeAckEscalationSilence_FiresExactlyOncePerPause(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	rt := NewRuntime(store, nil, nil, nil)
	conv, _, err := store.GetOrCreateConversation(ctx, "escalation-ack-test-"+uuid.NewString(), "telegram", "2", Actor{ExternalID: "2", Username: "client"})
	require.NoError(t, err)
	var sent []string
	rt.outbound = func(_ context.Context, _, _, text, _ string) error {
		sent = append(sent, text)
		return nil
	}
	state := RuntimeState{PauseReason: "escalated: E_OTHER"}

	rt.maybeAckEscalationSilence(ctx, conv, state)
	rt.maybeAckEscalationSilence(ctx, conv, state)
	rt.maybeAckEscalationSilence(ctx, conv, state)

	require.Equal(t, []string{escalationSilenceAck}, sent)
}

func TestMaybeAckEscalationSilence_ClearEscalationAckRearmsForFreshPause(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	rt := NewRuntime(store, nil, nil, nil)
	conv, _, err := store.GetOrCreateConversation(ctx, "escalation-ack-test-"+uuid.NewString(), "telegram", "3", Actor{ExternalID: "3", Username: "client"})
	require.NoError(t, err)
	var sent []string
	rt.outbound = func(_ context.Context, _, _, text, _ string) error {
		sent = append(sent, text)
		return nil
	}
	state := RuntimeState{PauseReason: "escalated: E_OTHER"}

	rt.maybeAckEscalationSilence(ctx, conv, state)
	require.Len(t, sent, 1)

	require.NoError(t, store.ClearEscalationAck(ctx, conv.ID))
	rt.maybeAckEscalationSilence(ctx, conv, state)

	require.Len(t, sent, 2)
}

func TestPGEscalate_SecondClientMessageDuringPauseGetsSilenceAck(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "escalation-ack-http-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "3003", Actor{ExternalID: "3003", Username: "client"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", map[string]any{
		"context_token":  token,
		"reason_code":    "E_DEPOSIT_HANDOFF",
		"summary":        "Пополнил 500, реквизиты дал сам.",
		"client_message": "Деньги на счёте, дальше подключением занимается куратор",
	}, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Len(t, out.texts, 1)

	msgBody := map[string]any{
		"agent_name":  agent,
		"channel":     "telegram",
		"external_id": "3003",
		"actor":       map[string]any{"external_id": "3003", "username": "client"},
		"content":     "Ещё вопрос, вы тут?",
	}
	status, result = postRuntime(t, httpServer.URL, "/message", msgBody, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["suppressed"])
	require.Empty(t, result["text"])
	require.Equal(t, []string{"Деньги на счёте, дальше подключением занимается куратор", escalationSilenceAck}, out.texts)

	status, result = postRuntime(t, httpServer.URL, "/message", msgBody, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["suppressed"])
	require.Equal(t, []string{"Деньги на счёте, дальше подключением занимается куратор", escalationSilenceAck}, out.texts)
}
