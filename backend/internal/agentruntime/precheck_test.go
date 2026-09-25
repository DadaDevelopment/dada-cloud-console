package agentruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

const precheckTraceID = "0af7651916cd43dd8448eb211c80319c"

// fakePrecheck is a TurnJudge whose Check answers from a script, one verdict
// per call (the last one repeats), optionally waiting on release.
type fakePrecheck struct {
	mu       sync.Mutex
	verdicts []agentjudge.Precheck
	errs     []error
	release  chan struct{}
	// delays is how long each Check takes (the last repeats), cut by ctx.
	delays   []time.Duration
	criteria map[string]agentjudge.Criterion
	checks   []agentjudge.Turn
	submits  []agentjudge.Turn
	done     chan struct{}
}

func (f *fakePrecheck) Submit(_ string, t agentjudge.Turn) {
	f.mu.Lock()
	f.submits = append(f.submits, t)
	f.mu.Unlock()
	if f.done != nil {
		f.done <- struct{}{}
	}
}

func (f *fakePrecheck) Check(ctx context.Context, _ string, t agentjudge.Turn) (agentjudge.Precheck, error) {
	f.mu.Lock()
	i := len(f.checks)
	f.checks = append(f.checks, t)
	f.mu.Unlock()
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return agentjudge.Precheck{}, ctx.Err()
		}
	}
	if len(f.delays) > 0 {
		select {
		case <-time.After(f.delays[min(i, len(f.delays)-1)]):
		case <-ctx.Done():
			return agentjudge.Precheck{}, ctx.Err()
		}
	}
	var err error
	if len(f.errs) > 0 {
		err = f.errs[min(i, len(f.errs)-1)]
	}
	if len(f.verdicts) == 0 {
		return agentjudge.Precheck{}, err
	}
	return f.verdicts[min(i, len(f.verdicts)-1)], err
}

func (f *fakePrecheck) Criterion(_, id string) (agentjudge.Criterion, bool) {
	c, ok := f.criteria[id]
	return c, ok
}

func (f *fakePrecheck) snapshot() ([]agentjudge.Turn, []agentjudge.Turn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentjudge.Turn{}, f.checks...), append([]agentjudge.Turn{}, f.submits...)
}

// scriptedAgent answers each call with the next draft (the last repeats) and
// records the runs it got.
type scriptedAgent struct {
	mu     sync.Mutex
	drafts []string
	kb     []agentjudge.KBResult
	// kbs, when set, is the kb_search result of each call (the last repeats)
	// instead of kb; escalations are the escalate_to_operator codes of every call.
	kbs         [][]agentjudge.KBResult
	escalations []string
	runs        []AgentRunRequest
	// delay is how long each call takes: the rewrite's time, which the
	// check budget must not count.
	delay time.Duration
}

