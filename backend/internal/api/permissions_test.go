package api

import (
	"sort"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// resolveRole is the pure authz core (ADR-009 §4): it decodes native Keycloak
// group paths + the org-role cascade with no DB. effectiveRole wraps it with the
// project→org lookup, so these cases pin the role math without a pool.
func TestResolveRole(t *testing.T) {
	// Helper to build claims from native group paths + scope string.
	mk := func(groups ...string) *auth.Claims {
		return &auth.Claims{Groups: groups}
	}

	cases := []struct {
		name       string
		claims     *auth.Claims
		projectOrg string
		projectID  string
		want       models.MemberRole
		wantOK     bool
	}{
		{
			name:      "platform-admins is Owner everywhere",
			claims:    mk("/platform-admins"),
			projectID: "p1", projectOrg: "acme",
			want: models.MemberRoleOwner, wantOK: true,
		},
		{
			name:      "local dev-god token (carries /platform-admins) is Owner everywhere",
			claims:    mk("/orgs/local-dev/Owner", "/platform-admins"),
			projectID: "p1", projectOrg: "anything",
			want: models.MemberRoleOwner, wantOK: true,
		},
		{
			name:       "org Admin cascades into a project of that org",
			claims:     mk("/orgs/acme/Admin"),
			projectOrg: "acme", projectID: "p1",
			want: models.MemberRoleAdmin, wantOK: true,
		},
		{
			name:       "org Developer does NOT cascade into an unlisted project",
			claims:     mk("/orgs/acme/Developer"),
			projectOrg: "acme", projectID: "p1",
			want: "", wantOK: false,
		},
		{
			name:       "explicit project role beats lower org role",
			claims:     mk("/orgs/acme/ReadOnly", "/orgs/acme/projects/p1/Admin"),
			projectOrg: "acme", projectID: "p1",
			want: models.MemberRoleAdmin, wantOK: true,
		},
		{
			name:       "org role boosts lower explicit project role",
			claims:     mk("/orgs/acme/Admin", "/orgs/acme/projects/p1/ReadOnly"),
			projectOrg: "acme", projectID: "p1",
			want: models.MemberRoleAdmin, wantOK: true,
		},
		{
			name:       "project-only role with no org membership",
			claims:     mk("/orgs/acme/projects/p1/Developer"),
			projectOrg: "acme", projectID: "p1",
			want: models.MemberRoleDeveloper, wantOK: true,
		},
		{
			name: "multi-org: role resolved against the project's own org (beta)",
			claims: mk(
				"/orgs/acme/Admin",
				"/orgs/beta/projects/p2/Developer",
			),
			projectOrg: "beta", projectID: "p2",
			want: models.MemberRoleDeveloper, wantOK: true,
		},
		{
			name: "multi-org: acme admin does not leak into beta project p3",
			claims: mk(
				"/orgs/acme/Admin",
				"/orgs/beta/ReadOnly",
			),
			projectOrg: "beta", projectID: "p3",
			want: "", wantOK: false,
		},
		{
			name:       "nil claims denied",
			claims:     nil,
			projectOrg: "acme", projectID: "p1",
			want: "", wantOK: false,
		},
		{
			name:       "no membership at all denied",
			claims:     mk("/orgs/other/Owner"),
			projectOrg: "acme", projectID: "p1",
			want: "", wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveRole(tc.claims, tc.projectOrg, tc.projectID)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("resolveRole = (%q, ok=%v), want (%q, ok=%v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestDecodeGroups pins the native group-path decode contract (ADR-009 §2) the
// user-service/Keycloak side must produce.
func TestDecodeGroups(t *testing.T) {
	c := &auth.Claims{
		Groups: []string{
			"/orgs/acme/Admin",
			"/orgs/acme/projects/p1/Developer",
			"/orgs/beta/Owner",
			"/platform-admins",
			"/orgs/acme/Developer", // duplicate org scope: must keep the higher (Admin)
		},
	}
	if got := c.OrgRole("acme"); got != "Admin" {
		t.Errorf("OrgRole(acme) = %q, want Admin (max of Admin/Developer)", got)
	}
	if got := c.OrgRole("beta"); got != "Owner" {
		t.Errorf("OrgRole(beta) = %q, want Owner", got)
	}
	if got := c.OrgRole("ghost"); got != "" {
		t.Errorf("OrgRole(ghost) = %q, want empty", got)
	}
	if got := c.ProjectRole("p1"); got != "Developer" {
		t.Errorf("ProjectRole(p1) = %q, want Developer", got)
	}
	if !c.IsPlatformAdmin() {
		t.Error("IsPlatformAdmin() = false, want true")
	}
}

// TestParseScope checks scopes come from the native space-delimited `scope`
// claim (standard OIDC), not a custom array.
func TestParseScope(t *testing.T) {
	c := &auth.Claims{Scope: "read metrics:write logs:write deploy:write builds:read builds:write admin"}
	for _, s := range auth.AllScopes {
		if !c.HasScope(s) {
			t.Errorf("HasScope(%q) = false, want true", s)
		}
	}
	if c.HasScope("nonexistent") {
		t.Error("HasScope(nonexistent) = true, want false")
	}
	// Native OIDC scope strings often carry non-app scopes; they must be harmless.
	c2 := &auth.Claims{Scope: "openid profile email read"}
	if !c2.HasScope("read") || c2.HasScope("admin") {
		t.Error("scope set mishandled extra OIDC scopes")
	}
}

// TestAdminOrgIDs / TestClaimProjectIDs cover the multi-org enumeration helpers
// ListProjects / ListAdminApprovals use to scope queries across many tenants.
func TestAdminOrgIDs(t *testing.T) {
	c := &auth.Claims{Groups: []string{
		"/orgs/acme/Owner",
		"/orgs/beta/Admin",
		"/orgs/gamma/Developer", // not admin → excluded
		"/orgs/delta/ReadOnly",  // not admin → excluded
	}}
	got := adminOrgIDs(c)
	sort.Strings(got)
	want := []string{"acme", "beta"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("adminOrgIDs = %v, want %v", got, want)
	}
	if adminOrgIDs(nil) != nil {
		t.Error("adminOrgIDs(nil) should be nil")
	}
}

func TestClaimProjectIDs(t *testing.T) {
	p1 := uuid.New()
	c := &auth.Claims{Groups: []string{
		"/orgs/acme/projects/" + p1.String() + "/Developer",
		"/orgs/acme/projects/not-a-uuid/Admin", // unparseable → skipped
	}}
	got := claimProjectIDs(c)
	if len(got) != 1 || got[0] != p1 {
		t.Errorf("claimProjectIDs = %v, want [%v]", got, p1)
	}
}

func TestMaxRole(t *testing.T) {
	cases := []struct {
		a, b, want models.MemberRole
	}{
		{models.MemberRoleOwner, models.MemberRoleReadOnly, models.MemberRoleOwner},
		{models.MemberRoleReadOnly, models.MemberRoleAdmin, models.MemberRoleAdmin},
		{models.MemberRoleDeveloper, models.MemberRoleDeveloper, models.MemberRoleDeveloper},
		{"", models.MemberRoleReadOnly, models.MemberRoleReadOnly},
	}
	for _, c := range cases {
		if got := models.MaxRole(c.a, c.b); got != c.want {
			t.Errorf("MaxRole(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestIsOrgAdmin(t *testing.T) {
	for r, want := range map[models.MemberRole]bool{
		models.MemberRoleOwner:     true,
		models.MemberRoleAdmin:     true,
		models.MemberRoleDeveloper: false,
		models.MemberRoleReadOnly:  false,
		"":                         false,
	} {
		if got := isOrgAdmin(r); got != want {
			t.Errorf("isOrgAdmin(%q) = %v, want %v", r, got, want)
		}
	}
}
