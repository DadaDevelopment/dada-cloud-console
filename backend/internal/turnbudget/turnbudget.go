package turnbudget

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAgentCall = 150 * time.Second
	agentCallEnv     = "AGENT_CALL_TIMEOUT_SECONDS"
	runtimeOverhead  = 30 * time.Second
	gatewayOverhead  = 15 * time.Second

	// Precheck is the budget of the pre-delivery check of one turn under
	// AGENT_RUNTIME_PRECHECK=block: the Checks of a turn together take at
	// most this long (agentruntime's precheckBudgetDefault).
	Precheck    = 30 * time.Second
	precheckEnv = "AGENT_RUNTIME_PRECHECK"
)

func AgentCall() time.Duration {
	if raw := os.Getenv(agentCallEnv); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return DefaultAgentCall
}

// RuntimeTurn bounds one turn in the runtime: two agent calls (the draft and
// its one rewrite) plus the overhead, and under AGENT_RUNTIME_PRECHECK=block
// the check budget on top, so a turn whose Checks used it all still reaches
// SaveMessage before the deadline.
func RuntimeTurn() time.Duration {
	turn := 2*AgentCall() + runtimeOverhead
	if precheckBlocks() {
		turn += Precheck
	}
	return turn
}

// GatewayWait is how long the gateway waits for the runtime. It always
// counts the check budget: the gateway runs in its own process and does not
// see the runtime's AGENT_RUNTIME_PRECHECK, and it must outlast the longest
// runtime turn.
func GatewayWait() time.Duration {
	wait := RuntimeTurn() + gatewayOverhead
	if !precheckBlocks() {
		wait += Precheck
	}
	return wait
}

func precheckBlocks() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv(precheckEnv))) == "block"
}