func (a *scriptedAgent) SendTraced(_ context.Context, run AgentRunRequest) (A2AReply, error) {
	if a.delay > 0 {
		time.Sleep(a.delay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	i := min(len(a.runs), len(a.drafts)-1)
	kb := a.kb
	if len(a.kbs) > 0 {
		kb = a.kbs[min(len(a.runs), len(a.kbs)-1)]
	}
	a.runs = append(a.runs, run)
	return A2AReply{Text: a.drafts[i], TraceID: precheckTraceID, ObservationID: "b7ad6b7169203331", KB: kb, Escalations: a.escalations}, nil
}

func (a *scriptedAgent) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	reply, err := a.SendTraced(ctx, run)
	return reply.Text, err
}

func violation(id, block, ask, code string) agentjudge.Violation {
	return agentjudge.Violation{ID: id, Block: block, Ask: ask, Why: "why " + id, Code: code}
}

// precheckRuntime is a runtime on a real store with the scripted agent, the
// fake judge as both scorer and checker, and a recorded hand-off.
type precheckRig struct {
	rt       *Runtime
	store    *pgStore
	agent    *scriptedAgent
	judge    *fakePrecheck
	agentKey string
	handoffs []HandoffRequest
	paused   bool
	// pauseFails makes a forced pause fail (PauseAgent down).
	pauseFails bool
}

func newPrecheckRig(t *testing.T, env map[string]string, drafts ...string) *precheckRig {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	store := setupTestStore(t).(*pgStore)
	rig := &precheckRig{store: store, agent: &scriptedAgent{drafts: drafts}, judge: &fakePrecheck{}, agentKey: "precheck-" + uuid.NewString()}
	rig.rt = NewRuntime(store, testHooks{}, rig.agent, nil)
	rig.rt.contextKey = []byte(testRuntimeToken)
	rig.rt.judge = rig.judge
	rig.rt.ext.precheck = rig.judge
	rig.rt.ext.handoff = func(ctx context.Context, conv Conversation, req HandoffRequest) (HandoffResult, error) {
		rig.handoffs = append(rig.handoffs, req)
		if req.ForcePause && rig.pauseFails {
			return HandoffResult{Status: 400}, errors.New("pause rejected")
		}
		if req.ForcePause {
			// as Server.handOff: a forced pause answers the pending input itself
			require.NoError(t, rig.rt.markPendingHandled(ctx, conv.ID))
		}
		return HandoffResult{Status: 200, Paused: rig.paused || req.ForcePause}, nil
	}
	return rig
}

func (rig *precheckRig) send(t *testing.T, text string) MessageResponse {
	t.Helper()
	out, err := rig.rt.ProcessMessage(context.Background(), MessageRequest{AgentName: rig.agentKey, Channel: "telegram", ExternalID: "77",
		Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
	require.NoError(t, err)
	return out
}

// Flags off: the check is never called, the reply is the agent's first
// draft, and the judge gets exactly the turn it got before (no kb, no
// precheck record) -- the same turn as a runtime with no check wired at all.
func TestPGPrecheckOff_ReplyAndScoreUnchanged(t *testing.T) {
	kb := []agentjudge.KBResult{{Query: "q", Text: "t"}}
	withCheck := newPrecheckRig(t, nil, "Счёт открыт?")
	withCheck.agent.kb = kb
	withCheck.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockRewrite, "ask", "")}}}
	out := withCheck.send(t, "привет")

	plain := newPrecheckRig(t, nil, "Счёт открыт?")
	plain.agent.kb = kb
	plain.rt.ext = runtimeExt{}
	plainOut := plain.send(t, "привет")

	require.Equal(t, plainOut.Text, out.Text)
	require.Equal(t, plainOut.Messages, out.Messages)
	require.Equal(t, plainOut.Suppressed, out.Suppressed)
	require.Equal(t, "Счёт открыт?", out.Text)
	checks, submits := withCheck.judge.snapshot()
	require.Empty(t, checks)
	require.Len(t, withCheck.agent.runs, 1)
	require.Len(t, submits, 1)
	_, plainSubmits := plain.judge.snapshot()
	require.Len(t, plainSubmits, 1)
	require.Nil(t, submits[0].KB)
	require.Nil(t, submits[0].Precheck)
	require.Nil(t, submits[0].PrecheckVerdicts)
	require.Equal(t, plainSubmits[0], submits[0])
}

