package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// Pre-delivery check (plan 2026-09-25). The criteria and every line the
// client or the model reads live in the agent's judge spec; this file is the
// mechanics: when to check, what to do with the verdict, what to record.

// precheckBudgetDefault bounds the Checks of one turn: their durations
// together stay within it, each Check runs under what the earlier ones left.
// The rewrite in between is the agent's own call and neither counts nor is
// cut; a Check with nothing left is not made. turnbudget.Precheck is the
// same budget on the turn's own deadline.
const precheckBudgetDefault = 30 * time.Second

// precheckLogParallel bounds the background Checks of log mode; one more is
// skipped with a log line instead of piling up goroutines on a slow judge.
const precheckLogParallel = 8

// precheckPartial is the attempt status of a Check that failed but still
// returned violations or signals (code criteria answered, the LLM did not):
// they are acted on and the record says the verdict was partial.
const precheckPartial = "partial"

// Outcomes of metadata.precheck.outcome (and shadow_outcome).
const (
	precheckPass    = "pass"
	precheckRewrite = "rewrite"
	precheckHandoff = "handoff"
	precheckTimeout = "timeout"
	precheckError   = "error"
)

// precheckIdleLog is metadata.precheck.mode of a follow-up checked after
// delivery under AGENT_RUNTIME_PRECHECK=block: follow-ups are not held.
const precheckIdleLog = "idle_log"

// Signal ids of the turn spec the runtime acts on under block (refusal_*
// only with ScriptCounters).
const (
	signalDistress         = "distress"
	signalMoneyWithUsLost  = "money_with_us_lost"
	signalWithdrawalStuck  = "withdrawal_stuck"
	refusalSignalPrefix    = "refusal_"
	escalationLostMoney    = "E_LOST_MONEY"
	escalationWithdraw     = "E_WITHDRAW"
	escalationOtherDefault = "E_OTHER"
)

// moneySignals are the signals that hand the conversation off with a pause
// after the client got the (sympathetic) draft, in priority order: one
// hand-off per turn, the first signal on wins.
var moneySignals = []struct{ id, code string }{
	{signalMoneyWithUsLost, escalationLostMoney},
	{signalWithdrawalStuck, escalationWithdraw},
}

// refusalHandoffCodes maps a refusal signal to the code the conversation is
// handed off with at the threshold; a refusal signal not listed here hands
// off with E_OTHER.
var refusalHandoffCodes = map[string]string{
	"refusal_distrust": "E_DISTRUST",
	"refusal_money":    "E_TERMS_OFF_LADDER",
	"refusal_time":     escalationOtherDefault,
	"refusal_other":    escalationOtherDefault,
}

// precheckTurn is one turn's check record across the attempts.
type precheckTurn struct {
	mode   string
	budget time.Duration
	start  time.Time
	// spent is the time the Checks of the turn took, the rewrite excluded.
	spent    time.Duration
	partial  bool
	outcome  string
	shadow   string
	attempts []map[string]any
	signals  map[string]bool
	acted    bool
	// handoffBy is the criterion or signal id a hand-off came from
	// (shadowHandoffBy: what block would have handed off on, in log).
	handoffBy       string
	shadowHandoffBy string

	// verdicts and reply are the last answered Check and the draft it
	// judged; Submit reuses them when that draft is what was delivered.
	verdicts *agentjudge.Precheck
	reply    string

	// firstHandoff is the first handoff violation of attempt 0: when the
	// Check of the rewrite does not answer, the turn acts on it instead of
	// delivering an unchecked rewrite (handoffAttempt0 records that).
	firstHandoff    *agentjudge.Violation
	handoffAttempt0 bool
	// signalHandoff is the hand-off a money signal asked for, waiting on the
	// draft: a clean draft goes out and deferred carries the hand-off past
	// delivery; a draft that still breaks a criterion is dropped for it.
	signalHandoff *HandoffRequest
	deferred      *HandoffRequest
}

func newPrecheckTurn(mode string, budget time.Duration) *precheckTurn {
	if budget <= 0 {
		budget = precheckBudgetDefault
	}
	return &precheckTurn{mode: mode, budget: budget}
}

