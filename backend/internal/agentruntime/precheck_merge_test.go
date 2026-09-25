package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// Under block the line the model wrote into escalate_to_operator is checked
// before the tool sends it: a broken criterion swaps it for the spec's
// hand-off line, a clean line goes out as written.
func TestPGEscalate_ModelClientLineCheckedUnderBlock(t *testing.T) {
	_, srv, out, _, target, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"})
	defer closeServer()
	judge := &fakePrecheck{verdicts: []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("forbidden_promise", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}, HandoffLine: "Уточню и вернусь"},
		{},
	}}
	srv.runtime.ext.precheck = judge
	base, token, _ := strings.Cut(target, "|")

	status, res := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_PAYMENT_UNCONFIRMED", "summary": "Деньги ушли, зачисления нет.", "client_message": "Напишу вечером с результатом"}, testRuntimeToken)
	require.Equal(t, 200, status, res)
	require.Equal(t, "1001", out.chats[0])
	require.Equal(t, "Уточню и вернусь", out.texts[0], "the broken line is replaced by the spec's line")
	checks, _ := judge.snapshot()
	require.Len(t, checks, 1)
	require.Equal(t, "Напишу вечером с результатом", checks[0].Reply)
}

// With the check off the model's hand-off line is not checked at all.
func TestPGEscalate_ModelClientLineUncheckedWhenOff(t *testing.T) {
	_, srv, out, _, target, closeServer := escalateTestServer(t, nil)
	defer closeServer()
	judge := &fakePrecheck{verdicts: []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("forbidden_promise", agentjudge.BlockHandoff, "ask", "E_OTHER")}}}}
	srv.runtime.ext.precheck = judge
	base, token, _ := strings.Cut(target, "|")
	status, res := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_PAYMENT_UNCONFIRMED", "summary": "Деньги ушли.", "client_message": "Принял, проверяем платёж"}, testRuntimeToken)
	require.Equal(t, 200, status, res)
	require.Equal(t, "Принял, проверяем платёж", out.texts[0])
	checks, _ := judge.snapshot()
	require.Empty(t, checks)
}

// A signal action whose code the runtime does not know fails closed: the
// hand-off still happens, with the fallback code and a pause, and the spec's
// line; it is not skipped in favour of a later action.
func TestPrecheckSignals_UnknownHandoffCodeFailsClosed(t *testing.T) {
	r := &Runtime{}
	pc := newPrecheckTurn(precheckBlock, 0)
	pc.signals = map[string]bool{"a": true, "b": true}
	pc.actions = []agentjudge.SignalAction{{Signal: "a", Handoff: "E_NOPE"}, {Signal: "b", Handoff: "E_WITHDRAW"}}
	pc.handoffLine = "spec line"
	r.precheckSignals(Conversation{}, pc)
	require.NotNil(t, pc.signalHandoff)
	require.Equal(t, precheckFallbackCode, pc.signalHandoff.Code)
	require.Equal(t, "spec line", pc.signalHandoff.ClientLine)
	require.True(t, pc.signalHandoff.ForcePause)
	require.Equal(t, "a", pc.handoffBy)
}

// A handoff criterion with a code the runtime does not know hands off with
// the fallback code and a pause instead of degrading to a signal.
func TestPGPrecheckBlock_UnknownCriterionCodeFailsClosed(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "первый ответ", "второй ответ")
	bad := agentjudge.Precheck{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEAGL_TAX")}}
	rig.judge.verdicts = []agentjudge.Precheck{bad, bad}
	out := rig.send(t, "это законно?")
	require.True(t, out.Suppressed, "the forced pause ends the turn")
	require.Len(t, rig.handoffs, 1)
	require.Equal(t, precheckFallbackCode, rig.handoffs[0].Code)
	require.True(t, rig.handoffs[0].ForcePause)
}

// A handoff violation whose code only signals, with no line of its own and
// no spec handoff_line, never delivers the draft the check refused: the
// platform line replaces it, also when the model already made the call.
func TestPGPrecheckBlock_SignalHandoffWithoutLineReplacesTheDraft(t *testing.T) {
	for _, already := range []bool{false, true} {
		rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "комиссия 5%", "комиссия 7%")
		if already {
			rig.agent.escalations = []string{"E_CHECK_AND_RETURN"}
		}
		bad := agentjudge.Precheck{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}
		rig.judge.verdicts = []agentjudge.Precheck{bad, bad}
		out := rig.send(t, "какая комиссия?")
		require.Equal(t, rig.rt.clientHandoffLine(), out.Text, "already=%v", already)
		require.NotContains(t, out.Text, "комиссия")
	}
}

