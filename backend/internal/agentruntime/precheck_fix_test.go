package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

var blockEnv = map[string]string{"AGENT_RUNTIME_PRECHECK": "block", "AGENT_RUNTIME_HANDS_OFF_CODES": "E_LEGAL_TAX"}

// The check budget counts only the Checks: a rewrite slower than the whole
// budget still gets its Check, and the record says how long the Checks took.
func TestPGPrecheckBlock_BudgetExcludesTheRewrite(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "первый ответ", "вторая версия")
	rig.rt.ext.precheckBudget = 200 * time.Millisecond
	rig.agent.delay = 300 * time.Millisecond
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}, {}}
	out := rig.send(t, "не знаю")
	require.Equal(t, "вторая версия", out.Text)
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 2, "the rewrite's Check is made although the rewrite took longer than the budget")
	rec := submits[0].Precheck
	require.Equal(t, precheckRewrite, rec["outcome"])
	require.Less(t, rec["check_ms"].(int64), int64(200))
	require.GreaterOrEqual(t, rec["duration_ms"].(int64), int64(300), "duration_ms is the wall time, rewrite included")
	attempts := rec["attempts"].([]map[string]any)
	require.NotContains(t, attempts[1], "status")
}

// The Checks together use up the budget: the rewrite's Check times out, and
// since attempt 0 broke a handoff criterion the turn hands off on it (code
// and line of that criterion) instead of delivering an unchecked rewrite.
func TestPGPrecheckBlock_RewriteCheckTimeoutActsOnAttemptZero(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "комиссия 5%", "комиссия 7%")
	rig.rt.ext.precheckBudget = 150 * time.Millisecond
	rig.judge.delays = []time.Duration{100 * time.Millisecond, time.Second}
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{
		violation("script_repeat", agentjudge.BlockRewrite, "ask r", ""),
		violation("fact_unbacked", agentjudge.BlockHandoff, "ask f", "E_CHECK_AND_RETURN"),
	}}, {}}
	rig.judge.criteria = map[string]agentjudge.Criterion{"fact_unbacked": {Line: "Уточню и вернусь"}}
	started := time.Now()
	out := rig.send(t, "какая комиссия?")
	require.Less(t, time.Since(started), 2*time.Second, "the second Check gets only what the first one left")
	require.Equal(t, "Уточню и вернусь", out.Text)
	require.Len(t, rig.handoffs, 1)
	require.Equal(t, HandoffRequest{Code: "E_CHECK_AND_RETURN", Summary: "precheck fact_unbacked: why fact_unbacked", ClientLine: "Уточню и вернусь"}, rig.handoffs[0])
	_, submits := rig.judge.snapshot()
	rec := submits[0].Precheck
	require.Equal(t, precheckHandoff, rec["outcome"])
	require.Equal(t, "fact_unbacked", rec["handoff_by"])
	require.Equal(t, 0, rec["handoff_attempt"])
	require.Equal(t, precheckTimeout, rec["attempts"].([]map[string]any)[1]["status"])

	// A judge error on the rewrite acts the same; with only rewrite
	// violations on attempt 0 the rewrite goes out unchecked, as before.
	broken := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	broken.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}, {}}
	broken.judge.errs = []error{nil, errors.New("llm 500")}
	broken.judge.criteria = map[string]agentjudge.Criterion{"legal_no_handoff": {Line: "legal line"}}
	require.True(t, broken.send(t, "какой налог?").Suppressed)
	require.Len(t, broken.handoffs, 1)
	require.Equal(t, "E_LEGAL_TAX", broken.handoffs[0].Code)
	require.True(t, broken.handoffs[0].ForcePause)
	require.Equal(t, "legal line", broken.handoffs[0].ClientLine)

	soft := newPrecheckRig(t, blockEnv, "первый", "второй")
	soft.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}, {}}
	soft.judge.errs = []error{nil, errors.New("llm 500")}
	require.Equal(t, "второй", soft.send(t, "ну").Text)
	require.Empty(t, soft.handoffs)
	_, submits = soft.judge.snapshot()
	require.Equal(t, precheckError, submits[0].Precheck["outcome"])
}

