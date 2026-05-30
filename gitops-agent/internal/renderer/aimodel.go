package renderer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// AIModelSpec holds parameters for an AIModel manifest.
//
// Mirror of platform.dada-tuda.ru/v1alpha1 AIModel XRD.
// Use ContainerImage with ModelType="custom"; otherwise use ArtifactURI.
type AIModelSpec struct {
	Name            string
	Namespace       string
	ProjectSlug     string
	EnvSlug         string
	OperationID     string
	ModelType       string
	Version         string
	Stage           string
	ArtifactURI     string
	ContainerImage  string
	ProfileCPU      string
	ProfileMemory   string
	ProfileGPU      string
	CanaryPercent   *int   // omitted from spec when nil
	APIKeySecretRef string // name of the K8s Secret backing PublicApi auth
	AttachedAppName string // empty when not attached
}

var aiModelTmpl = template.Must(template.New("aimodel").Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: AIModel
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}{{ if .AttachedAppName }}
    dada.io/attached-app: {{ .AttachedAppName }}{{ end }}
spec:
  modelType: {{ .ModelType }}
  version: {{ .Version }}
  stage: {{ .Stage }}
{{- if eq .ModelType "custom" }}
  container:
    image: {{ .ContainerImage }}
{{- else }}
  artifactURI: {{ .ArtifactURI }}
{{- end }}
  resources:
    cpu: "{{ .ProfileCPU }}"
    memory: {{ .ProfileMemory }}{{ if .ProfileGPU }}
    gpu: "{{ .ProfileGPU }}"{{ end }}
  autoscaling:
    minReplicas: 1
    maxReplicas: 1
    targetConcurrency: 0{{ if .CanaryPercent }}
  canary:
    trafficPercent: {{ .CanaryPercent }}{{ end }}
  storage:
    serviceAccountName: model-storage
    secretName: model-storage-s3
`))

// RenderAIModel produces the YAML for one AIModel CR.
func RenderAIModel(spec AIModelSpec) (string, error) {
	var buf bytes.Buffer
	if err := aiModelTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering AIModel: %w", err)
	}
	return buf.String(), nil
}

// AIModelGitPath returns the canonical Git path for an AIModel CR. The model
// lives inside its owning app's Helm chart (chart/templates/), where ownerApp is
// the attached app when set, otherwise the model's own name. This supersedes the
// former env-level models/ layout (D10): every resource now belongs to an app.
func AIModelGitPath(projectSlug, envSlug, ownerApp string) string {
	return AppChartTemplatesGitPath(projectSlug, envSlug, ownerApp) + "/aimodel.yaml"
}

// AIModelPublicApiGitPath returns the Git path for the PublicApi CR emitted
// alongside an AIModel, inside the same owning app's chart templates.
func AIModelPublicApiGitPath(projectSlug, envSlug, ownerApp string) string {
	return AppChartTemplatesGitPath(projectSlug, envSlug, ownerApp) + "/publicapi.yaml"
}

// AIModelDomain renders the canonical FQDN for a model: <name>-<project>.<envSuffix>.dada-tuda.ru.
// Keeps the mapping deterministic so the same model name in the same project/env
// always lands on the same domain.
func AIModelDomain(projectSlug, envSlug, modelName, baseDomain string) string {
	return fmt.Sprintf("%s-%s.%s.%s",
		modelName, projectSlug, envSlug, strings.TrimPrefix(baseDomain, "."))
}