// record is metadata.precheck of the turn score.
func (pc *precheckTurn) record() map[string]any {
	rec := map[string]any{"mode": pc.mode, "outcome": pc.outcome, "attempts": pc.attempts}
	if !pc.start.IsZero() {
		rec["duration_ms"] = time.Since(pc.start).Milliseconds()
		rec["check_ms"] = pc.spent.Milliseconds()
	}
	if pc.partial {
		rec[precheckPartial] = true
	}
	if pc.handoffAttempt0 {
		rec["handoff_attempt"] = 0
	}
	if pc.shadow != "" {
		rec["shadow_outcome"] = pc.shadow
	}
	if pc.handoffBy != "" {
		rec["handoff_by"] = pc.handoffBy
	}
	if pc.shadowHandoffBy != "" {
		rec["shadow_handoff_by"] = pc.shadowHandoffBy
	}
	var signals []string
	for id, on := range pc.signals {
		if on {
			signals = append(signals, id)
		}
	}
	sort.Strings(signals)
	if len(signals) > 0 {
		rec["signals"] = signals
	}
	return rec
}

// precheckOnce runs one Check under what is left of the turn's check budget
// and records the attempt. status is "" when the judge answered,
// precheckTimeout or precheckError otherwise (the verdict is then ignored).
// A Check that failed but still returned a violation or a signal that is on
// answered in part: status is precheckPartial, the verdict is returned and
// the attempt carries the error. Signals answered false do not make a partial
// verdict: agentjudge reports every answered signal, so a failed Check almost
// always has some.
func (r *Runtime) precheckOnce(ctx context.Context, agent string, t agentjudge.Turn, attempt int, pc *precheckTurn) (agentjudge.Precheck, string) {
	now := time.Now()
	if pc.start.IsZero() {
		pc.start = now
	}
	entry := map[string]any{"attempt": attempt}
	pc.attempts = append(pc.attempts, entry)
	left := pc.budget - pc.spent
	if left <= 0 {
		entry["status"] = precheckTimeout
		return agentjudge.Precheck{}, precheckTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, left)
	defer cancel()
	verdict, err := r.ext.precheck.Check(cctx, agent, t)
	pc.spent += time.Since(now)
	if err != nil {
		anyOn := false
		for _, on := range verdict.Signals {
			anyOn = anyOn || on
		}
		if len(verdict.Violations) == 0 && !anyOn {
			status := precheckError
			if cctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				status = precheckTimeout
			}
			entry["status"] = status
			entry["error"] = err.Error()
			return agentjudge.Precheck{}, status
		}
		entry["status"] = precheckPartial
		entry["error"] = err.Error()
		pc.partial = true
	}
	status := ""
	if err != nil {
		status = precheckPartial
	}
	ids := make([]string, 0, len(verdict.Violations))
	for _, v := range verdict.Violations {
		ids = append(ids, v.ID)
	}
	entry["violations"] = ids
	if pc.signals == nil {
		pc.signals = verdict.Signals
	}
	pc.verdicts, pc.reply = &verdict, t.Reply
	return verdict, status
}

// precheckAction is what the loop does with a checked draft.
type precheckAction int

const (
	precheckDeliver precheckAction = iota
	precheckRedo
	precheckHandOff
)

type precheckDecision struct {
	action  precheckAction
	ask     string
	handoff HandoffRequest
}

