package worker

import "testing"

// TestEnvComposeGitPath locks the cross-agent contract: this MUST match
// gitops-agent's renderer.EnvComposeGitPath, since Portainer pulls the aggregate
// per-environment compose file gitops-agent committed.
func TestEnvComposeGitPath(t *testing.T) {
	got := envComposeGitPath("alpha", "prod")
	want := "clusters/beget-prod/projects/alpha/environments/prod/compose.yaml"
	if got != want {
		t.Errorf("envComposeGitPath = %q, want %q", got, want)
	}
}