// Block, attempt 0 with violations: the asks of every violated criterion go
// back as one ReplyError (each once, spec order), the rewrite is checked
// again and delivered; the score carries the record and the verdicts.
func TestPGPrecheckBlock_RewriteJoinsAsks(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "первый ответ", "вторая версия")
	rig.agent.kb = []agentjudge.KBResult{{Query: "комиссия", Text: "без комиссии"}}
	rig.judge.verdicts = []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{
			violation("fact_unbacked", agentjudge.BlockHandoff, "ask one", "E_CHECK_AND_RETURN"),
			violation("script_repeat", agentjudge.BlockRewrite, "ask two", ""),
			violation("sell_during_distress", agentjudge.BlockRewrite, "ask one", ""),
		}},
		{},
	}
	out := rig.send(t, "какая комиссия?")

	require.Equal(t, "вторая версия", out.Text)
	require.Len(t, rig.agent.runs, 2)
	require.Empty(t, rig.agent.runs[0].ConversationContext.ReplyError)
	require.Equal(t, "ask one\nask two", rig.agent.runs[1].ConversationContext.ReplyError)
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 2)
	require.Equal(t, "первый ответ", checks[0].Reply)
	require.Equal(t, "вторая версия", checks[1].Reply)
	require.Equal(t, rig.agent.kb, checks[0].KB, "{{kb}} is what kb_search returned in this attempt")
	require.Empty(t, rig.handoffs)

	require.Len(t, submits, 1)
	rec := submits[0].Precheck
	require.Equal(t, "block", rec["mode"])
	require.Equal(t, precheckRewrite, rec["outcome"])
	require.Contains(t, rec, "duration_ms")
	attempts := rec["attempts"].([]map[string]any)
	require.Len(t, attempts, 2)
	require.Equal(t, []string{"fact_unbacked", "script_repeat", "sell_during_distress"}, attempts[0]["violations"])
	require.Equal(t, []string{}, attempts[1]["violations"])
	require.Equal(t, "вторая версия", submits[0].PrecheckReply)
	require.NotNil(t, submits[0].PrecheckVerdicts)
	require.Equal(t, rig.agent.kb, submits[0].KB)
}

// Block, attempt 1 still breaks a handoff criterion: the draft is dropped,
// the hand-off uses the criterion's code and line. A hands-off code that
// paused ends the turn silently (the line went out with the hand-off); a
// signal code (E_CHECK_AND_RETURN) delivers the line as the reply.
func TestPGPrecheckBlock_HandoffUsesCriterionLineAndCode(t *testing.T) {
	bad := agentjudge.Precheck{Violations: []agentjudge.Violation{
		violation("script_repeat", agentjudge.BlockRewrite, "ask r", ""),
		violation("legal_no_handoff", agentjudge.BlockHandoff, "ask l", "E_LEGAL_TAX"),
	}}
	criteria := map[string]agentjudge.Criterion{"legal_no_handoff": {ID: "legal_no_handoff", Line: " line from the spec ", Code: "E_LEGAL_TAX"}}

	paused := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "про налоги 13%", "всё равно про налоги")
	paused.judge.verdicts, paused.judge.criteria = []agentjudge.Precheck{bad}, criteria
	out := paused.send(t, "какой налог?")
	require.True(t, out.Suppressed)
	require.Empty(t, out.Text)
	require.Equal(t, []HandoffRequest{{Code: "E_LEGAL_TAX", Summary: "precheck legal_no_handoff: why legal_no_handoff", ClientLine: "line from the spec", ForcePause: true}}, paused.handoffs,
		"a hands-off code of a check verdict pauses, narrow mode or not")
	_, submits := paused.judge.snapshot()
	require.Len(t, submits, 1, "a handed-off turn is still scored once")
	require.Equal(t, precheckHandoff, submits[0].Precheck["outcome"])
	require.Equal(t, "legal_no_handoff", submits[0].Precheck["handoff_by"])
	require.Equal(t, "line from the spec", submits[0].Reply)
	conv, err := paused.store.FindActiveConversation(context.Background(), paused.agentKey, "telegram", "77")
	require.NoError(t, err)
	pending, err := paused.store.PendingRuntimeMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	require.Empty(t, pending, "the input is answered by the hand-off")

	signal := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "комиссия 5%", "комиссия 7%")
	signal.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}}
	signal.judge.criteria = map[string]agentjudge.Criterion{"fact_unbacked": {ID: "fact_unbacked", Line: "Уточню и вернусь", Code: "E_CHECK_AND_RETURN"}}
	out = signal.send(t, "какая комиссия?")
	require.Equal(t, "Уточню и вернусь", out.Text)
	require.Len(t, signal.handoffs, 1)
	require.Equal(t, "E_CHECK_AND_RETURN", signal.handoffs[0].Code)
	require.False(t, signal.handoffs[0].ForcePause)
	_, submits = signal.judge.snapshot()
	require.Equal(t, "fact_unbacked", submits[0].Precheck["handoff_by"])
}

