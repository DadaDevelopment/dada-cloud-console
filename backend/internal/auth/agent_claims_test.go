package auth

import "testing"

// TestAgentHasNoImplicitPersonalOrg pins the containment guarantee behind the
// hidden /agents group: an automation identity holds ONLY the project grants it
// was handed. Without the carve-out in decode, every caller — including a service
// account scoped to one sandbox project — is implicit Owner of the org named
// after its username, which is a standing licence to mint projects there.
func TestAgentHasNoImplicitPersonalOrg(t *testing.T) {
	agent := &Claims{
		Username: "service-account-dada-routine-svc",
		Groups: []string{
			"/agents",
			"/orgs/dada/projects/agent-sandbox/Owner",
		},
	}

	if !agent.IsAgent() {
		t.Fatal("IsAgent() = false, want true for a member of /agents")
	}
	if got := agent.OrgRole("service-account-dada-routine-svc"); got != "" {
		t.Errorf("OrgRole(own username) = %q, want empty: an agent must not own a personal org", got)
	}
	if got := agent.OrgRole("dada"); got != "" {
		t.Errorf("OrgRole(dada) = %q, want empty: the sandbox grant is project-scoped, not org-scoped", got)
	}
	if got := agent.ProjectRole("agent-sandbox"); got != "Owner" {
		t.Errorf("ProjectRole(agent-sandbox) = %q, want Owner", got)
	}
}

// TestNonAgentKeepsImplicitPersonalOrg guards the carve-out's blast radius: real
// users still get the implicit personal org that self-service signup depends on.
func TestNonAgentKeepsImplicitPersonalOrg(t *testing.T) {
	user := &Claims{Username: "alexkekiy"}

	if user.IsAgent() {
		t.Fatal("IsAgent() = true for a caller with no groups, want false")
	}
	if got := user.OrgRole("alexkekiy"); got != "Owner" {
		t.Errorf("OrgRole(own username) = %q, want Owner", got)
	}
}

// TestPlatformAnalystGroup pins the read-only staff group. It must NOT imply
// platform-admin: isGod stays the write gate everywhere.
func TestPlatformAnalystGroup(t *testing.T) {
	analyst := &Claims{Username: "svc-analytics", Groups: []string{"/platform-analysts"}}

	if !analyst.IsPlatformAnalyst() {
		t.Error("IsPlatformAnalyst() = false, want true")
	}
	if analyst.IsPlatformAdmin() {
		t.Error("IsPlatformAdmin() = true for /platform-analysts, want false: read-only staff must never pass a write gate")
	}
	if analyst.IsAgent() {
		t.Error("IsAgent() = true for /platform-analysts, want false")
	}
}

// TestStaffGroupsAreNotOrgPaths keeps the hidden groups out of the org/project
// maps: they are consumed by the switch in decode and must never be parsed as
// "/orgs/..." paths.
func TestStaffGroupsAreNotOrgPaths(t *testing.T) {
	c := &Claims{Groups: []string{"/platform-admins", "/platform-analysts", "/agents"}}

	if len(c.OrgRoles()) != 0 {
		t.Errorf("OrgRoles() = %v, want empty", c.OrgRoles())
	}
	if len(c.ProjectRoles()) != 0 {
		t.Errorf("ProjectRoles() = %v, want empty", c.ProjectRoles())
	}
}
