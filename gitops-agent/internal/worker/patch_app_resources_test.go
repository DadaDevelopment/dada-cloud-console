package worker

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// TestPatchAppResourcesFlag_LegacyAppYAML is the exact regression this exists
// for: an app.yaml rendered before "resources: true" was added to the
// template (no argoName, no namespace/labels, no resources flag) never gets
// re-rendered by ensureAppExists because the file already exists, so a
// resource attached later (custom domain, ServiceDatabase, ...) is written
// into resources.values.yaml but ArgoCD never gains the source that reads it.
func TestPatchAppResourcesFlag_LegacyAppYAML(t *testing.T) {
	legacy := "apiVersion: platform.dada-tuda.ru/v1alpha1\n" +
		"kind: App\n" +
		"metadata:\n" +
		"  name: telemost-bot\n" +
		"spec:\n" +
		"  helm:\n" +
		"    repoURL: https://bitbucket.dada-tuda.ru/scm/dada/dada-argo.git\n" +
		"    path: helm/python\n" +
		"    targetRevision: develop\n"

	patched, changed := patchAppResourcesFlag(legacy)
	if !changed {
		t.Fatalf("expected legacy app.yaml without resources: true to be patched")
	}
	if !strings.Contains(patched, "\nspec:\n  resources: true\n") {
		t.Errorf("patched app.yaml missing resources: true right after spec:\n%s", patched)
	}
	if !strings.Contains(patched, "path: helm/python") {
		t.Errorf("patch must not touch the existing workload chart path\n%s", patched)
	}
}

// TestPatchAppResourcesFlag_AlreadyCurrent guards against a needless commit on
// every operation touching an app that already has the field: RenderApp's
// current template always emits "resources: true" for a real workload app.
func TestPatchAppResourcesFlag_AlreadyCurrent(t *testing.T) {
	current, err := renderer.RenderApp(renderer.AppSpec{
		Name:               "agent-orchestrator-ui",
		Namespace:          "internal-prod",
		ProjectSlug:        "internal",
		EnvSlug:            "prod",
		OperationID:        "11111111-1111-1111-1111-111111111111",
		HelmRepoURL:        renderer.WorkloadRepoURL,
		HelmTargetRevision: renderer.WorkloadBranch,
		Framework:          "javascript",
	})
	if err != nil {
		t.Fatalf("RenderApp: %v", err)
	}

	patched, changed := patchAppResourcesFlag(current)
	if changed {
		t.Errorf("app.yaml already has resources: true, must not be rewritten:\n%s", patched)
	}
}

// TestPatchAppResourcesFlag_ResourcesOnlyOwnerLeftAlone guards the bare
// chart-owner shape (spec.helm.path already at helm/app-resources): it never
// carries "resources: true" by design, and must not gain it.
func TestPatchAppResourcesFlag_ResourcesOnlyOwnerLeftAlone(t *testing.T) {
	owner, err := renderer.RenderApp(renderer.AppSpec{
		Name:               "service-databases-internal",
		Namespace:          "internal-prod",
		ProjectSlug:        "internal",
		EnvSlug:            "prod",
		OperationID:        "22222222-2222-2222-2222-222222222222",
		HelmRepoURL:        renderer.WorkloadRepoURL,
		HelmTargetRevision: renderer.WorkloadBranch,
		ResourcesOnly:      true,
		ResourcesValueFile: "clusters/beget-prod/projects/internal/environments/prod/apps/service-databases-internal/resources.values.yaml",
	})
	if err != nil {
		t.Fatalf("RenderApp: %v", err)
	}

	patched, changed := patchAppResourcesFlag(owner)
	if changed {
		t.Errorf("ResourcesOnly owner app.yaml must never gain resources: true:\n%s", patched)
	}
}