// A Check that failed but returned violations is acted on: the violations
// send the draft back, the attempt says partial and carries the error.
func TestPGPrecheckBlock_PartialVerdictIsApplied(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "первый ответ", "вторая версия")
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask one", "")}}, {}}
	rig.judge.errs = []error{errors.New("judge reply unparsable"), nil}
	out := rig.send(t, "не знаю")
	require.Equal(t, "вторая версия", out.Text)
	require.Equal(t, "ask one (why script_repeat)", rig.agent.runs[1].ConversationContext.ReplyError)
	_, submits := rig.judge.snapshot()
	rec := submits[0].Precheck
	require.Equal(t, precheckRewrite, rec["outcome"])
	require.Equal(t, true, rec[precheckPartial])
	attempts := rec["attempts"].([]map[string]any)
	require.Equal(t, precheckPartial, attempts[0]["status"])
	require.Equal(t, "judge reply unparsable", attempts[0]["error"])
	require.Equal(t, []string{"script_repeat"}, attempts[0]["violations"])

	// An error without violations or signals is still an error.
	failed := newPrecheckRig(t, blockEnv, "ответ")
	failed.judge.errs = []error{errors.New("spec broken")}
	require.Equal(t, "ответ", failed.send(t, "привет").Text)
	_, submits = failed.judge.snapshot()
	require.Equal(t, precheckError, submits[0].Precheck["outcome"])
	require.NotContains(t, submits[0].Precheck, precheckPartial)
}

// A deliberate silence (empty reply, SKIP) is not checked and carries no
// check record.
func TestPGPrecheckBlock_SilenceIsNotChecked(t *testing.T) {
	for _, silent := range []string{"", "SKIP"} {
		rig := newPrecheckRig(t, blockEnv, silent)
		rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("x", agentjudge.BlockHandoff, "ask", "E_OTHER")}}}
		rig.send(t, "ок")
		checks, submits := rig.judge.snapshot()
		require.Empty(t, checks, "reply %q", silent)
		require.Empty(t, rig.handoffs)
		require.Len(t, rig.agent.runs, 1)
		for _, s := range submits {
			require.Nil(t, s.Precheck)
		}
	}
}

// A forced pause that did not pause (PauseAgent down): nothing goes out as
// if the chat had been handed off; the turn fails into the recovery ladder
// and the input stays pending.
func TestPGPrecheckBlock_ForcedPauseFailureFailsTheTurn(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	rig.pauseFails = true
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}}
	rig.judge.criteria = map[string]agentjudge.Criterion{"legal_no_handoff": {Line: "legal line"}}
	rig.rt.recoveryDelays = []time.Duration{time.Hour}
	out, err := rig.rt.ProcessMessage(context.Background(), MessageRequest{AgentName: rig.agentKey, Channel: "telegram", ExternalID: "77",
		Messages: []InboundMessage{{Content: "какой налог?", ChannelMessageID: uuid.NewString()}}})
	require.Error(t, err)
	var failure *turnFailure
	require.ErrorAs(t, err, &failure)
	require.Empty(t, out.Text)
	require.Len(t, rig.handoffs, 1)
	conv, err := rig.store.FindActiveConversation(context.Background(), rig.agentKey, "telegram", "77")
	require.NoError(t, err)
	pending, err := rig.store.PendingRuntimeMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the input waits for the recovery ladder")
	history, err := rig.store.GetRecentMessages(context.Background(), conv.ID, 10)
	require.NoError(t, err)
	for _, m := range history {
		require.NotEqual(t, "assistant", m.Role, "nothing was saved as a reply")
	}
}

// The model's own escalate_to_operator already sent the card for the code
// of a forced hand-off: the runtime pauses without a second card.
func TestPGPrecheckBlock_ForcedHandoffSkipsCardTheModelSent(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	rig.agent.escalations = []string{"E_LEGAL_TAX"}
	rig.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}}
	require.True(t, rig.send(t, "какой налог?").Suppressed)
	require.Len(t, rig.handoffs, 1)
	require.True(t, rig.handoffs[0].ForcePause)
	require.True(t, rig.handoffs[0].NoCard)

	other := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	other.agent.escalations = []string{"E_DISTRUST"}
	other.judge.verdicts = rig.judge.verdicts
	other.send(t, "какой налог?")
	require.False(t, other.handoffs[0].NoCard)
}

