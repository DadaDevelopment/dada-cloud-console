package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

// TestStuckOperationsCoversEveryDeclaredStatus is the drift gate behind
// terminalOperationStatuses. The admin panel spent months reporting 466
// finished deploys as stuck operations because it hand-wrote its own terminal
// set and left "Committed" out of it, while classifyOperationStatus and the
// audit writer had it right -- three definitions of terminal in one codebase.
// terminalOperationStatuses now derives from classifyOperationStatus, but it
// still has to enumerate the model's constants by hand (Go cannot reflect over
// a const block), so a newly added status would silently fall through as
// "non-terminal" and become the next false alarm. This test reads the const
// block itself and fails until the new status is classified on purpose.
func TestStuckOperationsCoversEveryDeclaredStatus(t *testing.T) {
	src, err := os.ReadFile("../models/operation.go")
	if err != nil {
		t.Fatalf("read operation model: %v", err)
	}

	declared := regexp.MustCompile(`OperationStatus\w*\s+OperationStatus\s+=\s+"([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(declared) == 0 {
		t.Fatal("found no OperationStatus constants; the regex no longer matches the model and this gate is dead")
	}

	classified := map[string]bool{}
	for _, s := range allOperationStatusesForStuckCheck {
		classified[string(s)] = true
	}

	for _, m := range declared {
		if !classified[m[1]] {
			t.Fatalf("operation status %q is declared in models but not classified in allOperationStatusesForStuckCheck; "+
				"until it is, the admin overview will report it as a stuck operation forever", m[1])
		}
	}
}

// TestTerminalOperationStatusesMatchesDeployHooks pins the panel's exclusion
// set to the deploy-hook poller's verdict, so the two can never disagree about
// whether an operation is finished.
func TestTerminalOperationStatusesMatchesDeployHooks(t *testing.T) {
	for _, s := range allOperationStatusesForStuckCheck {
		terminal, _ := classifyOperationStatus(s)
		listed := strings.Contains(strings.Join(terminalOperationStatuses, ","), string(s))
		if terminal != listed {
			t.Fatalf("status %q: classifyOperationStatus terminal=%v but terminalOperationStatuses listed=%v", s, terminal, listed)
		}
	}
	if !strings.Contains(strings.Join(terminalOperationStatuses, ","), string(models.OperationStatusCommitted)) {
		t.Fatal("Committed must be terminal: gitops-agent ends the operation there and nothing advances the row afterwards")
	}
}