// precheckDraft is the block-mode rule for one draft. Attempt 0 with any
// violation goes back with the violated criteria's asks. Attempt 1 (also when
// a soft check already spent attempt 0) with a handoff violation hands off
// with that criterion's code and line; with only rewrite violations the
// draft goes out and the violation is recorded. A timeout or a judge error
// delivers the draft as is, except on attempt 1 after attempt 0 broke a
// handoff criterion: the turn then hands off on that violation
// (precheckFallback). A partial verdict is acted on, except on attempt 1
// without a handoff violation: the draft is then as unchecked as after an
// error and takes the same way (its signals still count). The signals
// of the first answered Check may hand the turn off before any of that; a
// money signal waits on the draft (precheckSignals). A criterion's code that
// hands off (escalationHandsOff, E_LEGAL_TAX under the triggers) pauses even
// under narrow mode; a signal code (E_CHECK_AND_RETURN) only pages the
// operator.
func (r *Runtime) precheckDraft(ctx context.Context, conv Conversation, t agentjudge.Turn, pending []Message, attempt int, pc *precheckTurn) precheckDecision {
	verdict, status := r.precheckOnce(ctx, conv.AgentName, t, attempt, pc)
	if status == precheckPartial {
		status = ""
		if attempt > 0 && !slices.ContainsFunc(verdict.Violations, func(v agentjudge.Violation) bool { return v.Block == agentjudge.BlockHandoff }) {
			if d, ok := r.precheckSignals(ctx, conv, pending, pc); ok {
				pc.outcome = precheckHandoff
				return d
			}
			status = precheckError
		}
	}
	if status != "" {
		pc.outcome = status
		if attempt == 0 {
			return precheckDecision{action: precheckDeliver}
		}
		return r.precheckFallback(conv, pc)
	}
	if d, ok := r.precheckSignals(ctx, conv, pending, pc); ok {
		pc.outcome = precheckHandoff
		return d
	}
	if len(verdict.Violations) == 0 {
		if pc.outcome == "" {
			pc.outcome = precheckPass
		}
		if pc.signalHandoff != nil {
			pc.deferred, pc.signalHandoff, pc.outcome = pc.signalHandoff, nil, precheckHandoff
		}
		return precheckDecision{action: precheckDeliver}
	}
	if attempt == 0 {
		pc.outcome = precheckRewrite
		for _, v := range verdict.Violations {
			if v.Block == agentjudge.BlockHandoff {
				pc.firstHandoff = &v
				break
			}
		}
		return precheckDecision{action: precheckRedo, ask: precheckAsk(verdict.Violations)}
	}
	if req := pc.signalHandoff; req != nil {
		pc.signalHandoff, pc.outcome = nil, precheckHandoff
		return precheckDecision{action: precheckHandOff, handoff: *req}
	}
	for _, v := range verdict.Violations {
		if v.Block == agentjudge.BlockHandoff {
			return r.precheckViolationHandoff(conv, v, pc)
		}
	}
	pc.outcome = precheckRewrite
	return precheckDecision{action: precheckDeliver}
}

// precheckFallback is attempt 1 when its draft is unchecked (the Check
// timed out, failed or answered only in part, or the model fell silent): a
// money hand-off attempt 0 asked for takes the turn, else the handoff
// violation of attempt 0 does, else the draft goes out as is.
func (r *Runtime) precheckFallback(conv Conversation, pc *precheckTurn) precheckDecision {
	if req := pc.signalHandoff; req != nil {
		pc.signalHandoff, pc.outcome = nil, precheckHandoff
		return precheckDecision{action: precheckHandOff, handoff: *req}
	}
	if v := pc.firstHandoff; v != nil {
		pc.handoffAttempt0 = true
		return r.precheckViolationHandoff(conv, *v, pc)
	}
	return precheckDecision{action: precheckDeliver}
}

// precheckViolationHandoff hands off on a handoff violation with the
// criterion's code and line.
func (r *Runtime) precheckViolationHandoff(conv Conversation, v agentjudge.Violation, pc *precheckTurn) precheckDecision {
	pc.outcome, pc.handoffBy = precheckHandoff, v.ID
	line := ""
	if c, ok := r.ext.precheck.Criterion(conv.AgentName, v.ID); ok {
		line = strings.TrimSpace(c.Line)
	}
	return precheckDecision{action: precheckHandOff, handoff: HandoffRequest{Code: v.Code, Summary: precheckSummary(v.ID, v.Why), ClientLine: line, ForcePause: r.codeHandsOff(v.Code)}}
}

// precheckAsk joins the asks of the violated criteria, each once, in spec
// order: the whole text comes from the spec.
func precheckAsk(violations []agentjudge.Violation) string {
	seen := map[string]bool{}
	var asks []string
	for _, v := range violations {
		ask := strings.TrimSpace(v.Ask)
		if ask == "" || seen[ask] {
			continue
		}
		seen[ask] = true
		asks = append(asks, ask)
	}
	return strings.Join(asks, "\n")
}

// precheckSummary is the operator card's summary for a check-decided
// hand-off: the criterion id and the judge's own reason.
func precheckSummary(id, why string) string {
	if why = strings.TrimSpace(why); why != "" {
		return "precheck " + id + ": " + why
	}
	return "precheck " + id
}

