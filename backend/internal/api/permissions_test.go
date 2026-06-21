package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// effectiveRole resolves authz purely from fat claims (ADR-009). These cases pin
// the max(org_role, projects[id]) rule and the god overrides. The org-cascade
// branch hits the DB (projectInOrg), so it is not exercised here — only the
// claim-only paths that need no pool.
func TestEffectiveRole_ClaimOnly(t *testing.T) {
	pid := uuid.New()
	other := uuid.New()
	h := &Handler{} // pool unused on the claim-only paths below

	cases := []struct {
		name    string
		claims  *auth.Claims
		project uuid.UUID
		want    models.MemberRole
		wantOK  bool
	}{
		{
			name:    "local dev-god is Owner everywhere",
			claims:  &auth.Claims{OrgID: "local-dev", OrgRole: "Owner"},
			project: pid,
			want:    models.MemberRoleOwner,
			wantOK:  true,
		},
		{
			name:    "platform-admins group is Owner everywhere",
			claims:  &auth.Claims{Groups: []string{"/platform-admins"}, OrgRole: "ReadOnly"},
			project: pid,
			want:    models.MemberRoleOwner,
			wantOK:  true,
		},
		{
			name:    "explicit project role beats lower org role",
			claims:  &auth.Claims{OrgID: "o1", OrgRole: "ReadOnly", Projects: map[string]string{pid.String(): "Admin"}},
			project: pid,
			want:    models.MemberRoleAdmin,
			wantOK:  true,
		},
		{
			name:    "org role beats lower explicit project role",
			claims:  &auth.Claims{OrgID: "o1", OrgRole: "Developer", Projects: map[string]string{pid.String(): "ReadOnly"}},
			project: pid,
			want:    models.MemberRoleDeveloper,
			wantOK:  true,
		},
		{
			name:    "developer org role does not cascade to unlisted project",
			claims:  &auth.Claims{OrgID: "o1", OrgRole: "Developer", Projects: map[string]string{other.String(): "Developer"}},
			project: pid,
			want:    "",
			wantOK:  false,
		},
		{
			name:    "nil claims denied",
			claims:  nil,
			project: pid,
			want:    "",
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.effectiveRole(nil, tc.claims, tc.project)
			ok := err == nil
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("effectiveRole = (%q, ok=%v), want (%q, ok=%v)", got, ok, tc.want, tc.wantOK)
			}
		})
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
