package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// precheckBudgetDefault bounds the Checks of one turn (plan 2026-09-25): their
// durations together stay within it, each Check runs under what the earlier
// ones left. The rewrite in between is the agent's own call and neither
// counts nor is cut; a Check with nothing left is not made.
// turnbudget.Precheck is the same budget on the turn's own deadline. The
// criteria, the asks, the lines and what a signal starts all live in the
// agent's judge spec; this file is the mechanics: when to check, what to do
// with the verdict, what to record.
const precheckBudgetDefault = 30 * time.Second

// precheckClientLineBudget bounds the check of the hand-off line the model
// wrote into escalate_to_operator: that check runs inside the model's own
// tool call, which the agent's turn timeout also covers.
const precheckClientLineBudget = 15 * time.Second

// precheckHistory is how many recent messages a check sees. A second refusal
// or a repeated script question can lie further back than the ten messages
// the runtime's own guards read.
const precheckHistory = 30

// precheckFallbackCode is the hand-off code a check uses when the spec names
// a code the runtime does not know: a person takes the conversation over
// (hands-off, pause) rather than a typo quietly turning a blocking rule into
// a card nobody acts on.
const precheckFallbackCode = "E_OTHER"

// precheckLogParallel bounds the background Checks of log mode; one more is
// skipped with a log line instead of piling up goroutines on a slow judge.
const precheckLogParallel = 8

// precheckPartial is the attempt status of a Check that failed but still
// returned violations or signals (code criteria answered, the LLM did not):
// they are acted on and the record says the verdict was partial.
const precheckPartial = "partial"

// Outcomes of metadata.precheck.outcome (and shadow_outcome). drop is a
// follow-up the check held back after its one rewrite, or a model-written
// hand-off line replaced by the spec's line.
const (
	precheckPass    = "pass"
	precheckRewrite = "rewrite"
	precheckHandoff = "handoff"
	precheckTimeout = "timeout"
	precheckError   = "error"
	precheckDrop    = "drop"
)

// Modes of metadata.precheck.mode beyond off|log|block: a follow-up checked
// before delivery under block, a follow-up checked after delivery under log
// (a follow-up never hands off, so the log share leaves it out), and the
// hand-off line of the model's own escalate_to_operator call checked under
// block.
const (
	precheckIdleBlock  = "idle_block"
	precheckIdleLog    = "idle_log"
	precheckClientLine = "client_line"
)

// precheckTurn is one turn's check record across the attempts: the time the
// Checks took (spent, the rewrite excluded), the attempts, the signals and
// signal actions of the first answered Check with the spec's hand-off line,
// the hand-off the turn came to (handoffBy; shadowHandoffBy is what block
// would have handed off on, in log), the last answered verdict and the draft
// it judged (Submit reuses them when that draft is what went out), the first
// handoff violation of attempt 0 (acted on when the rewrite's Check does not
// answer, handoffAttempt0 records that) and the hand-off a signal asked for:
// waiting on the draft (signalHandoff), or carried past delivery (deferred).
type precheckTurn struct {
	mode            string
	budget          time.Duration
	start           time.Time
	spent           time.Duration
	partial         bool
	outcome         string
	shadow          string
	attempts        []map[string]any
	signals         map[string]bool
	actions         []agentjudge.SignalAction
	handoffLine     string
	acted           bool
	handoffBy       string
	shadowHandoffBy string
	verdicts        *agentjudge.Precheck
	reply           string
	firstHandoff    *agentjudge.Violation
	handoffAttempt0 bool
	signalHandoff   *HandoffRequest
	deferred        *HandoffRequest
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

// stopsFollowups reports whether a marked signal of the turn asks the
// follow-up ladder to stop.
func (pc *precheckTurn) stopsFollowups() bool {
	for _, a := range pc.actions {
		if a.StopFollowups {
			return true
		}
	}
	return false
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
		pc.signals, pc.actions, pc.handoffLine = verdict.Signals, verdict.Actions, verdict.HandoffLine
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

// hasHandoffViolation reports whether a verdict breaks a handoff criterion.
func hasHandoffViolation(violations []agentjudge.Violation) bool {
	return slices.ContainsFunc(violations, func(v agentjudge.Violation) bool { return v.Block == agentjudge.BlockHandoff })
}

// precheckDraft is the block-mode rule for one draft. Attempt 0 with any
// violation goes back with the violated criteria's asks. Attempt 1 (also when
// a runtime guard already spent attempt 0) with a handoff violation hands off
// with that criterion's code and line; with only rewrite violations the
// draft goes out and the violation is recorded. A timeout or a judge error
// delivers the draft as is, except on attempt 1 after attempt 0 broke a
// handoff criterion: the turn then hands off on that violation
// (precheckFallback). A partial verdict is acted on, except on attempt 1
// without a handoff violation: the draft is then as unchecked as after an
// error and takes the same way (its signals still count). A signal whose
// spec action names a hand-off code waits on the draft (precheckSignals). A
// criterion's code that hands off (escalationHandsOff and
// AGENT_RUNTIME_HANDS_OFF_CODES) pauses even under narrow mode; a signal code
// (E_CHECK_AND_RETURN) only pages the operator.
func (r *Runtime) precheckDraft(ctx context.Context, conv Conversation, t agentjudge.Turn, attempt int, pc *precheckTurn) precheckDecision {
	verdict, status := r.precheckOnce(ctx, conv.AgentName, t, attempt, pc)
	if status == precheckPartial {
		status = ""
		if attempt > 0 && !hasHandoffViolation(verdict.Violations) {
			r.precheckSignals(conv, pc)
			status = precheckError
		}
	}
	if status != "" {
		pc.outcome = status
		if attempt == 0 {
			return precheckDecision{action: precheckDeliver}
		}
		return r.precheckFallback(pc)
	}
	r.precheckSignals(conv, pc)
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
			return r.precheckViolationHandoff(v, pc)
		}
	}
	pc.outcome = precheckRewrite
	return precheckDecision{action: precheckDeliver}
}

