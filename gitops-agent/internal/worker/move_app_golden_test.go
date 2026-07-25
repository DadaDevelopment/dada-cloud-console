package worker

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the moveapp golden files from current output")

// TestMoveGoldenRepointResourcesValues pins the byte-exact output of a stateful
// move's resources.values.yaml re-point. Unlike the targeted Contains assertions
// in move_app_db_test.go, a full-file golden guards the move's core contract —
// carry everything VERBATIM so the git diff is minimal — against silent drift: a
// reordered key, a dropped label, a stray field, or a changed default would all
// churn every moved app's diff, and only a whole-file comparison catches that.
//
// The golden captures repointResourcesValuesDB alone (the deterministic,
// data-independent transform): the ServiceDatabaseV2 re-homed to the target
// namespace with rewritten labels + operation id but verbatim name/appRef/
// database/backup, and the PublicApi left completely untouched (proving the
// re-point is DB-only). Regenerate intentionally with `go test -run
// TestMoveGoldenRepointResourcesValues -update-golden ./internal/worker/`.
func TestMoveGoldenRepointResourcesValues(t *testing.T) {
	const dir = "../../tests/golden/moveapp"
	srcPath := filepath.Join(dir, "n8n_src.resources.values.yaml")
	goldenPath := filepath.Join(dir, "n8n_moved.resources.values.yaml")

	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read move source fixture: %v", err)
	}
	rv, err := renderer.ParseResourcesValues(string(srcBytes))
	if err != nil {
		t.Fatalf("parse move source fixture: %v", err)
	}
	if err := repointResourcesValuesDB(rv, "platform", "prod", "platform-prod", "op-golden-0001"); err != nil {
		t.Fatalf("repoint DB for move: %v", err)
	}
	got, err := rv.Marshal()
	if err != nil {
		t.Fatalf("marshal moved resources.values.yaml: %v", err)
	}

	if *updateGolden {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("wrote golden %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("moved resources.values.yaml drifted from golden %s\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, string(want))
	}
}