// Server side of NoCard and NoClientLine: the pause happens, the skipped
// message does not go out.
func TestPGHandOff_NoCardAndNoClientLine(t *testing.T) {
	_, srv, out, client, _, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDS_OFF_CODES": "E_LEGAL_TAX"})
	res, err := srv.handOff(context.Background(), client, HandoffRequest{Code: "E_LOST_MONEY", Summary: "s", ClientLine: "line", ForcePause: true, NoCard: true})
	require.NoError(t, err)
	require.True(t, res.Paused)
	require.False(t, res.OperatorNotified)
	require.Equal(t, []string{"1001"}, out.chats)
	closeServer()

	_, srv, out, client, _, closeServer = escalateTestServer(t, map[string]string{"AGENT_RUNTIME_HANDS_OFF_CODES": "E_LEGAL_TAX"})
	defer closeServer()
	res, err = srv.handOff(context.Background(), client, HandoffRequest{Code: "E_LOST_MONEY", Summary: "s", ForcePause: true, NoClientLine: true})
	require.NoError(t, err)
	require.True(t, res.Paused)
	require.False(t, res.ClientNotified)
	require.Equal(t, []string{"4242"}, out.chats)
}

// withdrawal_stuck hands off with E_WITHDRAW after the draft, like
// money_with_us_lost; both on one turn make one hand-off, E_LOST_MONEY. The
// model's own call with the code spares the second card.
func TestPGPrecheckBlock_MoneySignalsHandOffAfterTheDraft(t *testing.T) {
	cases := []struct {
		signals     map[string]bool
		escalations []string
		code, by    string
		noCard      bool
	}{
		{map[string]bool{"withdrawal_stuck": true}, nil, "E_WITHDRAW", "withdrawal_stuck", false},
		{map[string]bool{"withdrawal_stuck": true, "money_with_us_lost": true}, nil, "E_LOST_MONEY", "money_with_us_lost", false},
		{map[string]bool{"money_with_us_lost": true}, []string{"E_LOST_MONEY"}, "E_LOST_MONEY", "money_with_us_lost", true},
	}
	for _, c := range cases {
		rig := newPrecheckRig(t, blockEnv, "сочувствую, давайте разберёмся")
		rig.agent.escalations = c.escalations
		rig.judge.verdicts = []agentjudge.Precheck{{Signals: c.signals}}
		require.Equal(t, "сочувствую, давайте разберёмся", rig.send(t, "деньги не выводятся").Text)
		require.Equal(t, []HandoffRequest{{Code: c.code, Summary: "precheck " + c.by, ForcePause: true, NoClientLine: true, NoCard: c.noCard}}, rig.handoffs)
		_, submits := rig.judge.snapshot()
		require.Equal(t, precheckHandoff, submits[0].Precheck["outcome"])
		require.Equal(t, c.by, submits[0].Precheck["handoff_by"])
		require.Equal(t, "сочувствую, давайте разберёмся", submits[0].Reply)
	}

	// The draft breaks a criterion: it goes back once; a clean rewrite goes
	// out and the hand-off follows it.
	rewrite := newPrecheckRig(t, blockEnv, "сочувствую, а депозит?", "сочувствую")
	rewrite.judge.verdicts = []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("sell_during_distress", agentjudge.BlockRewrite, "ask", "")}, Signals: map[string]bool{"money_with_us_lost": true}},
		{},
	}
	require.Equal(t, "сочувствую", rewrite.send(t, "я потерял деньги у вас").Text)
	require.Len(t, rewrite.handoffs, 1)
	require.True(t, rewrite.handoffs[0].NoClientLine)

	// The rewrite still breaks one: the draft is dropped for the hand-off
	// with the platform line, as before.
	dropped := newPrecheckRig(t, blockEnv, "сочувствую, а депозит?", "а депозит?")
	dropped.judge.verdicts = []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("sell_during_distress", agentjudge.BlockRewrite, "ask", "")}, Signals: map[string]bool{"money_with_us_lost": true}},
	}
	require.True(t, dropped.send(t, "я потерял деньги у вас").Suppressed)
	require.Equal(t, []HandoffRequest{{Code: "E_LOST_MONEY", Summary: "precheck money_with_us_lost", ForcePause: true}}, dropped.handoffs)

	// The rewrite's Check does not answer: the draft is not delivered
	// unchecked, the hand-off takes the turn.
	timeout := newPrecheckRig(t, blockEnv, "сочувствую, а депозит?", "сочувствую")
	timeout.judge.verdicts = append(append([]agentjudge.Precheck{}, dropped.judge.verdicts...), agentjudge.Precheck{})
	timeout.judge.errs = []error{nil, errors.New("llm 500")}
	require.True(t, timeout.send(t, "я потерял деньги у вас").Suppressed)
	require.Equal(t, "E_LOST_MONEY", timeout.handoffs[0].Code)
	require.False(t, timeout.handoffs[0].NoClientLine)
}