// Attempt 1 with only rewrite violations: the second version goes out and
// the violation is recorded, no hand-off.
func TestPGPrecheckBlock_RewriteViolationOnAttemptOneDelivers(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "Сколько готовы вложить?", "Сколько готовы вложить в итоге?")
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}}
	out := rig.send(t, "не знаю")
	require.Equal(t, "Сколько готовы вложить в итоге?", out.Text)
	require.Empty(t, rig.handoffs)
	_, submits := rig.judge.snapshot()
	require.Equal(t, precheckRewrite, submits[0].Precheck["outcome"])
	require.Equal(t, []string{"script_repeat"}, submits[0].Precheck["attempts"].([]map[string]any)[1]["violations"])
}

// A soft check of the runtime already spent attempt 0: the check runs once,
// on attempt 1, with the attempt-1 rule (hand-off, not another rewrite).
func TestPGPrecheckBlock_AfterSoftRewriteAppliesAttemptOneRule(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "Hello, how are you?", "комиссия 7%")
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}}
	rig.judge.criteria = map[string]agentjudge.Criterion{"fact_unbacked": {Line: "Уточню и вернусь"}}
	out := rig.send(t, "какая комиссия?")
	checks, _ := rig.judge.snapshot()
	require.Len(t, checks, 1)
	require.Len(t, rig.agent.runs, 2)
	require.Equal(t, languageRepairHint, rig.agent.runs[1].ConversationContext.ReplyError)
	require.Equal(t, "Уточню и вернусь", out.Text)
}

// Timeout and a judge error deliver the draft unchecked, with the outcome.
func TestPGPrecheckBlock_TimeoutAndErrorDeliver(t *testing.T) {
	slow := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "ответ А")
	slow.rt.ext.precheckBudget = 50 * time.Millisecond
	slow.judge.release = make(chan struct{})
	slow.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("x", agentjudge.BlockHandoff, "ask", "E_OTHER")}}}
	started := time.Now()
	out := slow.send(t, "привет")
	require.Less(t, time.Since(started), 5*time.Second, "the judge's own timeout does not outlive the deadline")
	require.Equal(t, "ответ А", out.Text)
	require.Len(t, slow.agent.runs, 1)
	_, submits := slow.judge.snapshot()
	require.Equal(t, precheckTimeout, submits[0].Precheck["outcome"])
	require.Nil(t, submits[0].PrecheckVerdicts)

	broken := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block"}, "ответ А")
	broken.judge.errs = []error{errors.New("llm 500")}
	out = broken.send(t, "привет")
	require.Equal(t, "ответ А", out.Text)
	_, submits = broken.judge.snapshot()
	require.Equal(t, precheckError, submits[0].Precheck["outcome"])
	require.Equal(t, "llm 500", submits[0].Precheck["attempts"].([]map[string]any)[0]["error"])
}

// Log: the reply is delivered as the agent wrote it before the check even
// answers; one goroutine checks, then submits once with the record and
// shadow_outcome; nothing is rewritten and nothing is handed off.
func TestPGPrecheckLog_DeliversFirstThenScoresOnce(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "log"}, "комиссия 7%")
	rig.agent.kb = []agentjudge.KBResult{{Query: "комиссия", Text: "без комиссии"}}
	rig.judge.release = make(chan struct{})
	rig.judge.done = make(chan struct{}, 1)
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{
		violation("script_repeat", agentjudge.BlockRewrite, "ask", ""),
		violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN"),
	}}}
	out := rig.send(t, "какая комиссия?")
	require.Equal(t, "комиссия 7%", out.Text, "returned while the check is still waiting")
	_, submits := rig.judge.snapshot()
	require.Empty(t, submits)

	close(rig.judge.release)
	select {
	case <-rig.judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the log check never submitted")
	}
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 1)
	require.Equal(t, rig.agent.kb, checks[0].KB)
	require.Len(t, submits, 1)
	require.Equal(t, "log", submits[0].Precheck["mode"])
	require.Equal(t, precheckPass, submits[0].Precheck["outcome"])
	require.Equal(t, precheckHandoff, submits[0].Precheck["shadow_outcome"])
	require.Equal(t, "комиссия 7%", submits[0].Reply)
	require.Equal(t, submits[0].Reply, submits[0].PrecheckReply, "Submit may reuse the verdicts")
	require.Len(t, rig.agent.runs, 1)
	require.Empty(t, rig.handoffs)
}

