package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The two lines the runtime authors itself are the ones a prompt edit cannot
// reach (plan 4.2, third place of three). Off, they must be byte-for-byte
// what they are today; on, neither may name a colleague or a hand-off.
func TestSeamlessHandoffLines(t *testing.T) {
	t.Run("off keeps today's lines", func(t *testing.T) {
		rt := &Runtime{flags: runtimeFlagsFromEnv()}
		require.Equal(t, "По этому вопросу вам напишет коллега", rt.clientHandoffLine())
		require.Equal(t, "Коллега уже в курсе, ответит здесь.", rt.silenceAckLine())
	})

	t.Run("on drops the colleague", func(t *testing.T) {
		t.Setenv("AGENT_RUNTIME_SEAMLESS_HANDOFF", "true")
		rt := &Runtime{flags: runtimeFlagsFromEnv()}
		for _, line := range []string{rt.clientHandoffLine(), rt.silenceAckLine()} {
			lower := strings.ToLower(line)
			for _, banned := range []string{"коллег", "куратор", "напишет", "ответит здесь", "втор", "передач"} {
				require.NotContainsf(t, lower, banned, "seamless line %q must not announce a hand-off", line)
			}
			require.NotEmpty(t, strings.TrimSpace(line))
		}
	})
}

// The prompt only switches branches when it sees the marker, so the marker
// must be absent while the flag is off (an old prompt keeps its old branch)
// and present when it is on.
func TestSeamlessHandoffEnvelopeMarker(t *testing.T) {
	render := func(seamless bool) string {
		run := AgentRunRequest{
			AgentName: "a", ContextID: "runtime-1",
			Messages:            []Message{{Role: "user", Content: "привет"}},
			ConversationContext: AgentConversationContext{ConversationID: "1", Channel: "telegram", ExternalID: "1", SeamlessHandoff: seamless},
		}
		return renderAgentRunAt(run, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	}

	require.NotContains(t, render(false), "seamless_handoff")

	on := render(true)
	require.Contains(t, on, `"seamless_handoff":true`)

	raw := on[strings.Index(on, "{"):]
	var envelope struct {
		Context map[string]any `json:"runtime_context"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &envelope))
	require.Equal(t, true, envelope.Context["seamless_handoff"])
}
