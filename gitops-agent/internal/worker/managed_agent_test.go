package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// agentCarrierFixture writes the project's agent carrier with the named agents
// in it, so the removal path can be exercised without a remote.
func agentCarrierFixture(t *testing.T, projectSlug, envSlug string, names ...string) (*git.Manager, string) {
	t.Helper()

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.invalid/scm/dada/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})
	valuesPath := renderer.ManagedAgentResourcesValuesGitPath(projectSlug, envSlug)

	var b strings.Builder
	b.WriteString("manifests:\n")
	for _, name := range names {
		yaml, err := renderer.RenderManagedAgent(renderer.ManagedAgentSpec{
			Name:        name,
			Namespace:   "kagent",
			ProjectSlug: projectSlug,
			EnvSlug:     envSlug,
			OperationID: "11111111-1111-1111-1111-111111111111",
			Prompt:      "Ты помощник.\n\nОтвечай коротко.",
		})
		if err != nil {
			t.Fatalf("RenderManagedAgent(%s): %v", name, err)
		}
		for i, line := range strings.Split(strings.TrimRight(yaml, "\n"), "\n") {
			if i == 0 {
				b.WriteString("  - " + line + "\n")
				continue
			}
			b.WriteString("    " + line + "\n")
		}
	}

	full := filepath.Join(mgr.LocalPath(), valuesPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}
	return mgr, valuesPath
}

// TestSaveAgent_UpsertsInPlace: the console has one "save", so an edit re-states
// the whole CR. It must replace the agent's entry rather than append a second
// one -- two claims with the same name compose over each other, and which
// prompt answers users is then decided by list order.
func TestSaveAgent_UpsertsInPlace(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agent-sandbox", "prod", "reels-poc", "digest-poc")

	updated, err := renderer.RenderManagedAgent(renderer.ManagedAgentSpec{
		Name:          "reels-poc",
		Namespace:     "kagent",
		ProjectSlug:   "agent-sandbox",
		EnvSlug:       "prod",
		OperationID:   "22222222-2222-2222-2222-222222222222",
		PromptVersion: "4",
		Prompt:        "Новый промпт.\n\nВторой абзац.",
	})
	if err != nil {
		t.Fatalf("RenderManagedAgent: %v", err)
	}
	file, err := upsertManifestFile(mgr, valuesPath, updated)
	if err != nil {
		t.Fatalf("upsertManifestFile: %v", err)
	}
	if strings.Count(file.Content, "name: reels-poc") != 1 {
		t.Errorf("saving an agent twice must replace its claim:\n%s", file.Content)
	}
	if !strings.Contains(file.Content, "Новый промпт.") {
		t.Errorf("the edited prompt did not land:\n%s", file.Content)
	}
	if !strings.Contains(file.Content, "Второй абзац.") {
		t.Errorf("the paragraph after the blank line was lost in the carrier:\n%s", file.Content)
	}
	if !strings.Contains(file.Content, "digest-poc") {
		t.Errorf("a sibling agent was dropped by a single-agent save:\n%s", file.Content)
	}
}

// A deleted agent must leave alone the other agents sharing its carrier.
func TestDeleteAgent_RemovesOnlyTheNamedAgent(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agent-sandbox", "prod", "reels-poc", "digest-poc")

	file, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"ManagedAgent", "digest-poc"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if !changed {
		t.Fatal("removing an agent that is in the file must report a change")
	}
	if strings.Contains(file.Content, "digest-poc") {
		t.Errorf("deleted agent still present:\n%s", file.Content)
	}
	if !strings.Contains(file.Content, "reels-poc") {
		t.Errorf("sibling agent was dropped by a single-agent delete:\n%s", file.Content)
	}
	empty, err := manifestsFileIsEmpty(file)
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if empty {
		t.Fatal("carrier still holds an agent; reporting it empty tears the carrier app down and takes the survivor with it")
	}
}

// The last agent empties the carrier, which is the signal doDeleteAgent uses to
// remove the carrier app whole instead of committing a manifests list ArgoCD
// refuses to auto-sync.
func TestDeleteAgent_LastAgentEmptiesTheCarrier(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agent-sandbox", "prod", "reels-poc")

	file, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"ManagedAgent", "reels-poc"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if !changed {
		t.Fatal("removing the only agent must report a change")
	}
	empty, err := manifestsFileIsEmpty(file)
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("last agent removed but the carrier is not reported empty:\n%s", file.Content)
	}
}

// An agent written into the runtime by hand is absent from the carrier: no
// change, which doDeleteAgent turns into a failed operation rather than a green
// delete over an agent that is still answering users.
func TestDeleteAgent_UnknownAgentIsNotAChange(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agent-sandbox", "prod", "reels-poc")

	_, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"ManagedAgent", "k8s-agent"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if changed {
		t.Fatal("an agent absent from the carrier must not report a change")
	}
}