// Refusal counters: attempts 0 and 1 of one input count once; the second
// input with the same reason drops the draft and hands off with the reason's
// code and a pause; the counters start over once an operator resumes the bot.
func TestPGPrecheckCounters_RefusalThresholdAndResume(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{
		"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_SCRIPT_COUNTERS": "on"})
	defer closeServer()
	ctx := context.Background()
	agent := &scriptedAgent{drafts: []string{"ответ А"}}
	judge := &fakePrecheck{verdicts: []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}, Signals: map[string]bool{"refusal_money": true, "distress": false}},
		{Signals: map[string]bool{"refusal_money": true}},
	}}
	srv.runtime.a2a, srv.runtime.ext.precheck, srv.runtime.judge, srv.runtime.hooks = agent, judge, judge, testHooks{}
	turn := func(text string) MessageResponse {
		resp, err := srv.runtime.ProcessMessage(ctx, MessageRequest{AgentName: client.AgentName, Channel: "telegram", ExternalID: "1001",
			Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
		require.NoError(t, err)
		return resp
	}

	first := turn("дорого")
	require.Equal(t, "ответ А", first.Text)
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"refusal_money": 1}, metadataCounts(fresh, obstacleRefusalsKey), "attempts 0 and 1 count once")

	judge.mu.Lock()
	judge.checks = nil
	judge.mu.Unlock()
	second := turn("всё равно дорого")
	require.True(t, second.Suppressed, "the draft is dropped")
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "escalated: E_TERMS_OFF_LADDER", state.PauseReason)
	require.Equal(t, "1001", out.chats[0])
	require.Equal(t, srv.runtime.clientHandoffLine(), out.texts[0], "no line field for a signal: the runtime fallback")
	require.Contains(t, out.texts[1], "E_TERMS_OFF_LADDER")
	require.Contains(t, out.texts[1], "refusal_money x2")
	_, submits := judge.snapshot()
	require.Equal(t, "refusal_money", submits[len(submits)-1].Precheck["handoff_by"])
	for _, text := range out.texts {
		require.NotContains(t, text, "ответ А")
	}

	_, err = store.pool.Exec(ctx, `UPDATE conversation_runtime_state SET agent_enabled = true, pause_reason = '', crm_status_sync = '' WHERE conversation_id = $1`, client.ID)
	require.NoError(t, err)
	judge.mu.Lock()
	judge.checks = nil
	judge.verdicts = []agentjudge.Precheck{{}}
	judge.mu.Unlock()
	third := turn("ладно, а что дальше?")
	require.Equal(t, "ответ А", third.Text)
	fresh, err = store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	require.NotContains(t, fresh.Metadata, obstacleRefusalsKey, "the operator's resume starts the counters over")
	require.NotContains(t, fresh.Metadata, refusalsPausedKey)
}

