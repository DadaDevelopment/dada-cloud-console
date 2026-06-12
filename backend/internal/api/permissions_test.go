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

func TestSlugRolesFromGroups(t *testing.T) {
	t.Run("platform-admins returns isPlatformAdmin=true", func(t *testing.T) {
		_, isAdmin := slugRolesFromGroups([]string{"/platform-admins", "/projects/x/developer"})
		if !isAdmin {
			t.Fatal("expected isPlatformAdmin=true")
		}
	})

	t.Run("extracts multiple slugs", func(t *testing.T) {
		m, isAdmin := slugRolesFromGroups([]string{
			"/projects/acme/developer",
			"/projects/beta/client-admin",
			"/unrelated",
		})
		if isAdmin {
			t.Fatal("unexpected isPlatformAdmin")
		}
		if m["acme"] != models.MemberRoleDeveloper {
			t.Errorf("acme role = %q, want developer", m["acme"])
		}
		if m["beta"] != models.MemberRoleClientAdmin {
			t.Errorf("beta role = %q, want client-admin", m["beta"])
		}
		if _, ok := m["unrelated"]; ok {
			t.Error("unrelated group should not appear")
		}
	})

	t.Run("highest-priority role wins for same slug", func(t *testing.T) {
		m, _ := slugRolesFromGroups([]string{
			"/projects/acme/client-viewer",
			"/projects/acme/developer",
		})
		if m["acme"] != models.MemberRoleDeveloper {
			t.Errorf("acme role = %q, want developer", m["acme"])
		}
	})

	t.Run("unknown role is skipped", func(t *testing.T) {
		m, _ := slugRolesFromGroups([]string{"/projects/acme/superuser"})
		if _, ok := m["acme"]; ok {
			t.Error("superuser should be rejected")
		}
	})

	t.Run("empty groups returns empty map", func(t *testing.T) {
		m, isAdmin := slugRolesFromGroups(nil)
		if isAdmin {
			t.Fatal("unexpected isPlatformAdmin")
		}
		if len(m) != 0 {
			t.Errorf("expected empty map, got %v", m)
		}
	})
}
