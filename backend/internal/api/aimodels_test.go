package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/profiles"
)

// canWrite gates every mutating endpoint. Adding a role and forgetting to
// teach this helper would silently grant or revoke write access. Pin the
// matrix so the regression is impossible.
func TestCanWrite(t *testing.T) {
	cases := []struct {
		role models.MemberRole
		want bool
	}{
		{models.MemberRoleOwner, true},
		{models.MemberRoleDeveloper, true},
		{models.MemberRoleAdmin, true},
		{models.MemberRoleReadOnly, false},
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

// decideQuota encodes D12: GPU + non-admin + over quota → approval gate.
// Admins get hard 409 because there's no one above them to approve. CPU is
// hard 409 for everyone — there's no GPU-style approval branch for CPU.
// Pin every cell of the matrix; the cost of a regression here is silent
// resource overrun or a non-admin getting blocked by a path that should
// have routed to approval.
func TestDecideQuota(t *testing.T) {
	cpu := profiles.Profile{Name: "cpu-small", CPU: "1", Memory: "2Gi"}
	gpu := profiles.Profile{Name: "gpu-t4", CPU: "4", Memory: "16Gi", GPU: "1"}

	cases := []struct {
		name               string
		prof               profiles.Profile
		role               models.MemberRole
		cpuMax, gpuMax     int
		cpuInUse, gpuInUse int
		want               quotaDecision
	}{
		{"CPU under quota → allow", cpu, models.MemberRoleDeveloper, 5, 0, 2, 0, quotaAllow},
		{"CPU at quota → reject", cpu, models.MemberRoleDeveloper, 5, 0, 5, 0, quotaReject},
		{"CPU over quota → reject (admin)", cpu, models.MemberRoleOwner, 5, 0, 7, 0, quotaReject},
		{"CPU over quota → reject (client-admin)", cpu, models.MemberRoleAdmin, 5, 0, 5, 0, quotaReject},
		{"GPU under quota → allow (admin)", gpu, models.MemberRoleOwner, 5, 2, 0, 1, quotaAllow},
		{"GPU under quota → allow (developer)", gpu, models.MemberRoleDeveloper, 5, 2, 0, 0, quotaAllow},
		{"GPU at quota gpuMax=0 → approval (developer)", gpu, models.MemberRoleDeveloper, 5, 0, 0, 0, quotaApproval},
		{"GPU at quota gpuMax=0 → approval (client-admin)", gpu, models.MemberRoleAdmin, 5, 0, 0, 0, quotaApproval},
		{"GPU over quota → approval (developer)", gpu, models.MemberRoleDeveloper, 5, 2, 0, 2, quotaApproval},
		{"GPU at quota gpuMax=0 → reject (admin)", gpu, models.MemberRoleOwner, 5, 0, 0, 0, quotaReject},
		{"GPU over quota → reject (admin)", gpu, models.MemberRoleOwner, 5, 2, 0, 2, quotaReject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideQuota(c.prof, c.role, c.cpuMax, c.gpuMax, c.cpuInUse, c.gpuInUse)
			if got != c.want {
				t.Errorf("decideQuota(%s, role=%s, cpu=%d/%d, gpu=%d/%d) = %d, want %d",
					c.prof.Name, c.role, c.cpuInUse, c.cpuMax, c.gpuInUse, c.gpuMax, got, c.want)
			}
		})
	}
}
