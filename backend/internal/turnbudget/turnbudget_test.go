package turnbudget

import (
	"testing"
	"time"
)

func TestBudgetsNestSoNoLayerGivesUpBeforeTheOneBelow(t *testing.T) {
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "")
	for _, mode := range []string{"", "off", "log", "block"} {
		t.Setenv("AGENT_RUNTIME_PRECHECK", mode)
		if AgentCall() != 150*time.Second {
			t.Fatalf("default agent call = %s", AgentCall())
		}
		if RuntimeTurn() < 2*AgentCall() {
			t.Fatalf("runtime turn %s cannot hold an agent call plus an ask_user resume (%s each)", RuntimeTurn(), AgentCall())
		}
		if GatewayWait() <= RuntimeTurn() {
			t.Fatalf("precheck=%q: gateway wait %s must outlast the runtime turn %s", mode, GatewayWait(), RuntimeTurn())
		}
	}
}

// Under block the Checks of a turn may take the whole precheck budget on
// top of two agent calls: the runtime turn grows by it, and the gateway,
// which cannot see the runtime's flag, always waits for the longer turn.
func TestPrecheckBlockAddsTheCheckBudget(t *testing.T) {
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "")
	t.Setenv("AGENT_RUNTIME_PRECHECK", "")
	off, gatewayOff := RuntimeTurn(), GatewayWait()
	if off != 330*time.Second {
		t.Fatalf("flag off: runtime turn %s, want the old 330s", off)
	}
	t.Setenv("AGENT_RUNTIME_PRECHECK", " Block ")
	if RuntimeTurn() != off+Precheck {
		t.Fatalf("block: runtime turn %s, want %s", RuntimeTurn(), off+Precheck)
	}
	if RuntimeTurn() < 2*AgentCall()+Precheck+runtimeOverhead {
		t.Fatalf("block: two agent calls, the check budget and the overhead do not fit in %s", RuntimeTurn())
	}
	if GatewayWait() != gatewayOff || GatewayWait() <= RuntimeTurn() {
		t.Fatalf("gateway wait %s (off %s) must be the same under both and outlast %s", GatewayWait(), gatewayOff, RuntimeTurn())
	}
}

func TestAgentCallEnvOverride(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_PRECHECK", "")
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "200")
	if AgentCall() != 200*time.Second || RuntimeTurn() != 430*time.Second || GatewayWait() != 475*time.Second {
		t.Fatalf("override not propagated: %s %s %s", AgentCall(), RuntimeTurn(), GatewayWait())
	}
	for _, bad := range []string{"0", "-5", "abc"} {
		t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", bad)
		if AgentCall() != DefaultAgentCall {
			t.Fatalf("%q should fall back to default, got %s", bad, AgentCall())
		}
	}
}
