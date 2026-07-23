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