// money_with_us_lost on a draft that breaks no criterion: the sympathetic
// draft goes out, then the conversation is handed off with E_LOST_MONEY and a
// pause, without the platform's client line; distress stops the follow-up
// ladder and keeps the reply.
func TestPGPrecheckCounters_LostMoneyAndDistress(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{
		"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_SCRIPT_COUNTERS": "on", "AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	ctx := context.Background()
	agent := &scriptedAgent{drafts: []string{"сочувствую"}}
	judge := &fakePrecheck{verdicts: []agentjudge.Precheck{{Signals: map[string]bool{"distress": true}}}}
	srv.runtime.a2a, srv.runtime.ext.precheck, srv.runtime.judge, srv.runtime.hooks = agent, judge, judge, testHooks{}
	turn := func(text string) MessageResponse {
		resp, err := srv.runtime.ProcessMessage(ctx, MessageRequest{AgentName: client.AgentName, Channel: "telegram", ExternalID: "1001",
			Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
		require.NoError(t, err)
		return resp
	}

	require.Equal(t, "сочувствую", turn("у меня умер отец").Text)
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	require.EqualValues(t, idleLadderStopped, fresh.Metadata["idle_step"], "no follow-up after distress")
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)

	judge.mu.Lock()
	judge.verdicts = []agentjudge.Precheck{{Signals: map[string]bool{"money_with_us_lost": true}}}
	judge.mu.Unlock()
	require.Equal(t, "сочувствую", turn("я у вас уже потерял 1000$").Text, "the draft is the reply")
	state, err = store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled, "E_LOST_MONEY pauses even under narrow mode")
	require.Equal(t, "escalated: E_LOST_MONEY", state.PauseReason)
	require.Equal(t, []string{"4242"}, out.chats, "only the card: no platform line to the client")
	require.True(t, strings.Contains(out.texts[len(out.texts)-1], "E_LOST_MONEY"))
}

// Through the real server: a legal violation on both attempts hands off
// with E_LEGAL_TAX, which pauses under the triggers flag; the client gets
// the criterion's line. A fact violation hands off with E_CHECK_AND_RETURN,
// which only signals: the line is the reply and the bot stays live.
func TestPGPrecheckBlock_LegalPausesCheckAndReturnDoesNot(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{
		"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"})
	defer closeServer()
	ctx := context.Background()
	agent := &scriptedAgent{drafts: []string{"комиссия 7%"}}
	judge := &fakePrecheck{
		verdicts: []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}},
		criteria: map[string]agentjudge.Criterion{
			"fact_unbacked":    {Line: "Уточню и вернусь"},
			"legal_no_handoff": {Line: "legal line"},
		},
	}
	srv.runtime.a2a, srv.runtime.ext.precheck, srv.runtime.judge, srv.runtime.hooks = agent, judge, judge, testHooks{}
	turn := func(text string) MessageResponse {
		resp, err := srv.runtime.ProcessMessage(ctx, MessageRequest{AgentName: client.AgentName, Channel: "telegram", ExternalID: "1001",
			Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
		require.NoError(t, err)
		return resp
	}

	require.Equal(t, "Уточню и вернусь", turn("какая комиссия?").Text)
	require.Equal(t, []string{"4242"}, out.chats, "only the operator is paged")
	require.Contains(t, out.texts[0], "E_CHECK_AND_RETURN")
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)

	judge.mu.Lock()
	judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}}
	judge.mu.Unlock()
	require.True(t, turn("какой налог платить?").Suppressed)
	state, err = store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "escalated: E_LEGAL_TAX", state.PauseReason)
	require.Equal(t, "1001", out.chats[1])
	require.Equal(t, "legal line", out.texts[1])
}

// H1: {{kb}} of a check is everything kb_search returned in this turn: the
// rewrite on attempt 1 is checked against the lookups of attempt 0 too, and
// the Submit carries the same union, each result once.
func TestPGPrecheckBlock_KBAccumulatesAcrossAttempts(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "первый ответ", "вторая версия")
	first := agentjudge.KBResult{Query: "комиссия", Text: "без комиссии"}
	second := agentjudge.KBResult{Query: "вывод", Text: "вывод за сутки"}
	rig.agent.kbs = [][]agentjudge.KBResult{{first}, {first, second}}
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}, {}}
	out := rig.send(t, "какая комиссия и вывод?")
	require.Equal(t, "вторая версия", out.Text)
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 2)
	require.Equal(t, []agentjudge.KBResult{first}, checks[0].KB)
	require.Equal(t, []agentjudge.KBResult{first, second}, checks[1].KB, "attempt 0's lookups still back the rewrite, once each")
	require.Equal(t, []agentjudge.KBResult{first, second}, submits[0].KB)

	rig = newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "первый ответ", "вторая версия")
	rig.agent.kbs = [][]agentjudge.KBResult{{first}, nil}
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}, {}}
	rig.send(t, "какая комиссия?")
	checks, submits = rig.judge.snapshot()
	require.Equal(t, []agentjudge.KBResult{first}, checks[1].KB, "a rewrite without a new lookup keeps the first one")
	require.Equal(t, []agentjudge.KBResult{first}, submits[0].KB)
}

