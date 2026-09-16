package agentruntime

import (
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
	// AGENT_RUNTIME_NARROW_RETURN_HOURS, 0 meaning the mode never lifts by
	// itself.
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
}

func runtimeFlagsFromEnv() runtimeFlags {
	return runtimeFlags{
		SeamlessHandoff:   envBool("AGENT_RUNTIME_SEAMLESS_HANDOFF", false),
		NarrowEscalation:  envBool("AGENT_RUNTIME_NARROW_ESCALATION", false),
		NarrowTopics:      ParseNarrowTopics(os.Getenv("AGENT_RUNTIME_NARROW_TOPICS")),
		NarrowReturnAfter: time.Duration(envInt("AGENT_RUNTIME_NARROW_RETURN_HOURS", 0)) * time.Hour,
		SplitReply:        envBool("AGENT_RUNTIME_SPLIT_REPLY", false),
		QuestionBudget:    envBool("AGENT_RUNTIME_QUESTION_BUDGET", false),
		AckLimit:          envInt("AGENT_RUNTIME_ACK_LIMIT", 0),
	}
}