// log mode: withdrawal_stuck is a shadow hand-off too.
func TestPGPrecheckLog_WithdrawalStuckShadow(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "log"}, "сочувствую")
	rig.judge.done = make(chan struct{}, 1)
	rig.judge.verdicts = []agentjudge.Precheck{{Signals: map[string]bool{"withdrawal_stuck": true}}}
	rig.send(t, "вывод завис")
	select {
	case <-rig.judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the log check never submitted")
	}
	_, submits := rig.judge.snapshot()
	require.Equal(t, precheckHandoff, submits[0].Precheck["shadow_outcome"])
	require.Equal(t, signalWithdrawalStuck, submits[0].Precheck["shadow_handoff_by"])
	require.Empty(t, rig.handoffs)
}

// log mode runs at most precheckLogParallel Checks at a time; one more is
// skipped (logged), not queued.
func TestPGPrecheckLog_BoundedInFlight(t *testing.T) {
	rig := newPrecheckRig(t, map[string]string{"AGENT_RUNTIME_PRECHECK": "log"}, "ответ")
	require.Equal(t, precheckLogParallel, cap(rig.rt.ext.logSlots))
	rig.rt.ext.logSlots = make(chan struct{}, 1)
	rig.judge.release = make(chan struct{})
	rig.judge.done = make(chan struct{}, 2)
	rig.send(t, "первое")
	require.Eventually(t, func() bool { checks, _ := rig.judge.snapshot(); return len(checks) == 1 }, 5*time.Second, 10*time.Millisecond)
	rig.send(t, "второе")
	close(rig.judge.release)
	select {
	case <-rig.judge.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the log check never submitted")
	}
	time.Sleep(100 * time.Millisecond)
	checks, submits := rig.judge.snapshot()
	require.Len(t, checks, 1, "the second check found no slot and was skipped")
	require.Len(t, submits, 1)
	require.Len(t, rig.rt.ext.logSlots, 0, "the slot is given back")

	rig.send(t, "третье")
	require.Eventually(t, func() bool { checks, _ := rig.judge.snapshot(); return len(checks) == 2 }, 5*time.Second, 10*time.Millisecond)
}

// Under AGENT_RUNTIME_PRECHECK=block a hand-off line with a link outside the
// reply link allowlist is replaced with the platform line (the same list the
// turn's own replies are held to); with the check off it goes out as before.
func TestPGEscalate_ClientMessageLinkOutsideAllowlist(t *testing.T) {
	line := "Обзор здесь: https://t.me/robinhoodmetals/58"
	for _, mode := range []string{"block", "off"} {
		_, srv, out, _, target, closeServer := escalateTestServer(t, map[string]string{"AGENT_RUNTIME_PRECHECK": mode, "AGENT_REPLY_LINK_ALLOWLIST": "direct-fxpro.com"})
		base, token, _ := strings.Cut(target, "|")
		status, res := postRuntime(t, base, "/tools/escalate", map[string]any{"context_token": token, "reason_code": "E_PAYMENT_UNCONFIRMED", "summary": "Деньги ушли, зачисления нет.", "client_message": line}, testRuntimeToken)
		require.Equal(t, 200, status, res)
		require.Equal(t, "1001", out.chats[0])
		if mode == "block" {
			require.Equal(t, srv.runtime.clientHandoffLine(), out.texts[0])
		} else {
			require.Equal(t, line, out.texts[0], "check off: unchanged")
		}
		closeServer()
	}
}

