package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// AGENT_RUNTIME_PRECHECK_SYNTHETIC takes only block and only next to log:
// any other word is ignored, block next to off or block refuses to start,
// and the mode is chosen per conversation by the actor's username.
func TestRuntimeFlags_PrecheckSyntheticNeedsLog(t *testing.T) {
	t.Setenv("AGENT_JUDGE_LLM_URL", "http://judge")
	t.Setenv("AGENT_JUDGE_LLM_KEY", "judge-key")
	t.Setenv("AGENT_JUDGE_LLM_MODEL", "judge-model")
	require.Empty(t, runtimeFlagsFromEnv().PrecheckSynthetic)

	t.Setenv("AGENT_RUNTIME_PRECHECK_SYNTHETIC", "log")
	require.Empty(t, runtimeFlagsFromEnv().PrecheckSynthetic, "only block is a synthetic mode")

	t.Setenv("AGENT_RUNTIME_PRECHECK_SYNTHETIC", "Block")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "requires AGENT_RUNTIME_PRECHECK=log")
	t.Setenv("AGENT_RUNTIME_PRECHECK", "block")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "requires AGENT_RUNTIME_PRECHECK=log")
	t.Setenv("AGENT_RUNTIME_PRECHECK", "log")
	require.NoError(t, ValidateFlagsFromEnv())

	rt := &Runtime{flags: runtimeFlagsFromEnv()}
	require.Equal(t, precheckBlock, rt.precheckMode(Conversation{ActorUsername: "eval_obj_1"}))
	require.Equal(t, precheckBlock, rt.precheckMode(Conversation{ActorUsername: "@QA_roman"}))
	require.Equal(t, precheckLog, rt.precheckMode(Conversation{ActorUsername: "ivan"}))
	require.Equal(t, precheckLog, rt.precheckMode(Conversation{}))
}

func (rig *precheckRig) sendAs(t *testing.T, username, externalID, text string) MessageResponse {
	t.Helper()
	out, err := rig.rt.ProcessMessage(context.Background(), MessageRequest{AgentName: rig.agentKey, Channel: "telegram", ExternalID: externalID,
		Actor:    Actor{ExternalID: externalID, Username: username},
		Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
	require.NoError(t, err)
	return out
}

// Log for clients, block for test traffic: an eval caller's draft is checked
// before delivery and rewritten, while a client of the same runtime gets
// the draft as is and the check runs after delivery.
func TestPGPrecheckSynthetic_BlockForTestTrafficLogForClients(t *testing.T) {
	env := map[string]string{"AGENT_RUNTIME_PRECHECK": "log", "AGENT_RUNTIME_PRECHECK_SYNTHETIC": "block"}
	bad := agentjudge.Precheck{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}

	eval := newPrecheckRig(t, env, "первый ответ", "вторая версия")
	eval.judge.verdicts = []agentjudge.Precheck{bad, {}}
	out := eval.sendAs(t, "eval_obj_1", "501", "привет")
	require.Equal(t, "вторая версия", out.Text)
	require.Len(t, eval.agent.runs, 2)
	require.Equal(t, "ask (why script_repeat)", eval.agent.runs[1].ConversationContext.ReplyError)
	checks, _ := eval.judge.snapshot()
	require.Len(t, checks, 2)

	client := newPrecheckRig(t, env, "первый ответ", "вторая версия")
	client.judge.verdicts = []agentjudge.Precheck{bad}
	client.judge.done = make(chan struct{}, 1)
	out = client.sendAs(t, "ivan", "502", "привет")
	require.Equal(t, "первый ответ", out.Text)
	require.Len(t, client.agent.runs, 1)
	select {
	case <-client.judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the client's log check never submitted")
	}
	_, submits := client.judge.snapshot()
	require.Len(t, submits, 1)
	require.Equal(t, "log", submits[0].Precheck["mode"])
	require.Equal(t, precheckRewrite, submits[0].Precheck["shadow_outcome"])
}