// H2: under block without the counters money_with_us_lost still hands off
// with E_LOST_MONEY and a pause (after the draft), and distress still stops
// the follow-ups; refusal_* signals are not counted.
func TestPGPrecheckBlock_SignalsWithoutCounters(t *testing.T) {
	store, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{
		"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1", "AGENT_RUNTIME_NARROW_ESCALATION": "1"})
	defer closeServer()
	ctx := context.Background()
	agent := &scriptedAgent{drafts: []string{"сочувствую"}}
	judge := &fakePrecheck{verdicts: []agentjudge.Precheck{{Signals: map[string]bool{"distress": true, "refusal_money": true}}}}
	srv.runtime.a2a, srv.runtime.ext.precheck, srv.runtime.judge, srv.runtime.hooks = agent, judge, judge, testHooks{}
	require.False(t, srv.runtime.flags.ScriptCounters)
	turn := func(text string) MessageResponse {
		resp, err := srv.runtime.ProcessMessage(ctx, MessageRequest{AgentName: client.AgentName, Channel: "telegram", ExternalID: "1001",
			Messages: []InboundMessage{{Content: text, ChannelMessageID: uuid.NewString()}}})
		require.NoError(t, err)
		return resp
	}

	require.Equal(t, "сочувствую", turn("у меня умер отец, и дорого").Text)
	fresh, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	require.EqualValues(t, idleLadderStopped, fresh.Metadata["idle_step"], "distress stops the follow-ups without the counters")
	require.NotContains(t, fresh.Metadata, obstacleRefusalsKey, "refusals are counted only under the counters")

	judge.mu.Lock()
	judge.verdicts = []agentjudge.Precheck{{Signals: map[string]bool{"money_with_us_lost": true}}}
	judge.mu.Unlock()
	require.Equal(t, "сочувствую", turn("я у вас уже потерял 1000$").Text)
	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.False(t, state.AgentEnabled, "E_LOST_MONEY pauses without the counters, even under narrow mode")
	require.Equal(t, "escalated: E_LOST_MONEY", state.PauseReason)
	require.Contains(t, out.texts[len(out.texts)-1], "E_LOST_MONEY")
	_, submits := judge.snapshot()
	require.Equal(t, signalMoneyWithUsLost, submits[len(submits)-1].Precheck["handoff_by"])
}

// H2, log: the signals only reach the record (shadow_outcome handoff,
// shadow_handoff_by money_with_us_lost); nothing is handed off and distress
// does not stop the follow-ups.
func TestPGPrecheckLog_SignalsOnlyShadow(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "log"}, "сочувствую")
	rig.judge.done = make(chan struct{}, 1)
	rig.judge.verdicts = []agentjudge.Precheck{{Signals: map[string]bool{"money_with_us_lost": true, "distress": true}}}
	out := rig.send(t, "я у вас уже потерял 1000$")
	require.Equal(t, "сочувствую", out.Text)
	select {
	case <-rig.judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the log check never submitted")
	}
	_, submits := rig.judge.snapshot()
	require.Equal(t, precheckPass, submits[0].Precheck["outcome"])
	require.Equal(t, precheckHandoff, submits[0].Precheck["shadow_outcome"])
	require.Equal(t, signalMoneyWithUsLost, submits[0].Precheck["shadow_handoff_by"])
	require.NotContains(t, submits[0].Precheck, "handoff_by")
	require.Empty(t, rig.handoffs)
	conv, err := rig.store.FindActiveConversation(context.Background(), rig.agentKey, "telegram", "77")
	require.NoError(t, err)
	require.NotEqualValues(t, idleLadderStopped, conv.Metadata["idle_step"], "log acts on nothing")
	state, err := rig.store.GetState(context.Background(), conv.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
}