// Under AGENT_RUNTIME_PRECHECK=block a follow-up with a link outside the
// reply link allowlist is dropped; with the check off it goes out as before.
func TestIdleScheduler_DropsFollowUpWithLinkOutsideAllowlist(t *testing.T) {
	for _, mode := range []string{"block", "off"} {
		t.Setenv("AGENT_RUNTIME_PRECHECK", mode)
		store := setupTestStore(t).(*pgStore)
		ctx := context.Background()
		agentName := "idle-deny-" + uuid.NewString()[:8]
		conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-deny", Actor{ExternalID: "u1"})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = store.pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
			_, _ = store.pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
		})
		_, err = store.pool.Exec(ctx, `
			INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
			VALUES ($1, 'follow-up', 'conversation.idle', '{"idle_minutes":0}', 'schedule', '{"agent_message":"дожим"}')
		`, agentName)
		require.NoError(t, err)
		_, err = store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "привет"})
		require.NoError(t, err)

		agent := &scriptedAgent{drafts: []string{"Обзор здесь: https://t.me/robinhoodmetals/58"}}
		var delivered []string
		rt := NewRuntime(store, &noopHooks{}, agent, nil)
		rt.contextKey = []byte(testRuntimeToken)
		rt.linkAllowlist = []string{"direct-fxpro.com"}
		judge := &fakePrecheck{}
		rt.ext.precheck = judge
		sched := NewIdleScheduler(store.pool, rt, agent, &fakeOutbound{onSend: func(_, _, text string) { delivered = append(delivered, text) }}, time.Second)
		sched.now = idleDaytime
		require.NoError(t, sched.Tick(ctx))
		if mode == "block" {
			require.Empty(t, delivered)
		} else {
			require.Equal(t, []string{"Обзор здесь: https://t.me/robinhoodmetals/58"}, delivered)
		}
	}
}

// agentjudge reports every answered signal, false ones too: a failed Check
// with only false signals is an error, not a partial verdict. On attempt 0
// the draft goes out (Q8); on attempt 1 after a handoff violation of attempt
// 0 the turn hands off on it.
func TestPGPrecheckBlock_FalseSignalsDoNotMakeAPartialVerdict(t *testing.T) {
	off := map[string]bool{signalDistress: false, signalMoneyWithUsLost: false, signalWithdrawalStuck: false}
	first := newPrecheckRig(t, blockEnv, "ответ")
	first.judge.verdicts = []agentjudge.Precheck{{Signals: off}}
	first.judge.errs = []error{errors.New("null verdict for block criterion")}
	require.Equal(t, "ответ", first.send(t, "привет").Text)
	require.Empty(t, first.handoffs)
	_, submits := first.judge.snapshot()
	rec := submits[0].Precheck
	require.Equal(t, precheckError, rec["outcome"])
	require.NotContains(t, rec, precheckPartial)
	require.Equal(t, precheckError, rec["attempts"].([]map[string]any)[0]["status"])

	second := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	second.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}, {Signals: off}}
	second.judge.errs = []error{nil, errors.New("null verdict for block criterion")}
	second.judge.criteria = map[string]agentjudge.Criterion{"legal_no_handoff": {Line: "legal line"}}
	require.True(t, second.send(t, "какой налог?").Suppressed)
	require.Len(t, second.handoffs, 1)
	require.Equal(t, "E_LEGAL_TAX", second.handoffs[0].Code)
	require.Equal(t, "legal line", second.handoffs[0].ClientLine)
	_, submits = second.judge.snapshot()
	rec = submits[0].Precheck
	require.Equal(t, precheckHandoff, rec["outcome"])
	require.Equal(t, 0, rec["handoff_attempt"])
	require.Equal(t, precheckError, rec["attempts"].([]map[string]any)[1]["status"])
}

