package auth

import "testing"

// Every authenticated user is implicitly Owner of a personal org equal to their
// username (ADR-009 follow-up: default per-user org, no Keycloak group).
func TestDecode_PersonalOrgOwner(t *testing.T) {
	c := &Claims{Username: "alice"}
	if got := c.OrgRole("alice"); got != "Owner" {
		t.Fatalf("personal org role = %q, want Owner", got)
	}
	if got := c.OrgRole("bob"); got != "" {
		t.Fatalf("foreign org role = %q, want empty", got)
	}
}

// An explicit Keycloak grant on the same-named org must not be downgraded by the
// synthesized personal-org Owner (max-merge). Owner is already the top role, so
// this mainly guards the merge direction for any future role.
func TestDecode_PersonalOrgDoesNotClobberExplicit(t *testing.T) {
	c := &Claims{
		Username: "alice",
		Groups:   []string{"/orgs/alice/Owner", "/orgs/dada/Admin"},
	}
	if got := c.OrgRole("alice"); got != "Owner" {
		t.Fatalf("alice personal org = %q, want Owner", got)
	}
	if got := c.OrgRole("dada"); got != "Admin" {
		t.Fatalf("dada org = %q, want Admin", got)
	}
}

// Empty username (e.g. service/scoped-key claims) must not synthesize an org.
func TestDecode_NoUsernameNoPersonalOrg(t *testing.T) {
	c := &Claims{}
	if len(c.OrgRoles()) != 0 {
		t.Fatalf("orgRoles = %v, want empty", c.OrgRoles())
	}
}
