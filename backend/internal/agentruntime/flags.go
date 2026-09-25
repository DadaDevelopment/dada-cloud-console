package agentruntime

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Every humanness change of plan v5 ships behind one of these switches, and
// every default reproduces the behaviour that was on the wire before the
// change. Reading them here (rather than at each call site) keeps the
// "unset == today" promise checkable in one place: a flag whose default is
// not today's behaviour is a bug visible in this file.

// envBool reads an on/off switch. Anything that is not a recognised word
// leaves the default in place, so a typo in a manifest degrades to the safe
// side instead of flipping behaviour.
func envBool(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	return def
}

// envInt reads a whole number, keeping the default for anything unparseable
// and for negatives (no switch in this package has a meaningful negative).
func envInt(name string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n < 0 {
		return def
	}
	return n
}

// envDurationMS reads a millisecond count, keeping the default for anything
// unparseable or non-positive.
func envDurationMS(name string, def time.Duration) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Millisecond
}

// runtimeFlags is the per-process switch set, read once when the Runtime is
// built so one turn cannot see two different worlds.
type runtimeFlags struct {
	// SeamlessHandoff (AGENT_RUNTIME_SEAMLESS_HANDOFF, plan 4.2) drops "a
	// colleague will write to you" from the two lines the runtime itself
	// authors, and tells the model through runtime_context that it should
	// drop it too.
	SeamlessHandoff bool

	// NarrowEscalation (AGENT_RUNTIME_NARROW_ESCALATION, plan 4.1) replaces
	// the pause on a hands-off escalation with the narrow mode: the agent
	// stays enabled, answers only NarrowTopics and is silent otherwise.
	NarrowEscalation bool
	// NarrowTopics is the white list (AGENT_RUNTIME_NARROW_TOPICS overrides
	// the built-in one); NarrowReturnAfter is
	// AGENT_RUNTIME_NARROW_RETURN_HOURS, defaulting to a day. A mode that
	// never lifts by itself is a chat that goes quiet forever if the curator
	// forgets it, so "never" has to be typed out as an explicit 0 rather than
	// arrived at by leaving a variable unset.
	NarrowTopics      []*regexp.Regexp
	NarrowReturnAfter time.Duration

	// SplitReply (AGENT_RUNTIME_SPLIT_REPLY, plan 3.1/3.2) lets a turn reach
	// the customer as up to three messages and tells the prompt, through
	// runtime_context.reply_split, that it may mark the seams.
	SplitReply bool

	// QuestionBudget (AGENT_RUNTIME_QUESTION_BUDGET, plan 3.4) keeps the
	// runtime-owned questions_in_row / used_phrases counters and tells the
	// prompt to skip its question when the budget is spent.
	QuestionBudget bool

	// AckLimit (AGENT_RUNTIME_ACK_LIMIT, plan 3.3) is the length in runes
	// above which a reply to a bare confirmation is sent back for a rewrite.
	// 0 (the default) is off.
	AckLimit int

	FunnelOrder bool

	// SilenceRecovery (AGENT_RUNTIME_SILENCE_RECOVERY, plan 4.3) puts a turn
	// that ended without a message and without a deliberate Suppressed on the
	// existing turn_recovery ladder, instead of leaving the customer with
	// silence and no error anywhere.
	SilenceRecovery bool

	// Pre-delivery check (plan 2026-09-25). Every default below is today's
	// behaviour: no check, no counters, no new hand-off codes.

	// Precheck (AGENT_RUNTIME_PRECHECK) is off|log|block. log judges the
	// delivered turn once, after delivery, and records what block would have
	// done; block checks every draft before it reaches the client.
	Precheck string
	// ScriptCounters (AGENT_RUNTIME_SCRIPT_COUNTERS) counts the check's
	// refusal_* signals per conversation; RefusalHandoffAt
	// (AGENT_RUNTIME_REFUSAL_HANDOFF_AT, default 2) hands the conversation
	// off at that many refusals for one reason. Requires Precheck=block.
	ScriptCounters   bool
	RefusalHandoffAt int
	// HandoffTriggers (AGENT_RUNTIME_HANDOFF_TRIGGERS) makes E_LEGAL_TAX
	// hands-off and refuses the denylisted links (linkDenylist) in replies,
	// follow-ups and hand-off lines. E_CHECK_AND_RETURN is a signal with the
	// flag on or off. Required by Precheck=block.
	HandoffTriggers bool
}

