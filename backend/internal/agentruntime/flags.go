package agentruntime

import (
	"os"
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
}

func runtimeFlagsFromEnv() runtimeFlags {
	return runtimeFlags{
		SeamlessHandoff: envBool("AGENT_RUNTIME_SEAMLESS_HANDOFF", false),
	}
}
