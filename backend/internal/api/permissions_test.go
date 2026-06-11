package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

func TestRoleFromGroups(t *testing.T) {
	cases := []struct {
		name   string
		groups []string
		slug   string
		want   models.MemberRole
	}{
		{
			name:   "platform-admin group grants global admin",
			groups: []string{"/platform-admins", "/dada-tuda-users"},
			slug:   "any-project",
			want:   models.MemberRolePlatformAdmin,
		},
		{
			name:   "developer role for matching project",
			groups: []string{"/projects/acme/developer", "/dada-tuda-users"},
			slug:   "acme",
			want:   models.MemberRoleDeveloper,
		},
		{
			name:   "client-admin role",
			groups: []string{"/projects/acme/client-admin"},
			slug:   "acme",
			want:   models.MemberRoleClientAdmin,
		},
		{
			name:   "client-viewer role",
			groups: []string{"/projects/acme/client-viewer"},
			slug:   "acme",
			want:   models.MemberRoleClientViewer,
		},
		{
			name:   "member of different project returns empty",
			groups: []string{"/projects/other/developer"},
			slug:   "acme",
			want:   "",
		},
		{
			name:   "unknown role suffix is rejected",
			groups: []string{"/projects/acme/superuser"},
			slug:   "acme",
			want:   "",
		},
		{
			name:   "partial prefix does not match",
			groups: []string{"/projects/acme-extra/developer"},
			slug:   "acme",
			want:   "",
		},
		{
			name:   "no groups returns empty",
			groups: nil,
			slug:   "acme",
			want:   "",
		},
		{
			name:   "platform-admins takes priority over project role",
			groups: []string{"/platform-admins", "/projects/acme/client-viewer"},
			slug:   "acme",
			want:   models.MemberRolePlatformAdmin,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := roleFromGroups(tc.groups, tc.slug)
			if got != tc.want {
				t.Errorf("roleFromGroups(%v, %q) = %q, want %q", tc.groups, tc.slug, got, tc.want)
			}
		})
	}
}