// Modes of AGENT_RUNTIME_PRECHECK.
const (
	precheckOff   = "off"
	precheckLog   = "log"
	precheckBlock = "block"
)

// envChoice reads one of a fixed set of words; anything else keeps the
// default, same safe-side rule as envBool.
func envChoice(name, def string, allowed ...string) string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

func runtimeFlagsFromEnv() runtimeFlags {
	return runtimeFlags{
		SeamlessHandoff:   envBool("AGENT_RUNTIME_SEAMLESS_HANDOFF", false),
		NarrowEscalation:  envBool("AGENT_RUNTIME_NARROW_ESCALATION", false),
		NarrowTopics:      ParseNarrowTopics(os.Getenv("AGENT_RUNTIME_NARROW_TOPICS")),
		NarrowReturnAfter: time.Duration(envInt("AGENT_RUNTIME_NARROW_RETURN_HOURS", narrowReturnHoursDefault)) * time.Hour,
		SplitReply:        envBool("AGENT_RUNTIME_SPLIT_REPLY", false),
		QuestionBudget:    envBool("AGENT_RUNTIME_QUESTION_BUDGET", false),
		AckLimit:          envInt("AGENT_RUNTIME_ACK_LIMIT", 0),
		FunnelOrder:       envBool("AGENT_RUNTIME_FUNNEL_ORDER", false),
		SilenceRecovery:   envBool("AGENT_RUNTIME_SILENCE_RECOVERY", false),
		Precheck:          envChoice("AGENT_RUNTIME_PRECHECK", precheckOff, precheckOff, precheckLog, precheckBlock),
		ScriptCounters:    envBool("AGENT_RUNTIME_SCRIPT_COUNTERS", false),
		RefusalHandoffAt:  envInt("AGENT_RUNTIME_REFUSAL_HANDOFF_AT", refusalHandoffAtDefault),
		HandoffTriggers:   envBool("AGENT_RUNTIME_HANDOFF_TRIGGERS", false),
	}
}

// ValidateFlagsFromEnv rejects switch combinations that cannot work, so the
// process refuses to start instead of silently running without them. The
// refusal counters read the check's signals before delivery; under
// PRECHECK=log those signals arrive after the reply is gone, and a
// threshold that cannot discard the draft would hand off one turn late.
// A check switched on without its model is refused for the same reason.
func ValidateFlagsFromEnv() error {
	f := runtimeFlagsFromEnv()
	if f.Precheck != precheckOff && precheckLLMFromEnv() == nil {
		return fmt.Errorf("AGENT_RUNTIME_PRECHECK=%s requires AGENT_JUDGE_LLM_URL, AGENT_JUDGE_LLM_KEY and AGENT_JUDGE_LLM_MODEL", f.Precheck)
	}
	return f.validate()
}

func (f runtimeFlags) validate() error {
	if f.ScriptCounters && f.Precheck != precheckBlock {
		return fmt.Errorf("AGENT_RUNTIME_SCRIPT_COUNTERS=on requires AGENT_RUNTIME_PRECHECK=block (got %q)", f.Precheck)
	}
	// The block check hands off with the spec's codes: without the triggers
	// E_LEGAL_TAX does not pause.
	if f.Precheck == precheckBlock && !f.HandoffTriggers {
		return fmt.Errorf("AGENT_RUNTIME_PRECHECK=block requires AGENT_RUNTIME_HANDOFF_TRIGGERS=on")
	}
	return nil
}
