package box

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestReadinessRejectsAcceptButUnusable covers the failure that a cheaper stop
// point would miss. Each case here is a box the platform could have reported as
// "ready" if readiness were defined as "the API answered" or "TCP accepted".
func TestReadinessRejectsAcceptButUnusable(t *testing.T) {
	cases := []struct {
		name string
		res  CanaryResult
		want string
	}{
		{
			name: "canary exited non-zero",
			res:  CanaryResult{ExitCode: 127, Stdout: "sh: node: not found"},
			want: "exited 127",
		},
		{
			name: "banner only, canary never ran",
			res:  CanaryResult{ExitCode: 0, Stdout: "Welcome to Ubuntu 24.04 LTS\n"},
			want: "missing",
		},
		{
			name: "exit zero but empty output",
			res:  CanaryResult{ExitCode: 0, Stdout: ""},
			want: "missing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EvaluateReadiness(tc.res)
			if err == nil {
				t.Fatal("expected not-ready")
			}
			if !errors.Is(err, ErrNotReady) {
				t.Errorf("error should wrap ErrNotReady, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q so the operator knows which check failed", err, tc.want)
			}
		})
	}
}

// TestReadinessRequiresTheWarmToolchain is the test that keeps the warm image
// honest. A box that boots in two seconds and then makes the agent run apt install
// has not delivered the product, so the toolchain check is part of readiness rather
// than a separate nice-to-have. Dropping it under schedule pressure would make the
// headline latency number measure something the customer does not get.
func TestReadinessRequiresTheWarmToolchain(t *testing.T) {
	if err := EvaluateReadiness(CanaryResult{ExitCode: 0, Stdout: WarmCanaryStdout}); err != nil {
		t.Fatalf("a fully warm box must be ready: %v", err)
	}

	for _, tool := range requiredToolchain {
		t.Run("missing_"+tool, func(t *testing.T) {
			err := EvaluateReadiness(CanaryMissing(tool))
			if err == nil {
				t.Fatalf("a box without %s must not be ready", tool)
			}
			if !strings.Contains(err.Error(), tool) {
				t.Errorf("error %q should name the missing tool %q", err, tool)
			}
		})
	}
}

// TestReadinessRejectsAShellErrorAsAVersion closes the hole the non-empty check
// left open.
//
// The canary folds stderr into each value, so a box with no toolchain at all
// reports `node=sh: 1: node: not found` — non-empty, and therefore ready under a
// check that only asked for non-empty. That is the exact failure the toolchain
// check exists to prevent, dressed as a pass, and it was live until this test.
func TestReadinessRejectsAShellErrorAsAVersion(t *testing.T) {
	for _, complaint := range []string{
		"sh: 1: node: not found",
		"bash: node: command not found",
		"/bin/sh: node: No such file or directory",
	} {
		stdout := strings.Replace(WarmCanaryStdout, "node=v22.11.0", "node="+complaint, 1)
		err := EvaluateReadiness(CanaryResult{ExitCode: 0, Stdout: stdout})
		if err == nil {
			t.Fatalf("a box reporting %q for node must not be ready", complaint)
		}
		if !strings.Contains(err.Error(), "node") {
			t.Errorf("error %q should name node", err)
		}
	}
}

// TestCanaryCommandProbesEveryRequiredTool ties the command and the check
// together. If someone adds a tool to requiredToolchain but not to the canary,
// every box would report as not-ready; if they add it to the canary but not the
// list, it would never be checked. Both are silent, so pin the pair.
func TestCanaryCommandProbesEveryRequiredTool(t *testing.T) {
	for _, tool := range requiredToolchain {
		if !strings.Contains(CanaryCommand, tool+"=") {
			t.Errorf("CanaryCommand does not emit %q; requiredToolchain and the canary must move together", tool)
		}
	}
	if !strings.Contains(CanaryCommand, readyMarker) {
		t.Errorf("CanaryCommand must emit the %q marker", readyMarker)
	}
}

// TestSpawnRunsTheCanaryInsideTheBox guards against the stop point quietly
// loosening to something cheaper: the spawn must actually execute the canary, not
// merely observe that a channel opened.
func TestSpawnRunsTheCanaryInsideTheBox(t *testing.T) {
	deps, spec, _, rt, _ := NewWarmFixture(10 * time.Millisecond)

	if _, err := Spawn(context.Background(), deps, spec); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	cmds := rt.ExecutedCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected exactly one exec on the critical path, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != CanaryCommand {
		t.Errorf("spawn executed %q, want the canary", cmds[0])
	}
}