// precheckSignals acts on the signals of the turn's first answered Check,
// once per turn: money_with_us_lost (E_LOST_MONEY) or else withdrawal_stuck
// (E_WITHDRAW) asks for a hand-off with a pause but does not drop the draft:
// the sympathetic draft that breaks no criterion goes out, then the
// conversation is handed off without a platform line (afterCheckedTurn); a
// draft that still breaks one is dropped for the hand-off with the platform
// line. Under AGENT_RUNTIME_SCRIPT_COUNTERS each refusal_*
// signal is counted (deduplicated by the pending message ids, so attempts 0
// and 1 and a recovery replay count once) and at
// AGENT_RUNTIME_REFUSAL_HANDOFF_AT refusals for one reason the draft is
// dropped and the conversation handed off with a pause. The client gets the
// runtime's hand-off line: the spec has no line field for a signal.
func (r *Runtime) precheckSignals(ctx context.Context, conv Conversation, pending []Message, pc *precheckTurn) (precheckDecision, bool) {
	if pc.acted || pc.signals == nil {
		return precheckDecision{}, false
	}
	pc.acted = true
	for _, sig := range moneySignals {
		if !pc.signals[sig.id] {
			continue
		}
		pc.handoffBy = sig.id
		pc.signalHandoff = &HandoffRequest{Code: sig.code, Summary: "precheck " + sig.id, ForcePause: true}
		log.Info().Str("conversation", conv.ID.String()).Str("signal", sig.id).Str("code", sig.code).Msg("agentruntime: precheck money signal, handing off after the draft")
		return precheckDecision{}, false
	}
	if !r.flags.ScriptCounters {
		return precheckDecision{}, false
	}
	store, ok := r.store.(counterStore)
	if !ok {
		log.Warn().Str("conversation", conv.ID.String()).Msg("agentruntime: refusal counter storage is not configured")
		return precheckDecision{}, false
	}
	msgIDs := make([]string, 0, len(pending))
	for _, m := range pending {
		msgIDs = append(msgIDs, m.ID.String())
	}
	var keys []string
	for id, on := range pc.signals {
		if on && strings.HasPrefix(id, refusalSignalPrefix) {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		count, err := store.RecordRefusal(ctx, conv.ID, key, msgIDs)
		if err != nil {
			log.Warn().Err(err).Str("conversation", conv.ID.String()).Str("refusal", key).Msg("agentruntime: refusal not recorded")
			continue
		}
		if at := r.flags.RefusalHandoffAt; at <= 0 || count < at {
			continue
		}
		code := refusalHandoffCodes[key]
		if code == "" {
			code = escalationOtherDefault
		}
		pc.handoffBy = key
		log.Info().Str("conversation", conv.ID.String()).Str("refusal", key).Int("count", count).Str("code", code).
			Msg("agentruntime: refusal threshold reached, handing off")
		return precheckDecision{action: precheckHandOff, handoff: HandoffRequest{Code: code, Summary: fmt.Sprintf("precheck %s x%d", key, count), ForcePause: true}}, true
	}
	return precheckDecision{}, false
}

// precheckStepResult tells the attempt loop what to do after a check.
type precheckStepResult int

const (
	precheckStepDeliver precheckStepResult = iota
	precheckStepRedo
	precheckStepDone
)

// precheckStep checks the draft that passed every other guard of this
// attempt. Redo: the run carries the asks as ReplyError, the loop goes on.
// Done: a hand-off ended the turn, resp is what runTurn returns. Deliver: the
// draft (or, for a signal-code hand-off, the criterion's line in *reply)
// goes out as usual. When the model already called escalate_to_operator with
// the code of the hand-off (and the call went through), the runtime does not
// page the operator a second time: a hand-off without a forced pause is not
// repeated at all (only the criterion's line goes out), a forced one pauses
// without a second card. A forced pause that did not pause is an error: the
// turn goes to the recovery ladder instead of delivering anything.
func (r *Runtime) precheckStep(ctx context.Context, conv Conversation, run *AgentRunRequest, after RuntimeState, pending, history []Message, reply *string, traced A2AReply, attempt int, pc *precheckTurn) (precheckStepResult, MessageResponse, error) {
	if isSilenceReply(*reply) {
		return r.precheckSilence(ctx, conv, run, after, pending, history, reply, traced, pc)
	}
	d := r.precheckDraft(ctx, conv, r.judgeInput(conv, *run, pending, history, *reply, nil, traced), pending, attempt, pc)
	return r.precheckAct(ctx, conv, run, after, pending, history, reply, traced, pc, d)
}

// precheckSilence is a deliberate silence (empty, SKIP) of the rewrite (the
// caller does not check attempt 0's silence): it is not checked, but a
// hand-off attempt 0 left waiting (a money signal, a handoff violation) is
// carried out as after a failed Check, not lost in the silence.
func (r *Runtime) precheckSilence(ctx context.Context, conv Conversation, run *AgentRunRequest, after RuntimeState, pending, history []Message, reply *string, traced A2AReply, pc *precheckTurn) (precheckStepResult, MessageResponse, error) {
	d := r.precheckFallback(conv, pc)
	if d.action != precheckHandOff {
		return precheckStepDeliver, MessageResponse{}, nil
	}
	return r.precheckAct(ctx, conv, run, after, pending, history, reply, traced, pc, d)
}

// precheckAct carries out a precheck decision for the draft in *reply.
func (r *Runtime) precheckAct(ctx context.Context, conv Conversation, run *AgentRunRequest, after RuntimeState, pending, history []Message, reply *string, traced A2AReply, pc *precheckTurn, d precheckDecision) (precheckStepResult, MessageResponse, error) {
	switch d.action {
	case precheckRedo:
		log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Msg("agentruntime: reply sent back for a rewrite by the precheck")
		run.ConversationContext.State = after
		run.ConversationContext.ReplyError = d.ask
		return precheckStepRedo, MessageResponse{}, nil
	case precheckHandOff:
		already := slices.Contains(traced.Escalations, d.handoff.Code)
		if !d.handoff.ForcePause && already {
			log.Info().Str("conversation", conv.ID.String()).Str("code", d.handoff.Code).Msg("agentruntime: precheck hand-off already called by the model, not repeated")
		} else {
			req := d.handoff
			req.NoCard = already
			ended, err := r.precheckHandOff(ctx, conv, req)
			if err != nil {
				logPrecheck(conv, pc.record())
				return precheckStepDone, MessageResponse{}, err
			}
			if ended {
				line := d.handoff.ClientLine
				if line == "" {
					line = r.clientHandoffLine()
				}
				logPrecheck(conv, pc.record())
				r.judgeTurn(ctx, conv, *run, pending, history, line, nil, traced, pc)
				return precheckStepDone, MessageResponse{Suppressed: true}, nil
			}
		}
		if d.handoff.ClientLine != "" {
			*reply = d.handoff.ClientLine
		}
	case precheckDeliver:
		if pc.deferred != nil && slices.Contains(traced.Escalations, pc.deferred.Code) {
			pc.deferred.NoCard = true
		}
	}
	return precheckStepDeliver, MessageResponse{}, nil
}

// precheckHandOff carries out a check-decided hand-off and reports whether
// it ended the turn: the agent was paused or the client was already told
// (hands-off code, narrow mode). The pending input is then marked handled,
// since no reply of this turn will be saved. A signal code (such as
// E_CHECK_AND_RETURN) only pages the operator and returns false: the
// criterion's line then goes out as the turn's reply. A forced pause that
// did not pause returns an error: nothing of this turn may go out as if the
// conversation had been handed off.
func (r *Runtime) precheckHandOff(ctx context.Context, conv Conversation, req HandoffRequest) (bool, error) {
	if r.ext.handoff == nil {
		log.Warn().Str("conversation", conv.ID.String()).Str("code", req.Code).Msg("agentruntime: precheck hand-off without a hand-off path, delivering the line")
		return false, nil
	}
	res, err := r.ext.handoff(ctx, conv, req)
	if err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Str("code", req.Code).Msg("agentruntime: precheck hand-off failed")
	}
	if req.ForcePause && !res.Paused {
		log.Error().Err(err).Str("conversation", conv.ID.String()).Str("code", req.Code).Msg("agentruntime: precheck forced pause did not pause, turn not delivered")
		return false, fmt.Errorf("precheck hand-off %s: agent not paused: %v", req.Code, err)
	}
	if !res.Paused && !res.ClientNotified {
		return false, nil
	}
	if !req.ForcePause {
		if err := r.markPendingHandled(ctx, conv.ID); err != nil {
			log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: precheck hand-off could not drain the pending inbox")
		}
	}
	return true, nil
}

