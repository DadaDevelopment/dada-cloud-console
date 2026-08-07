package worker

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

func TestManifestsFileIsEmpty_LastManifestRemoved(t *testing.T) {
	empty, err := manifestsFileIsEmpty(git.FileChange{Path: "x", Content: "manifests: []\n"})
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if !empty {
		t.Fatal("a values file with no manifests must report empty; otherwise the carrier app stays in git and ArgoCD wedges on \"auto-sync will wipe out all resources\"")
	}
}

func TestManifestsFileIsEmpty_SiblingSurvives(t *testing.T) {
	content := "manifests:\n" +
		"  - apiVersion: platform.dada-tuda.ru/v1alpha1\n" +
		"    kind: ServiceDatabaseV2\n" +
		"    metadata:\n" +
		"      name: keeper\n"
	empty, err := manifestsFileIsEmpty(git.FileChange{Path: "x", Content: content})
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if empty {
		t.Fatal("a values file that still carries a sibling CR must not be reported empty: removing the carrier app would delete the surviving database")
	}
}

func TestStandaloneOwnerAppPaths_CoversTheWholeCarrier(t *testing.T) {
	paths := standaloneOwnerAppPaths("acme", "prod", "service-databases-acme")
	want := []string{"app.yaml", "resources.values.yaml", "values.yaml"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %d entries", paths, len(want))
	}
	for i, suffix := range want {
		if !strings.HasSuffix(paths[i], suffix) {
			t.Fatalf("paths[%d] = %q, want suffix %q", i, paths[i], suffix)
		}
		if !strings.Contains(paths[i], "projects/acme/environments/prod/apps/service-databases-acme/") {
			t.Fatalf("paths[%d] = %q, want it scoped to the carrier app of acme/prod", i, paths[i])
		}
	}
}
