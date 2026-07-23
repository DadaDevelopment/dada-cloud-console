package renderer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LimitRangeSpec defines per-container CPU/memory defaults and bounds.
type LimitRangeSpec struct {
	DefaultMemory string `yaml:"defaultMemory"`
	DefaultCpu    string `yaml:"defaultCpu"`
	MaxMemory     string `yaml:"maxMemory"`
	MaxCpu        string `yaml:"maxCpu"`
	MinMemory     string `yaml:"minMemory"`
}

// ResourceQuotaSpec defines aggregate resource caps for a namespace.
type ResourceQuotaSpec struct {
	RequestsMemory string `yaml:"requestsMemory"`
	RequestsCpu    string `yaml:"requestsCpu"`
	LimitsMemory   string `yaml:"limitsMemory"`
	LimitsCpu      string `yaml:"limitsCpu"`
	Pods           string `yaml:"pods"`
}

// RegistrySecretSpec toggles rendering of the internal-registry image pull
// secret by the namespace-policy chart. Only the enabled flag is written here;
// registry host and credentials merge in from the chart's default values.
type RegistrySecretSpec struct {
	Enabled bool `yaml:"enabled"`
}

// NamespacePolicySpec holds parameters for a namespace-policy manifest.
type NamespacePolicySpec struct {
	Namespace      string              `yaml:"namespace"`
	LimitRange     LimitRangeSpec      `yaml:"limitRange"`
	ResourceQuota  ResourceQuotaSpec   `yaml:"resourceQuota"`
	RegistrySecret *RegistrySecretSpec `yaml:"registrySecret,omitempty"`
}

func RenderNamespacePolicy(spec NamespacePolicySpec) (string, error) {
	b, err := yaml.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("rendering NamespacePolicy: %w", err)
	}
	return string(b), nil
}

func NamespacePolicyGitPath(namespace string) string {
	return fmt.Sprintf("clusters/beget-prod/namespace-policies/%s.yaml", namespace)
}
