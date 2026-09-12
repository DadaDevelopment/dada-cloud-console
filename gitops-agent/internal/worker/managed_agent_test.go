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

// TestCarriedOverAgent_FieldsSurviveASaveThatDoesNotKnowAboutThem: the console
// save re-states every field the console knows, and it knows nothing about
// memory. An agent onboarded by hand keeps thirty days of notes; if a prompt fix
// dropped spec.memory, the agent would go on answering while quietly forgetting
// everything, and nobody would connect that to the edit.
func TestCarriedOverAgent_FieldsSurviveASaveThatDoesNotKnowAboutThem(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agents", "prod", "telemost-poc")

	full := filepath.Join(mgr.LocalPath(), valuesPath)
	content, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read values: %v", err)
	}
	withMemory := strings.Replace(string(content),
		"      prompt: |-",
		"      langfuseProjectId: \"cmt4v2gg60eajad0dyizlor3b\"\n      memory:\n        modelConfig: embeddings-local\n        ttlDays: 30\n      prompt: |-", 1)
	if withMemory == string(content) {
		t.Fatalf("fixture shape changed, memory was not injected:\n%s", content)
	}
	if err := os.WriteFile(full, []byte(withMemory), 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}

	carried, err := carriedOverAgent(mgr, valuesPath, "telemost-poc")
	if err != nil {
		t.Fatalf("carriedOverAgent: %v", err)
	}
	memory := carried.Memory
	if memory == nil || memory.ModelConfig != "embeddings-local" || memory.TTLDays != 30 {
		t.Fatalf("memory not carried over: %#v", memory)
	}
	if carried.LangfuseProjectID != "cmt4v2gg60eajad0dyizlor3b" {
		t.Fatalf("langfuse project not carried over: %q", carried.LangfuseProjectID)
	}

	saved, err := renderer.RenderManagedAgent(renderer.ManagedAgentSpec{
		Name:        "telemost-poc",
		Namespace:   "kagent",
		ProjectSlug: "agents",
		EnvSlug:     "prod",
		Prompt:      "Отвечай коротко.",
		Memory:      memory,

		LangfuseProjectID: carried.LangfuseProjectID,
	})
	if err != nil {
		t.Fatalf("RenderManagedAgent: %v", err)
	}
	if !strings.Contains(saved, "modelConfig: embeddings-local") || !strings.Contains(saved, "ttlDays: 30") {
		t.Fatalf("re-rendered claim lost its memory:\n%s", saved)
	}
	if !strings.Contains(saved, `langfuseProjectId: "cmt4v2gg60eajad0dyizlor3b"`) {
		t.Fatalf("re-rendered claim lost the Langfuse project, its traces link goes silent:\n%s", saved)
	}

	none, err := carriedOverAgent(mgr, valuesPath, "reels-poc")
	if err != nil {
		t.Fatalf("carriedOverAgent(absent): %v", err)
	}
	if none.Memory != nil {
		t.Fatalf("an agent without memory must stay without one, got %#v", none.Memory)
	}
	if none.LangfuseProjectID != "" {
		t.Fatalf("an agent that names no Langfuse project must stay without one, got %q", none.LangfuseProjectID)
	}
}

// TestFillUnsaid_APromptOnlySaveKeepsTheRestOfTheSpec replays the 2026-09-11
// native.38 incident: a saveAgent carrying only the prompt re-rendered the whole
// claim, modelConfig/runtime/description/tools fell out, the agent dropped onto
// the platform default model tier and the bot went silent for 38 hours. A save
// that says nothing about a field must leave what git has; a save that names a
// new value must win.
func TestFillUnsaid_APromptOnlySaveKeepsTheRestOfTheSpec(t *testing.T) {
	mgr, valuesPath := agentCarrierFixture(t, "agents", "prod", "native")

	fullSpec := renderer.ManagedAgentSpec{
		Name:        "native",
		Namespace:   "kagent",
		ProjectSlug: "agents",
		EnvSlug:     "prod",
		DisplayName: "Hello Trading",
		Description: "Referral bot",
		ModelConfig: "tg-referral-glm-53-flash",
		Runtime:     "python",
		Prompt:      "Ты помощник.",
		Tools: []renderer.ManagedAgentToolRef{{
			Name:     "tg-agent-tools",
			URL:      "http://tg-agent-tools.agents-prod.svc:8080/mcp",
			Protocol: "streamable-http",
			Timeout:  "30s",
			Headers:  []renderer.ManagedAgentToolHeader{{Name: "X-Project", Value: "agents"}},
		}},
		Env: []renderer.ManagedAgentEnvVar{{Name: "REFERRAL_TIER", Value: "flash"}},
	}
	yaml, err := renderer.RenderManagedAgent(fullSpec)
	if err != nil {
		t.Fatalf("RenderManagedAgent(full): %v", err)
	}
	var b strings.Builder
	b.WriteString("manifests:\n")
	for i, line := range strings.Split(strings.TrimRight(yaml, "\n"), "\n") {
		if i == 0 {
			b.WriteString("  - " + line + "\n")
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	full := filepath.Join(mgr.LocalPath(), valuesPath)
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}

	carried, err := carriedOverAgent(mgr, valuesPath, "native")
	if err != nil {
		t.Fatalf("carriedOverAgent: %v", err)
	}

	promptOnly := renderer.ManagedAgentSpec{
		Name:        "native",
		Namespace:   "kagent",
		ProjectSlug: "agents",
		EnvSlug:     "prod",
		Prompt:      "Ты помощник. Отвечай коротко.",
	}
	fillUnsaid(&promptOnly, carried)
	saved, err := renderer.RenderManagedAgent(promptOnly)
	if err != nil {
		t.Fatalf("RenderManagedAgent(prompt-only): %v", err)
	}
	for _, want := range []string{
		"modelConfig: tg-referral-glm-53-flash",
		"runtime: python",
		`displayName: "Hello Trading"`,
		`description: "Referral bot"`,
		"- name: tg-agent-tools",
		"url: http://tg-agent-tools.agents-prod.svc:8080/mcp",
		"protocol: streamable-http",
		"timeout: 30s",
		`- name: "X-Project"`,
		"- name: REFERRAL_TIER",
		`value: "flash"`,
		"Отвечай коротко.",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("a prompt-only save lost %q from the claim:\n%s", want, saved)
		}
	}

	retargeted := renderer.ManagedAgentSpec{
		Name:        "native",
		Namespace:   "kagent",
		ProjectSlug: "agents",
		EnvSlug:     "prod",
		Prompt:      "Ты помощник.",
		ModelConfig: "tg-referral-gpt5-mini",
	}
	fillUnsaid(&retargeted, carried)
	if retargeted.ModelConfig != "tg-referral-gpt5-mini" {
		t.Fatalf("a save that names a new modelConfig must win, got %q", retargeted.ModelConfig)
	}
	if retargeted.Runtime != "python" || len(retargeted.Tools) != 1 {
		t.Fatalf("naming one field must not drop the others: %#v", retargeted)
	}
}
