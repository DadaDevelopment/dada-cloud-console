package renderer_test

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

func TestRenderAIModel_CPU(t *testing.T) {
	got, err := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:            "iris-cls",
		Namespace:       "internal-prod",
		ProjectSlug:     "internal",
		EnvSlug:         "prod",
		OperationID:     "op-iris",
		ModelType:       "sklearn",
		Version:         "v1",
		Stage:           "production",
		ArtifactURI:     "s3://platform-models/internal/iris/v1",
		ProfileCPU:      "1",
		ProfileMemory:   "2Gi",
		APIKeySecretRef: "aimodel-iris-cls-apikey",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"kind: AIModel",
		"name: iris-cls",
		"namespace: internal-prod",
		"modelType: sklearn",
		"stage: production",
		"artifactURI: s3://platform-models/internal/iris/v1",
		"cpu: \"1\"",
		"memory: 2Gi",
		"minReplicas: 1",
		"maxReplicas: 1",
		"serviceAccountName: model-storage",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q\n%s", w, got)
		}
	}
	if strings.Contains(got, "gpu:") {
		t.Errorf("CPU profile should not render gpu field\n%s", got)
	}
	if strings.Contains(got, "canary:") {
		t.Errorf("nil canary should not render canary block\n%s", got)
	}
}

func TestRenderAIModel_GPUWithCanary(t *testing.T) {
	canary := 25
	got, err := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:          "llama",
		Namespace:     "internal-prod",
		ProjectSlug:   "internal",
		EnvSlug:       "prod",
		OperationID:   "op-llama",
		ModelType:     "huggingface",
		Version:       "v3",
		Stage:         "production",
		ArtifactURI:   "s3://platform-models/internal/llama/v3",
		ProfileCPU:    "8",
		ProfileMemory: "32Gi",
		ProfileGPU:    "1",
		CanaryPercent: &canary,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"gpu: \"1\"",
		"canary:",
		"trafficPercent: 25",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q\n%s", w, got)
		}
	}
}

func TestRenderAIModel_CustomContainer(t *testing.T) {
	got, err := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:           "custom-bert",
		Namespace:      "internal-prod",
		ProjectSlug:    "internal",
		EnvSlug:        "prod",
		OperationID:    "op-cb",
		ModelType:      "custom",
		Version:        "v1",
		Stage:          "production",
		ContainerImage: "ghcr.io/dada-tuda/bert-runner:1.0",
		ProfileCPU:     "2",
		ProfileMemory:  "4Gi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "container:\n    image: ghcr.io/dada-tuda/bert-runner:1.0") {
		t.Errorf("expected container block, got:\n%s", got)
	}
	if strings.Contains(got, "artifactURI:") {
		t.Errorf("custom modelType must not render artifactURI\n%s", got)
	}
}

func TestRenderAIModel_AttachedAppLabel(t *testing.T) {
	got, _ := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:            "x",
		Namespace:       "n",
		ProjectSlug:     "p",
		EnvSlug:         "e",
		OperationID:     "o",
		ModelType:       "sklearn",
		Version:         "v1",
		Stage:           "production",
		ArtifactURI:     "s3://x",
		ProfileCPU:      "1",
		ProfileMemory:   "2Gi",
		AttachedAppName: "my-api",
	})
	if !strings.Contains(got, "dada.io/attached-app: my-api") {
		t.Errorf("expected attached-app label, got:\n%s", got)
	}
}

func TestAIModelGitPaths(t *testing.T) {
	if got := renderer.AIModelGitPath("internal", "prod", "iris"); got !=
		"clusters/beget-prod/projects/internal/environments/prod/models/iris/aimodel.yaml" {
		t.Errorf("AIModelGitPath: %s", got)
	}
	if got := renderer.AIModelPublicApiGitPath("internal", "prod", "iris"); got !=
		"clusters/beget-prod/projects/internal/environments/prod/models/iris/publicapi.yaml" {
		t.Errorf("AIModelPublicApiGitPath: %s", got)
	}
}