// L1: the model already called escalate_to_operator with the verdict's code
// on this turn: the runtime does not page the operator again, the
// criterion's line still goes out. A different code is handed off as usual.
func TestPGPrecheckBlock_ModelAlreadyEscalatedSameCode(t *testing.T) {
	bad := []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}}
	criteria := map[string]agentjudge.Criterion{"fact_unbacked": {Line: "Уточню и вернусь"}}

	same := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "комиссия 5%", "комиссия 7%")
	same.agent.escalations = []string{"E_CHECK_AND_RETURN"}
	same.judge.verdicts, same.judge.criteria = bad, criteria
	require.Equal(t, "Уточню и вернусь", same.send(t, "какая комиссия?").Text)
	require.Empty(t, same.handoffs, "no second page for the code the model already used")
	_, submits := same.judge.snapshot()
	require.Equal(t, precheckHandoff, submits[0].Precheck["outcome"])
	require.Equal(t, "fact_unbacked", submits[0].Precheck["handoff_by"])

	other := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDOFF_TRIGGERS": "1"}, "комиссия 5%", "комиссия 7%")
	other.agent.escalations = []string{"E_DISTRUST"}
	other.judge.verdicts, other.judge.criteria = bad, criteria
	require.Equal(t, "Уточню и вернусь", other.send(t, "какая комиссия?").Text)
	require.Len(t, other.handoffs, 1)
	require.Equal(t, "E_CHECK_AND_RETURN", other.handoffs[0].Code)
}

// M4: under block a follow-up is not held for the check: it is delivered,
// then checked in the background and submitted once with mode idle_log;
// nothing is rewritten or handed off.
func TestPGPrecheckBlock_IdleFollowUpCheckedAfterDelivery(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_PRECHECK", "block")
	t.Setenv("AGENT_RUNTIME_HANDOFF_TRIGGERS", "1")
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agentName := "idle-precheck-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "-900777", Actor{ExternalID: "900777"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = store.pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = store.pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = store.pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[20],"direct_only":true}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require.NoError(t, err)

	agent := &scriptedAgent{drafts: []string{"Получилось пройти регистрацию?"}}
	judge := &fakePrecheck{release: make(chan struct{}), done: make(chan struct{}, 1),
		verdicts: []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("fact_unbacked", agentjudge.BlockHandoff, "ask", "E_CHECK_AND_RETURN")}}}}
	rt := NewRuntime(store, &noopHooks{}, agent, nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.judge, rt.ext.precheck = judge, judge
	var handoffs []HandoffRequest
	rt.ext.handoff = func(_ context.Context, _ Conversation, req HandoffRequest) (HandoffResult, error) {
		handoffs = append(handoffs, req)
		return HandoffResult{}, nil
	}
	sched := NewIdleScheduler(store.pool, rt, agent, &fakeOutbound{onSend: func(_, _, _ string) {}}, time.Second)

	reply, err := sched.InvokeNow(ctx, conv)
	require.NoError(t, err)
	require.Equal(t, "Получилось пройти регистрацию?", reply, "returned while the check is still waiting")
	_, submits := judge.snapshot()
	require.Empty(t, submits)

	close(judge.release)
	select {
	case <-judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle check never submitted")
	}
	checks, submits := judge.snapshot()
	require.Len(t, checks, 1)
	require.Len(t, submits, 1)
	require.Equal(t, precheckIdleLog, submits[0].Precheck["mode"])
	require.Equal(t, precheckHandoff, submits[0].Precheck["shadow_outcome"])
	require.Equal(t, "fact_unbacked", submits[0].Precheck["shadow_handoff_by"])
	require.Len(t, agent.runs, 1)
	require.Empty(t, handoffs)
}