// precheckFallback is attempt 1 when its draft is unchecked (the Check
// timed out, failed or answered only in part, or the model fell silent): a
// signal hand-off attempt 0 asked for takes the turn, else the handoff
// violation of attempt 0 does, else the draft goes out as is.
func (r *Runtime) precheckFallback(pc *precheckTurn) precheckDecision {
	if req := pc.signalHandoff; req != nil {
		pc.signalHandoff, pc.outcome = nil, precheckHandoff
		return precheckDecision{action: precheckHandOff, handoff: *req}
	}
	if v := pc.firstHandoff; v != nil {
		pc.handoffAttempt0 = true
		return r.precheckViolationHandoff(*v, pc)
	}
	return precheckDecision{action: precheckDeliver}
}

// precheckViolationHandoff hands off on a handoff violation with the
// criterion's code and the line agentjudge resolved for it (the criterion's
// own, else the spec's handoff_line; empty falls back to the platform line).
// A code the runtime does not know fails closed: precheckFallbackCode with a
// pause.
func (r *Runtime) precheckViolationHandoff(v agentjudge.Violation, pc *precheckTurn) precheckDecision {
	pc.outcome, pc.handoffBy = precheckHandoff, v.ID
	code, force := v.Code, r.codeHandsOff(v.Code)
	if _, known := escalationReasons[code]; !known {
		log.Error().Str("criterion", v.ID).Str("code", code).Str("fallback", precheckFallbackCode).Msg("agentruntime: precheck criterion names an unknown hand-off code, handing off with a pause")
		code, force = precheckFallbackCode, true
	}
	return precheckDecision{action: precheckHandOff, handoff: HandoffRequest{Code: code, Summary: precheckSummary(v.ID, v.Why), ClientLine: strings.TrimSpace(v.Line), ForcePause: force}}
}

// precheckAsk is the rewrite request: the asks of the violated criteria in
// spec order, each once, each followed by the judge's own reasons for the
// violations that raised it, so the model learns what to change and where.
// The asks come from the spec, the reasons from the judge; no text here.
func precheckAsk(violations []agentjudge.Violation) string {
	var order []string
	whys := map[string][]string{}
	for _, v := range violations {
		ask := strings.TrimSpace(v.Ask)
		if ask == "" {
			continue
		}
		if _, seen := whys[ask]; !seen {
			order = append(order, ask)
			whys[ask] = nil
		}
		if why := strings.TrimSpace(v.Why); why != "" && !slices.Contains(whys[ask], why) {
			whys[ask] = append(whys[ask], why)
		}
	}
	lines := make([]string, 0, len(order))
	for _, ask := range order {
		if reasons := whys[ask]; len(reasons) > 0 {
			ask += " (" + strings.Join(reasons, "; ") + ")"
		}
		lines = append(lines, ask)
	}
	return strings.Join(lines, "\n")
}

// precheckSummary is the operator card's summary for a check-decided
// hand-off: the criterion id and the judge's own reason.
func precheckSummary(id, why string) string {
	if why = strings.TrimSpace(why); why != "" {
		return "precheck " + id + ": " + why
	}
	return "precheck " + id
}

