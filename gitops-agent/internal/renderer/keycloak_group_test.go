package renderer

import (
	"strings"
	"testing"
)

func TestRenderProjectGroups_basic(t *testing.T) {
	spec := ProjectGroupSpec{ProjectSlug: "acme"}
	got, err := RenderProjectGroups(spec)
	if err != nil {
		t.Fatal(err)
	}
	// Org parent group present under orgs-container → /orgs/acme.
	if !strings.Contains(got, "name: org-acme\n") {
		t.Error("missing org parent group CR name")
	}
	if !strings.Contains(got, "name: acme\n") {
		t.Error("missing KC group name 'acme'")
	}
	if !strings.Contains(got, "name: orgs-container\n") {
		t.Error("org parent must hang off orgs-container")
	}
	// All 4 wire-role subgroups present, each role-mapped to its realm role.
	for _, role := range []string{"owner", "admin", "developer", "readonly"} {
		if !strings.Contains(got, "name: org-acme-"+role+"\n") {
			t.Errorf("missing subgroup CR for role %q", role)
		}
		if !strings.Contains(got, "name: org-acme-"+role+"-roles\n") {
			t.Errorf("missing group Roles (realm-role mapping) CR for role %q", role)
		}
	}
	// Realm role refs wired.
	for _, ref := range []string{"iam-role-owner", "iam-role-admin", "iam-role-developer", "iam-role-readonly"} {
		if !strings.Contains(got, "- name: "+ref+"\n") {
			t.Errorf("missing realm role ref %q", ref)
		}
	}
	// Title-cased KC role names emitted.
	for _, role := range []string{"Owner", "Admin", "Developer", "ReadOnly"} {
		if !strings.Contains(got, "name: "+role+"\n") {
			t.Errorf("missing KC role group name %q", role)
		}
	}
	// No membership CRs (members is nil).
	if strings.Contains(got, "Memberships") {
		t.Error("unexpected Memberships CR when no members")
	}
}

func TestRenderProjectGroups_withMembers(t *testing.T) {
	spec := ProjectGroupSpec{
		ProjectSlug: "beta",
		Members: map[string]string{
			"alice": "Developer",
			"bob":   "Developer",
			"carol": "Admin",
		},
	}
	got, err := RenderProjectGroups(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kind: Memberships") {
		t.Error("expected Memberships CRs")
	}
	if !strings.Contains(got, "org-beta-developer-members") {
		t.Error("developer Memberships CR missing")
	}
	if !strings.Contains(got, "- alice") && !strings.Contains(got, "- bob") {
		t.Error("developer members missing")
	}
	if !strings.Contains(got, "org-beta-admin-members") || !strings.Contains(got, "- carol") {
		t.Error("admin member missing")
	}
	// ReadOnly has no members — no Memberships CR for it.
	if strings.Contains(got, "org-beta-readonly-members") {
		t.Error("unexpected Memberships CR for empty role")
	}
}

func TestRenderProjectGroups_emptySlug(t *testing.T) {
	_, err := RenderProjectGroups(ProjectGroupSpec{})
	if err == nil {
		t.Error("expected error for empty slug")
	}
}

func TestProjectGroupsGitPath(t *testing.T) {
	path := ProjectGroupsGitPath("acme")
	want := "clusters/beget-prod/projects/platform/environments/prod/apps/keycloak-config/chart/templates/org-groups-acme.yaml"
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}
