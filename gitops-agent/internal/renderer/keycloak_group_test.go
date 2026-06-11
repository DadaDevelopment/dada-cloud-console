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
	// Parent group present.
	if !strings.Contains(got, "name: project-acme\n") {
		t.Error("missing parent group CR name")
	}
	if !strings.Contains(got, "name: acme\n") {
		t.Error("missing KC group name 'acme'")
	}
	// All 4 role subgroups present.
	for _, role := range []string{"developer", "client-admin", "client-viewer", "platform-admin"} {
		if !strings.Contains(got, "name: project-acme-"+role+"\n") {
			t.Errorf("missing subgroup CR for role %q", role)
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
			"alice": "developer",
			"bob":   "developer",
			"carol": "client-admin",
		},
	}
	got, err := RenderProjectGroups(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kind: Memberships") {
		t.Error("expected Memberships CRs")
	}
	if !strings.Contains(got, "- alice") && !strings.Contains(got, "- bob") {
		t.Error("developer members missing")
	}
	if !strings.Contains(got, "- carol") {
		t.Error("client-admin member missing")
	}
	// client-viewer has no members — no Memberships CR for it.
	if strings.Contains(got, "project-beta-client-viewer-members") {
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
	want := "clusters/beget-prod/projects/platform/environments/prod/apps/keycloak-config/chart/templates/project-groups-acme.yaml"
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}
