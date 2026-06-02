package worker

import "testing"

// TestComposeGitPath locks the cross-agent contract: this MUST match
// gitops-agent's renderer.AppComposeGitPath, since Portainer pulls the compose
// file gitops-agent committed.
func TestComposeGitPath(t *testing.T) {
	got := composeGitPath("alpha", "prod", "api")
	want := "clusters/beget-prod/projects/alpha/environments/prod/apps/api/compose.yaml"
	if got != want {
		t.Errorf("composeGitPath = %q, want %q", got, want)
	}
}
