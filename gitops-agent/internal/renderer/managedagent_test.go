package renderer

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func agentSpec() ManagedAgentSpec {
	return ManagedAgentSpec{
		Name:          "reels-poc",
		Namespace:     "kagent",
		ProjectSlug:   "internal",
		EnvSlug:       "prod",
		OperationID:   "op-1",
		DisplayName:   "Reels: план на неделю",
		PromptVersion: "3",
		ModelConfig:   "default-model-config",
		Runtime:       "python",
		Prompt:        "Ты помощник.\n\nОтвечай коротко: по делу.\n",
		Tools: []ManagedAgentToolRef{
			{Name: "reels-task-tools", URL: "http://reels/mcp", Timeout: "30s",
				AllowedHeaders: []string{"x-dada-user"}},
			{Name: "shared-tools"},
		},
		Env: []ManagedAgentEnvVar{{Name: "AGENTSYNC_BASE_URL", Value: "https://agentsync.dada-tuda.ru"}},
	}
}

// TestRenderManagedAgent_ParsesAndKeepsTheWholePrompt guards the failure that
// makes a rendered agent worse than no agent: a blank line inside a block
// scalar at column zero closes the block, so the second half of the prompt
// becomes top-level YAML keys. Every human-written prompt has a blank line.
func TestRenderManagedAgent_ParsesAndKeepsTheWholePrompt(t *testing.T) {
	out, err := RenderManagedAgent(agentSpec())
	if err != nil {
		t.Fatalf("RenderManagedAgent: %v", err)
	}

	var doc struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
		Spec struct {
			Namespace     string `yaml:"namespace"`
			ProjectRef    string `yaml:"projectRef"`
			DisplayName   string `yaml:"displayName"`
			PromptVersion string `yaml:"promptVersion"`
			ModelConfig   string `yaml:"modelConfig"`
			Runtime       string `yaml:"runtime"`
			Prompt        string `yaml:"prompt"`
			Tools         []struct {
				Name           string   `yaml:"name"`
				URL            string   `yaml:"url"`
				Timeout        string   `yaml:"timeout"`
				AllowedHeaders []string `yaml:"allowedHeaders"`
			} `yaml:"tools"`
			Env []struct {
				Name  string `yaml:"name"`
				Value string `yaml:"value"`
			} `yaml:"env"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered agent does not parse: %v\n%s", err, out)
	}
	if doc.Kind != "ManagedAgent" || doc.Metadata.Name != "reels-poc" {
		t.Fatalf("wrong object: %s", out)
	}
	if doc.Spec.Prompt != "Ты помощник.\n\nОтвечай коротко: по делу." {
		t.Fatalf("prompt round-tripped as %q\n%s", doc.Spec.Prompt, out)
	}
	if doc.Spec.DisplayName != "Reels: план на неделю" {
		t.Errorf("displayName = %q; a colon in free text must not split the scalar", doc.Spec.DisplayName)
	}
	if doc.Metadata.Labels["dada.io/project"] != "internal" {
		t.Errorf("labels = %#v", doc.Metadata.Labels)
	}
	if len(doc.Spec.Tools) != 2 {
		t.Fatalf("tools = %#v", doc.Spec.Tools)
	}
	if doc.Spec.Tools[0].URL != "http://reels/mcp" || doc.Spec.Tools[0].AllowedHeaders[0] != "x-dada-user" {
		t.Errorf("owned tool read wrong: %#v", doc.Spec.Tools[0])
	}
	if doc.Spec.Tools[1].URL != "" {
		t.Errorf("a shared server must be referenced by name only, got url %q", doc.Spec.Tools[1].URL)
	}
	if len(doc.Spec.Env) != 1 || doc.Spec.Env[0].Value != "https://agentsync.dada-tuda.ru" {
		t.Errorf("env read wrong: %#v", doc.Spec.Env)
	}
}

// TestRenderManagedAgent_RefusesAnEmptyPrompt: such an agent starts, answers,
// and answers as the bare model -- the hardest kind of broken to notice, so it
// never reaches git.
func TestRenderManagedAgent_RefusesAnEmptyPrompt(t *testing.T) {
	spec := agentSpec()
	spec.Prompt = "  \n\n"
	if _, err := RenderManagedAgent(spec); err == nil {
		t.Fatal("an empty prompt must be refused")
	}
}

// TestRenderManagedAgent_MinimalClaimStillParses covers the shape the console
// sends for an agent with no tools and no env: optional blocks must vanish
// rather than emit an empty key.
func TestRenderManagedAgent_MinimalClaimStillParses(t *testing.T) {
	out, err := RenderManagedAgent(ManagedAgentSpec{
		Name: "solo", Namespace: "kagent", ProjectSlug: "acme", EnvSlug: "prod",
		OperationID: "op-2", Prompt: "Be brief.",
	})
	if err != nil {
		t.Fatalf("RenderManagedAgent: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("minimal agent does not parse: %v\n%s", err, out)
	}
	if strings.Contains(out, "tools:") || strings.Contains(out, "env:") {
		t.Errorf("empty optional blocks must not be emitted:\n%s", out)
	}
}

// TestRenderManagedAgent_UpsertsIntoTheCarrierFile proves the rendered CR is
// accepted by the same manifests-list carrier every other platform resource
// rides, keyed by kind+name.
func TestRenderManagedAgent_UpsertsIntoTheCarrierFile(t *testing.T) {
	out, err := RenderManagedAgent(agentSpec())
	if err != nil {
		t.Fatalf("RenderManagedAgent: %v", err)
	}
	rv, err := ParseResourcesValues("")
	if err != nil {
		t.Fatalf("ParseResourcesValues: %v", err)
	}
	if err := rv.Upsert(out); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := rv.Upsert(out); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	if len(rv.Manifests) != 1 {
		t.Fatalf("upserting the same agent twice must replace it, got %d manifests", len(rv.Manifests))
	}
	if got := ManagedAgentResourcesValuesGitPath("internal", "prod"); got !=
		"clusters/beget-prod/projects/internal/environments/prod/apps/agents-internal/resources.values.yaml" {
		t.Errorf("carrier path = %q", got)
	}
}
