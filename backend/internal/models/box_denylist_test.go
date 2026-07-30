package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBoxActionsAreInGitopsAgentDenylist is the tripwire on D4, the landmine of
// this whole slice.
//
// gitops-agent/internal/db/operations.go claims operations with
// `action NOT IN (...)` — a DENYLIST. So an action it does not know about is
// claimed by it anyway and failed immediately with "unknown action". Every box
// action therefore has to appear in that exclusion list, and the two lists live in
// two different Go modules that cannot import each other: nothing but this test
// stands between adding an eleventh box action and shipping a feature that is dead
// on arrival with a confusing error and no retry.
//
// A static source scan rather than a DB test on purpose: the same reasoning as
// TestAlertedMetricsAreDeclared in internal/metrics. The failure being guarded
// against is a missing string literal, and a missing literal has no runtime
// representation to inspect.
func TestBoxActionsAreInGitopsAgentDenylist(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	path := filepath.Join(repoRoot, "gitops-agent/internal/db/operations.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(src)

	// Anchor on the claim query so a box action merely mentioned in a comment
	// elsewhere in the file cannot satisfy the check.
	start := strings.Index(body, "o.action NOT IN (")
	if start < 0 {
		t.Fatal("gitops-agent's claim query no longer contains `o.action NOT IN (` — " +
			"if the claim strategy changed to an allowlist this test should be rewritten, " +
			"not deleted: something must still keep the two agents' claim sets disjoint")
	}
	end := strings.Index(body[start:], ")")
	if end < 0 {
		t.Fatal("could not find the end of the NOT IN list")
	}
	denylist := body[start : start+end]

	for _, action := range BoxActions {
		if !strings.Contains(denylist, "'"+action+"'") {
			t.Errorf("action %q is missing from gitops-agent's claim denylist. Without it, "+
				"gitops-agent claims the operation and fails it with \"unknown action: %s\" "+
				"before box-agent ever sees it. Add it to the NOT IN list in "+
				"gitops-agent/internal/db/operations.go, in the same commit.", action, action)
		}
	}
}

// TestBoxActionsAreNotInPortainerAgentAllowlist verifies the other half of the
// claim split: portainer-agent uses an allowlist, so it needs no edit for box
// actions — and must not accidentally acquire one. If a box action ever appears in
// its IN list, both agents would claim it and race.
func TestBoxActionsAreNotInPortainerAgentAllowlist(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	path := filepath.Join(repoRoot, "portainer-agent/internal/db/operations.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(src)

	start := strings.Index(body, "o.action IN (")
	if start < 0 {
		t.Fatal("portainer-agent's claim query no longer contains `o.action IN (` — " +
			"it may have switched to a denylist, which would make it a second landmine")
	}
	end := strings.Index(body[start:], ")")
	if end < 0 {
		t.Fatal("could not find the end of the IN list")
	}
	allowlist := body[start : start+end]

	for _, action := range BoxActions {
		if strings.Contains(allowlist, "'"+action+"'") {
			t.Errorf("action %q appears in portainer-agent's claim allowlist; box actions belong "+
				"to box-agent, and two agents claiming the same action race for it", action)
		}
	}
}