// afterCheckedTurn runs after a checked turn was delivered: the hand-off a
// money signal left for after the draft (no platform line: the draft was the
// reply), the log line, and a distress signal stops the follow-up ladder
// until the client writes again. A turn no Check ran on (a deliberate
// silence) has nothing to record.
func (r *Runtime) afterCheckedTurn(ctx context.Context, conv Conversation, pc *precheckTurn) {
	if pc.start.IsZero() {
		return
	}
	if req := pc.deferred; req != nil {
		req.NoClientLine = true
		if ended, err := r.precheckHandOff(ctx, conv, *req); err != nil || !ended {
			log.Error().Err(err).Str("conversation", conv.ID.String()).Str("code", req.Code).Msg("agentruntime: draft delivered but the money hand-off did not pause")
		}
	}
	logPrecheck(conv, pc.record())
	if !pc.signals[signalDistress] {
		return
	}
	store, ok := r.store.(counterStore)
	if !ok {
		return
	}
	if err := store.StopIdleLadder(ctx, conv.ID); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: follow-ups not stopped after distress")
	}
}

// resetRefusalsAfterResume clears the refusal counters of a conversation an
// operator returned to the bot: a turn runs only while the agent is enabled,
// so a pause mark found here means the pause was lifted.
func (r *Runtime) resetRefusalsAfterResume(ctx context.Context, conv Conversation) {
	if !r.flags.ScriptCounters {
		return
	}
	store, ok := r.store.(counterStore)
	if !ok {
		return
	}
	reset, err := store.ResetRefusalsAfterPause(ctx, conv.ID)
	if err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: refusal counters not reset after resume")
		return
	}
	if reset {
		log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: agent resumed, refusal counters reset")
	}
}

