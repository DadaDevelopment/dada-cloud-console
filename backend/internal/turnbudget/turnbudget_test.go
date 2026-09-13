package turnbudget

import (
	"testing"
	"time"
)

func TestBudgetsNestSoNoLayerGivesUpBeforeTheOneBelow(t *testing.T) {
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "")
	if AgentCall() != 150*time.Second {
		t.Fatalf("default agent call = %s", AgentCall())
	}
	if RuntimeTurn() < 2*AgentCall() {
		t.Fatalf("runtime turn %s cannot hold an agent call plus an ask_user resume (%s each)", RuntimeTurn(), AgentCall())
	}
	if GatewayWait() <= RuntimeTurn() {
		t.Fatalf("gateway wait %s must outlast the runtime turn %s", GatewayWait(), RuntimeTurn())
	}
}

func TestAgentCallEnvOverride(t *testing.T) {
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "200")
	if AgentCall() != 200*time.Second || RuntimeTurn() != 430*time.Second || GatewayWait() != 445*time.Second {
		t.Fatalf("override not propagated: %s %s %s", AgentCall(), RuntimeTurn(), GatewayWait())
	}
	for _, bad := range []string{"0", "-5", "abc"} {
		t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", bad)
		if AgentCall() != DefaultAgentCall {
			t.Fatalf("%q should fall back to default, got %s", bad, AgentCall())
		}
	}
}
