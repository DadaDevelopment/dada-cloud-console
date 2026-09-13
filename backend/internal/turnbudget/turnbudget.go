package turnbudget

import (
	"os"
	"strconv"
	"time"
)

const (
	DefaultAgentCall = 150 * time.Second
	agentCallEnv     = "AGENT_CALL_TIMEOUT_SECONDS"
	runtimeOverhead  = 30 * time.Second
	gatewayOverhead  = 15 * time.Second
)

func AgentCall() time.Duration {
	if raw := os.Getenv(agentCallEnv); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return DefaultAgentCall
}

func RuntimeTurn() time.Duration {
	return 2*AgentCall() + runtimeOverhead
}

func GatewayWait() time.Duration {
	return RuntimeTurn() + gatewayOverhead
}