// A partial verdict of the rewrite without a handoff violation leaves the
// rewrite as unchecked as an error does: attempt 0's handoff violation takes
// the turn. A partial verdict of attempt 0 with only a signal on is acted on.
func TestPGPrecheckBlock_PartialRewriteVerdictFallsBack(t *testing.T) {
	rig := newPrecheckRig(t, blockEnv, "про налоги 13%", "всё равно про налоги")
	rig.judge.verdicts = []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}},
		{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}},
	}
	rig.judge.errs = []error{nil, errors.New("null verdict for block criterion")}
	require.True(t, rig.send(t, "какой налог?").Suppressed)
	require.Len(t, rig.handoffs, 1)
	require.Equal(t, "E_LEGAL_TAX", rig.handoffs[0].Code)
	_, submits := rig.judge.snapshot()
	require.Equal(t, 0, submits[0].Precheck["handoff_attempt"])
	require.Equal(t, precheckPartial, submits[0].Precheck["attempts"].([]map[string]any)[1]["status"])

	signal := newPrecheckRig(t, blockEnv, "сочувствую, давайте разберёмся")
	signal.judge.verdicts = []agentjudge.Precheck{{Signals: map[string]bool{signalMoneyWithUsLost: true, signalDistress: false}}}
	signal.judge.errs = []error{errors.New("null verdict for block criterion")}
	require.Equal(t, "сочувствую, давайте разберёмся", signal.send(t, "я потерял деньги у вас").Text)
	require.Equal(t, []HandoffRequest{{Code: "E_LOST_MONEY", Summary: "precheck money_with_us_lost", ForcePause: true, NoClientLine: true}}, signal.handoffs)
	_, submits = signal.judge.snapshot()
	require.Equal(t, true, submits[0].Precheck[precheckPartial])
}

// The rewrite is a silence (SKIP): a hand-off attempt 0 left waiting is not
// lost in it. Without one the silence stays a silence.
func TestPGPrecheckBlock_SilentRewriteKeepsTheHandoff(t *testing.T) {
	money := newPrecheckRig(t, blockEnv, "сочувствую, а депозит?", "SKIP")
	money.judge.verdicts = []agentjudge.Precheck{
		{Violations: []agentjudge.Violation{violation("sell_during_distress", agentjudge.BlockRewrite, "ask", "")}, Signals: map[string]bool{signalMoneyWithUsLost: true}},
	}
	require.True(t, money.send(t, "я потерял деньги у вас").Suppressed)
	require.Equal(t, []HandoffRequest{{Code: "E_LOST_MONEY", Summary: "precheck money_with_us_lost", ForcePause: true}}, money.handoffs)
	checks, submits := money.judge.snapshot()
	require.Len(t, checks, 1, "the silence is not checked")
	require.Equal(t, precheckHandoff, submits[0].Precheck["outcome"])

	legal := newPrecheckRig(t, blockEnv, "про налоги 13%", "SKIP")
	legal.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("legal_no_handoff", agentjudge.BlockHandoff, "ask", "E_LEGAL_TAX")}}}
	legal.judge.criteria = map[string]agentjudge.Criterion{"legal_no_handoff": {Line: "legal line"}}
	require.True(t, legal.send(t, "какой налог?").Suppressed)
	require.Len(t, legal.handoffs, 1)
	require.Equal(t, "E_LEGAL_TAX", legal.handoffs[0].Code)
	_, submits = legal.judge.snapshot()
	require.Equal(t, 0, submits[0].Precheck["handoff_attempt"])
	require.Equal(t, "legal_no_handoff", submits[0].Precheck["handoff_by"])

	soft := newPrecheckRig(t, blockEnv, "первый", "SKIP")
	soft.judge.verdicts = []agentjudge.Precheck{{Violations: []agentjudge.Violation{violation("script_repeat", agentjudge.BlockRewrite, "ask", "")}}}
	require.False(t, soft.send(t, "ну").Suppressed)
	require.Empty(t, soft.handoffs)
	checks, _ = soft.judge.snapshot()
	require.Len(t, checks, 1)
}