// precheckSignals acts on the signal actions of the turn's first answered
// Check, once per turn: the first action in spec order whose hand-off code
// the runtime knows asks for a hand-off with a pause, but does not drop the
// draft. A draft that breaks no criterion goes out and the conversation is
// handed off after it without a line (afterCheckedTurn); a draft that still
// breaks one is dropped for the hand-off with the spec's hand-off line. A
// code the runtime does not know fails closed: precheckFallbackCode, still
// with the pause, logged as an error (the spec names codes, the runtime owns
// the list).
func (r *Runtime) precheckSignals(conv Conversation, pc *precheckTurn) {
	if pc.acted || pc.signals == nil {
		return
	}
	pc.acted = true
	for _, a := range pc.actions {
		if a.Handoff == "" {
			continue
		}
		code := a.Handoff
		if _, known := escalationReasons[code]; !known {
			log.Error().Str("conversation", conv.ID.String()).Str("signal", a.Signal).Str("code", code).Str("fallback", precheckFallbackCode).Msg("agentruntime: precheck signal names an unknown hand-off code, handing off with the fallback code")
			code = precheckFallbackCode
		}
		pc.handoffBy = a.Signal
		pc.signalHandoff = &HandoffRequest{Code: code, Summary: "precheck " + a.Signal, ClientLine: pc.handoffLine, ForcePause: true}
		log.Info().Str("conversation", conv.ID.String()).Str("signal", a.Signal).Str("code", code).Msg("agentruntime: precheck signal, handing off after the draft")
		return
	}
}

// precheckStepResult tells the attempt loop what to do after a check.
type precheckStepResult int

const (
	precheckStepDeliver precheckStepResult = iota
	precheckStepRedo
	precheckStepDone
)

// precheckStep checks the draft that passed every other guard of this
// attempt, judged against the state after the agent's call (the facts it
// recorded and the procedures it loaded in this turn). Redo: the run carries
// the asks as ReplyError, the loop goes on.
// Done: a hand-off ended the turn, resp is what runTurn returns. Deliver: the
// draft (or, for a signal-code hand-off, the criterion's line in *reply)
// goes out as usual; a hand-off the turn does not end on always replaces the
// draft, with the platform line when the spec gives none, since the draft is
// the one the check refused. When the model already called escalate_to_operator with
// the code of the hand-off (and the call went through), the runtime does not
// page the operator a second time: a hand-off without a forced pause is not
// repeated at all (only the criterion's line goes out), a forced one pauses
// without a second card. A forced pause that did not pause is an error: the
// turn goes to the recovery ladder instead of delivering anything.
func (r *Runtime) precheckStep(ctx context.Context, conv Conversation, run *AgentRunRequest, after RuntimeState, pending, history []Message, reply *string, traced A2AReply, attempt int, pc *precheckTurn) (precheckStepResult, MessageResponse, error) {
	if isSilenceReply(*reply) {
		return r.precheckSilence(ctx, conv, run, after, pending, history, reply, traced, pc)
	}
	checked := *run
	checked.ConversationContext.State = after
	d := r.precheckDraft(ctx, conv, r.judgeInput(conv, checked, pending, history, *reply, nil, traced), attempt, pc)
	return r.precheckAct(ctx, conv, run, after, pending, history, reply, traced, pc, d)
}

// precheckWithSoftHint runs the attempt-0 Check on a draft a runtime guard
// already sent back, so the one rewrite the turn has carries both the
// guard's hint and the asks of the criteria the draft breaks; without it the
// judge would see only the rewrite, and a handoff criterion there hands off
// with no second chance. Without a block check (or for a silence) the hint
// goes back alone.
func (r *Runtime) precheckWithSoftHint(ctx context.Context, conv Conversation, run AgentRunRequest, pending, history []Message, reply string, traced A2AReply, pc *precheckTurn, hint string) string {
	if pc == nil || isSilenceReply(reply) {
		return hint
	}
	d := r.precheckDraft(ctx, conv, r.judgeInput(conv, run, pending, history, reply, nil, traced), 0, pc)
	if d.action == precheckRedo && d.ask != "" {
		return hint + "\n" + d.ask
	}
	return hint
}

// precheckSilence is a deliberate silence (empty, SKIP) of the rewrite (the
// caller does not check attempt 0's silence): it is not checked, but a
// hand-off attempt 0 left waiting (a signal hand-off, a handoff violation) is
// carried out as after a failed Check, not lost in the silence.
func (r *Runtime) precheckSilence(ctx context.Context, conv Conversation, run *AgentRunRequest, after RuntimeState, pending, history []Message, reply *string, traced A2AReply, pc *precheckTurn) (precheckStepResult, MessageResponse, error) {
	d := r.precheckFallback(pc)
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
		line := d.handoff.ClientLine
		if line == "" {
			line = r.clientHandoffLine()
		}
		*reply = line
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
// signal left for after the draft (no line: the draft was the reply), the log
// line, and a signal whose spec action says stop_followups ends the
// follow-up ladder until the client writes again. A turn no Check ran on (a
// deliberate silence) has nothing to record.
func (r *Runtime) afterCheckedTurn(ctx context.Context, conv Conversation, pc *precheckTurn) {
	if pc.start.IsZero() {
		return
	}
	if req := pc.deferred; req != nil {
		req.NoClientLine = true
		if ended, err := r.precheckHandOff(ctx, conv, *req); err != nil || !ended {
			log.Error().Err(err).Str("conversation", conv.ID.String()).Str("code", req.Code).Msg("agentruntime: draft delivered but the signal hand-off did not pause")
		}
	}
	logPrecheck(conv, pc.record())
	if !pc.stopsFollowups() {
		return
	}
	store, ok := r.store.(followupStopper)
	if !ok {
		return
	}
	if err := store.StopIdleLadder(ctx, conv.ID); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: follow-ups not stopped after a stop_followups signal")
	}
}