// The precheck counter takes the shadow outcome under log (what block would
// have done) and the real one under block.
func TestObservePrecheck_ShadowUnderLog(t *testing.T) {
	agent := "metrics-" + uuid.NewString()[:8]
	observePrecheck(agent, map[string]any{"mode": precheckLog, "outcome": precheckPass, "shadow_outcome": precheckHandoff, "shadow_handoff_by": "fact_unbacked", "check_ms": int64(1500)})
	observePrecheck(agent, map[string]any{"mode": precheckBlock, "outcome": precheckRewrite, "check_ms": int64(800)})
	require.Equal(t, 1.0, testutil.ToFloat64(precheckOutcomes.WithLabelValues(agent, precheckLog, precheckHandoff, "fact_unbacked")))
	require.Equal(t, 1.0, testutil.ToFloat64(precheckOutcomes.WithLabelValues(agent, precheckBlock, precheckRewrite, "")))
	require.Equal(t, 0.0, testutil.ToFloat64(precheckOutcomes.WithLabelValues(agent, precheckLog, precheckPass, "")))
}

// Under a check the judge reads further back than the runtime's own guards
// (precheckHistory, not 10 messages) and sees which procedures were loaded.
func TestPGPrecheckBlock_JudgeSeesLongerHistoryAndActiveSkills(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "ответ")
	rig.judge.verdicts = []agentjudge.Precheck{{}}
	for i := 0; i < 8; i++ {
		rig.send(t, fmt.Sprintf("сообщение %d", i))
	}
	checks, _ := rig.judge.snapshot()
	last := checks[len(checks)-1]
	require.Greater(t, len(last.History), 10)
	require.Contains(t, last.Context, `"active_skills":[]`)
}

// skillLoadingAgent loads a procedure into the conversation during its call,
// the way load_skill does, then answers.
type skillLoadingAgent struct {
	store *pgStore
	skill string
	reply string
}

func (a *skillLoadingAgent) SendTraced(ctx context.Context, run AgentRunRequest) (A2AReply, error) {
	id, err := uuid.Parse(run.ConversationContext.ConversationID)
	if err != nil {
		return A2AReply{}, err
	}
	content := "procedure " + a.skill
	sum := sha256.Sum256([]byte(content))
	if _, err := a.store.ActivateSkill(ctx, id, a.skill, content, hex.EncodeToString(sum[:])); err != nil {
		return A2AReply{}, err
	}
	return A2AReply{Text: a.reply, TraceID: precheckTraceID}, nil
}

func (a *skillLoadingAgent) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	reply, err := a.SendTraced(ctx, run)
	return reply.Text, err
}

// The check judges the draft against the state after the agent's call: a
// procedure the agent loaded in this turn is in active_skills.
func TestPGPrecheckBlock_JudgeSeesProceduresLoadedThisTurn(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "unused")
	rig.rt.a2a = &skillLoadingAgent{store: rig.store, skill: "objection", reply: "Понимаю сомнение, давайте разберём"}
	rig.judge.verdicts = []agentjudge.Precheck{{}}
	out := rig.send(t, "это развод")
	require.Equal(t, "Понимаю сомнение, давайте разберём", out.Text)
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 1)
	require.Contains(t, checks[0].Context, `"active_skills":["objection"]`)
	require.Len(t, submits, 1)
	require.Contains(t, submits[0].Context, `"active_skills":["objection"]`)
}

// Under log a follow-up is checked after delivery with mode idle_log, so the
// share of dialogues block would hand off leaves it out: a follow-up never
// hands off.
func TestPGPrecheckLog_IdleFollowUpRecordedAsIdleLog(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_PRECHECK", "log")
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agentName := "idle-log-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = store.pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = store.pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err := store.pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[20],"direct_only":true}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require.NoError(t, err)
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "-900779", Actor{ExternalID: "900779"})
	require.NoError(t, err)
	agent := &scriptedAgent{drafts: []string{"Давайте закончим регистрацию!"}}
	judge := &fakePrecheck{done: make(chan struct{}, 1), verdicts: []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}}}
	rt := NewRuntime(store, &noopHooks{}, agent, nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.judge, rt.ext.precheck = judge, judge
	sched := NewIdleScheduler(store.pool, rt, agent, &fakeOutbound{onSend: func(_, _, _ string) {}}, time.Second)
	reply, err := sched.InvokeNow(ctx, conv)
	require.NoError(t, err)
	require.Equal(t, "Давайте закончим регистрацию!", reply, "log changes nothing")
	select {
	case <-judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle log check never submitted")
	}
	_, submits := judge.snapshot()
	require.Len(t, submits, 1)
	require.Equal(t, precheckIdleLog, submits[0].Precheck["mode"])
	require.Equal(t, precheckHandoff, submits[0].Precheck["shadow_outcome"])
}
