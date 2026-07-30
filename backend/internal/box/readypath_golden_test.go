package box

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the ready-path golden file")

const readyPathGolden = "../../tests/golden/box/ready-path.txt"

// TestReadyPathGolden pins the ordered critical path of a spawn.
//
// Time to ready is the one product claim marketing cannot compensate for, and
// seconds cannot be measured in a CI pod without a hypervisor. What can be
// measured is the *shape* of the path, and that is where regressions in this class
// actually come from: someone adds a serial step — another provisioning call, a
// synchronous DNS wait, a second database round trip before ready — and the p95
// slides in production a week later.
//
// This test makes that a review-time failure with a one-line diff. If the change
// is intended:
//
//	go test ./internal/box -run TestReadyPathGolden -update-golden
//
// and justify the new step in the PR description. Work that can happen after the
// box is handed to the customer does not belong on this list.
func TestReadyPathGolden(t *testing.T) {
	deps, spec, _, _, _ := NewWarmFixture(100 * time.Millisecond)

	res, err := Spawn(context.Background(), deps, spec)
	if err != nil {
		t.Fatalf("warm spawn should succeed: %v", err)
	}

	var b strings.Builder
	for i, step := range res.Steps {
		fmt.Fprintf(&b, "%d %s\n", i+1, step)
	}
	got := b.String()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(readyPathGolden), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(readyPathGolden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Log("golden updated")
		return
	}

	want, err := os.ReadFile(readyPathGolden)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("the critical path of a box spawn changed.\n--- want ---\n%s\n--- got ---\n%s\n"+
			"Adding a serial step here directly costs time to ready. If it is intended, rerun with "+
			"-update-golden and say why in the PR; if the work can happen after the box is handed "+
			"over, move it off the critical path.", want, got)
	}
}

// TestSpawnOrchestrationOverheadIsNegligible checks that our own bookkeeping is
// not what makes a spawn slow. Against a zero-cost runtime the whole path is
// in-memory Go, so the real figure is microseconds; the 250ms ceiling is a
// three-orders-of-magnitude margin chosen so this cannot flake on a loaded CI node
// while still catching something genuinely pathological, like an accidental sleep
// or a per-step allocation storm.
func TestSpawnOrchestrationOverheadIsNegligible(t *testing.T) {
	deps, spec, _, _, _ := NewWarmFixture(0)

	start := time.Now()
	if _, err := Spawn(context.Background(), deps, spec); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("orchestration overhead %s exceeds 250ms against a zero-cost runtime", elapsed)
	}
}