// precheckFollowUp is the block-mode check of an idle follow-up before it
// goes out. Attempt 0 with any violation returns the asks for the one
// rewrite; attempt 1 with any violation drops the follow-up (a skipped
// follow-up is silence, a bad one is a message the client did not ask for).
// Signals start nothing here, and a Check that does not answer lets the
// follow-up go, as for a turn.
func (r *Runtime) precheckFollowUp(ctx context.Context, conv Conversation, t agentjudge.Turn, attempt int, pc *precheckTurn) (ask string, drop bool) {
	verdict, status := r.precheckOnce(ctx, conv.AgentName, t, attempt, pc)
	if status != "" && status != precheckPartial {
		pc.outcome = status
		return "", false
	}
	if len(verdict.Violations) == 0 {
		if pc.outcome == "" {
			pc.outcome = precheckPass
		}
		return "", false
	}
	if attempt == 0 {
		pc.outcome = precheckRewrite
		return precheckAsk(verdict.Violations), false
	}
	pc.outcome = precheckDrop
	return "", true
}

// checkedClientLine checks, under AGENT_RUNTIME_PRECHECK=block, the line the
// model wrote into escalate_to_operator before it reaches the client: the
// tool sends that line by itself, outside the turn the runtime checks. A line
// that breaks a block criterion is replaced by the handoff violation's line,
// else the spec's hand-off line, else the platform line; a check that does
// not answer lets the model's line go, as for a turn.
func (r *Runtime) checkedClientLine(ctx context.Context, conv Conversation, line string) string {
	if r.flags.Precheck != precheckBlock || r.ext.precheck == nil || strings.TrimSpace(line) == "" {
		return line
	}
	state, err := r.states.GetState(ctx, conv.ID)
	if err != nil {
		return line
	}
	history, err := r.store.GetRecentMessages(ctx, conv.ID, precheckHistory)
	if err != nil {
		return line
	}
	var pending []Message
	if inbox, ok := r.store.(interface {
		PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error)
	}); ok {
		pending, _ = inbox.PendingRuntimeMessages(ctx, conv.ID)
	}
	pc := newPrecheckTurn(precheckClientLine, precheckClientLineBudget)
	run := AgentRunRequest{ConversationContext: AgentConversationContext{State: state}}
	verdict, status := r.precheckOnce(ctx, conv.AgentName, r.judgeInput(conv, run, pending, history, line, nil, A2AReply{}), 0, pc)
	switch {
	case status != "" && status != precheckPartial:
		pc.outcome = status
	case len(verdict.Violations) == 0:
		pc.outcome = precheckPass
	default:
		pc.outcome, pc.handoffBy = precheckDrop, verdict.Violations[0].ID
		replacement := verdict.HandoffLine
		for _, v := range verdict.Violations {
			if v.Block == agentjudge.BlockHandoff && strings.TrimSpace(v.Line) != "" {
				replacement = v.Line
				break
			}
		}
		if strings.TrimSpace(replacement) == "" {
			replacement = r.clientHandoffLine()
		}
		line = replacement
	}
	logPrecheck(conv, pc.record())
	return line
}

// precheckLogged is AGENT_RUNTIME_PRECHECK=log: after delivery, one Check of
// the delivered reply under the same budget, then the one Submit of the turn
// with the record. Nothing is rewritten, no tool is called and no signal is
// acted on: shadow_outcome says what block would have started with (handoff
// when a handoff criterion failed or a marked signal names a hand-off code,
// rewrite for any other violation) and shadow_handoff_by what it would have
// handed off on.
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
	for _, a := range verdict.Actions {
		if a.Handoff != "" {
			pc.shadow, pc.shadowHandoffBy = precheckHandoff, a.Signal
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

// logPrecheck is the one log line per checked turn, follow-up or hand-off
// line, and the same record counted in the precheck metrics.
func logPrecheck(conv Conversation, rec map[string]any) {
	e := log.Info().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Interface("precheck", rec)
	e.Msg("agentruntime: precheck")
	observePrecheck(conv.AgentName, rec)
}
