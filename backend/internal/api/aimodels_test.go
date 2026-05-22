package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

// canWrite gates every mutating endpoint. Adding a role and forgetting to
// teach this helper would silently grant or revoke write access. Pin the
// matrix so the regression is impossible.
func TestCanWrite(t *testing.T) {
	cases := []struct {
		role models.MemberRole
		want bool
	}{
		{models.MemberRolePlatformAdmin, true},
		{models.MemberRoleDeveloper, true},
		{models.MemberRoleClientAdmin, true},
		{models.MemberRoleClientViewer, false},
		{"", false},
		{"unknown-role", false},
	}
	for _, c := range cases {
		if got := canWrite(c.role); got != c.want {
			t.Errorf("canWrite(%q) = %v, want %v", c.role, got, c.want)
		}
	}
}

// validModelTypes is the AIModel XRD's accepted set. CreateAIModel rejects
// anything outside it; KServe protocol routing in inference.go assumes the
// same set. If they drift the playground breaks for valid models.
func TestValidModelTypes(t *testing.T) {
	expected := []string{
		"sklearn", "xgboost", "lightgbm",
		"pytorch", "tensorflow", "triton",
		"huggingface", "custom",
	}
	for _, mt := range expected {
		if !validModelTypes[mt] {
			t.Errorf("validModelTypes missing %q", mt)
		}
	}
	for _, bad := range []string{"", "Sklearn", "PYTORCH", "java", "onnx"} {
		if validModelTypes[bad] {
			t.Errorf("validModelTypes accepts %q, should not", bad)
		}
	}
	if len(validModelTypes) != len(expected) {
		t.Errorf("validModelTypes has %d entries, want %d — map drifted from XRD",
			len(validModelTypes), len(expected))
	}
}
