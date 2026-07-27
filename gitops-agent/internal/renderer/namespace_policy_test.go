package renderer

import (
	"strings"
	"testing"
)

func TestRenderNamespacePolicyRegistrySecret(t *testing.T) {
	out, err := RenderNamespacePolicy(NamespacePolicySpec{
		Namespace:      "proj-pr-1-app",
		RegistrySecret: &RegistrySecretSpec{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "registrySecret:") || !strings.Contains(out, "enabled: true") {
		t.Fatalf("expected registrySecret enabled in output, got:\n%s", out)
	}
}

func TestRenderNamespacePolicyOmitsRegistrySecretByDefault(t *testing.T) {
	out, err := RenderNamespacePolicy(NamespacePolicySpec{Namespace: "proj-prod"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "registrySecret") {
		t.Fatalf("registrySecret must be omitted when unset, got:\n%s", out)
	}
}

func TestRenderNamespacePolicyOwnNamespace(t *testing.T) {
	out, err := RenderNamespacePolicy(NamespacePolicySpec{
		Namespace:    "proj-pr-1-app",
		OwnNamespace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ownNamespace: true") {
		t.Fatalf("expected ownNamespace: true in output, got:\n%s", out)
	}
}

// TestRenderNamespacePolicyOmitsOwnNamespaceByDefault pins the blast radius of
// the flag: every non-preview caller (doSetNamespacePolicy, and the hand-written
// infra files for crossplane-system/mlflow/opensearch) must keep rendering a
// policy that leaves its namespace untracked, so removing the file can never
// delete a long-lived namespace.
func TestRenderNamespacePolicyOmitsOwnNamespaceByDefault(t *testing.T) {
	out, err := RenderNamespacePolicy(NamespacePolicySpec{Namespace: "proj-prod"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ownNamespace") {
		t.Fatalf("ownNamespace must be omitted when unset, got:\n%s", out)
	}
}
