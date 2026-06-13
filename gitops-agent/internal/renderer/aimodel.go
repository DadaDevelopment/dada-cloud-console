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

// AIModelOwnerApp returns the app whose chart owns a model: the attached app
// when set, otherwise the per-project standalone "models-<project>" chart.
// (Supersedes the former model-owns-its-own-name layout, which collided across
// projects sharing an env — see StandaloneOwnerApp.)
func AIModelOwnerApp(attachedApp, projectSlug string) string {
	if attachedApp != "" {
		return attachedApp
	}
	return StandaloneOwnerApp("models", projectSlug)
}

// AIModelResourcesValuesGitPath returns the resources.values.yaml of the app
// that owns a model: the attached app, or the shared per-project
// "models-<project>" app when standalone. Both the AIModel CR and its companion
// PublicApi are entries in that file's manifests: list (keyed by kind+name), so
// one path now covers what used to be aimodel.yaml + publicapi.yaml.
func AIModelResourcesValuesGitPath(projectSlug, envSlug, attachedApp string) string {
	return AppResourcesValuesGitPath(projectSlug, envSlug, AIModelOwnerApp(attachedApp, projectSlug))
}

// AIModelDomain renders the canonical FQDN for a model: <name>-<project>.<envSuffix>.dada-tuda.ru.
// Keeps the mapping deterministic so the same model name in the same project/env
// always lands on the same domain.
func AIModelDomain(projectSlug, envSlug, modelName, baseDomain string) string {
	return fmt.Sprintf("%s-%s.%s.%s",
		modelName, projectSlug, envSlug, strings.TrimPrefix(baseDomain, "."))
}