// precheckLogged is AGENT_RUNTIME_PRECHECK=log (and, with mode idle_log, a
// follow-up under block): after delivery, one Check of the delivered reply
// under the same budget, then the one Submit of the turn with the record.
// Nothing is rewritten, no tool is called and no signal is acted on:
// shadow_outcome says what block would have started with (handoff on
// money_with_us_lost or when a handoff criterion failed, rewrite for any
// other violation) and shadow_handoff_by what it would have handed off on.
func (r *Runtime) precheckLogged(conv Conversation, t agentjudge.Turn, traced bool, mode string) {
	defer func() {
		if p := recover(); p != nil {
			log.Error().Interface("panic", p).Str("conversation", conv.ID.String()).Msg("agentruntime: precheck log panicked")
		}
	}()
	pc := newPrecheckTurn(mode, r.ext.precheckBudget)
	verdict, status := r.precheckOnce(context.Background(), conv.AgentName, t, 0, pc)
	pc.outcome, pc.shadow = precheckPass, precheckPass
	if status != "" && status != precheckPartial {
		pc.outcome, pc.shadow = status, ""
	}
	for _, v := range verdict.Violations {
		if v.Block == agentjudge.BlockHandoff {
			pc.shadow, pc.shadowHandoffBy = precheckHandoff, v.ID
			break
		}
		pc.shadow = precheckRewrite
	}
	for _, sig := range moneySignals {
		if verdict.Signals[sig.id] {
			pc.shadow, pc.shadowHandoffBy = precheckHandoff, sig.id
			break
		}
	}
	rec := pc.record()
	logPrecheck(conv, rec)
	if r.judge == nil || !traced {
		return
	}
	t.Precheck, t.PrecheckVerdicts, t.PrecheckReply = rec, pc.verdicts, pc.reply
	r.judge.Submit(conv.AgentName, t)
}

// goPrecheckLogged runs precheckLogged in the background, at most
// precheckLogParallel at a time; past that the check is skipped and logged.
func (r *Runtime) goPrecheckLogged(conv Conversation, t agentjudge.Turn, traced bool, mode string) {
	slots := r.ext.logSlots
	if slots == nil {
		go r.precheckLogged(conv, t, traced, mode)
		return
	}
	select {
	case slots <- struct{}{}:
	default:
		log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("mode", mode).Int("in_flight", cap(slots)).
			Msg("agentruntime: precheck log skipped, too many checks in flight")
		return
	}
	go func() {
		defer func() { <-slots }()
		r.precheckLogged(conv, t, traced, mode)
	}()
}

// logPrecheck is the one log line per checked turn.
func logPrecheck(conv Conversation, rec map[string]any) {
	e := log.Info().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Interface("precheck", rec)
	e.Msg("agentruntime: precheck")
}
