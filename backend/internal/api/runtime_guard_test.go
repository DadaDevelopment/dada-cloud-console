package api

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

// TestValuesFileAllowedForRuntime is the regression test for the one real
// fall-through the 'box' runtime audit found.
//
// Before 'box' existed, this predicate ended in "anything that is not vm edits
// values.yaml". Adding a third runtime therefore handed a box environment a signed
// values-token for a Helm chart that does not exist — a credential to write a git
// file nothing reads. That is why the audit had to be a task with a checklist
// rather than a side effect of the migration: the bug is not a compile error and
// not a failing query, it is a default arm quietly answering for a case it was
// never told about.
func TestValuesFileAllowedForRuntime(t *testing.T) {
	cases := []struct {
		rt   models.EnvironmentRuntime
		file string
		want bool
	}{
		{models.EnvironmentRuntimeK8s, "values.yaml", true},
		{models.EnvironmentRuntimeK8s, "compose.yaml", false},
		{models.EnvironmentRuntimeK8s, ".env", false},

		{models.EnvironmentRuntimeVM, "compose.yaml", true},
		{models.EnvironmentRuntimeVM, ".env", true},
		{models.EnvironmentRuntimeVM, "values.yaml", false},

		// A box owns no editable config file at all.
		{models.EnvironmentRuntimeBox, "values.yaml", false},
		{models.EnvironmentRuntimeBox, "compose.yaml", false},
		{models.EnvironmentRuntimeBox, ".env", false},
	}
	for _, tc := range cases {
		if got := valuesFileAllowedForRuntime(tc.rt, tc.file); got != tc.want {
			t.Errorf("valuesFileAllowedForRuntime(%q, %q) = %v, want %v", tc.rt, tc.file, got, tc.want)
		}
	}
}

// TestValuesFileRuntimeMsgNamesTheRuntime: the 400 has to say what the caller can
// actually do, and for a box that is "nothing, here" rather than a message about
// Kubernetes files it will never have.
func TestValuesFileRuntimeMsgNamesTheRuntime(t *testing.T) {
	msg := valuesFileRuntimeMsg(models.EnvironmentRuntimeBox)
	if !strings.Contains(msg, "box") {
		t.Errorf("box message = %q; it must name the box, not describe Kubernetes files", msg)
	}
	if strings.Contains(msg, "values.yaml") {
		t.Errorf("box message = %q; pointing a box at values.yaml is the bug this guard fixes", msg)
	}
}
