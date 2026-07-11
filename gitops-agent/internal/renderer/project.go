package renderer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ProjectEnv struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Type      string `yaml:"type"`
}

type ProjectSpec struct {
	Project            string         `yaml:"project"`
	DisplayName        string         `yaml:"displayName"`
	OwnerType          string         `yaml:"ownerType,omitempty"`
	DefaultEnvironment string         `yaml:"defaultEnvironment,omitempty"`
	Environments       []ProjectEnv   `yaml:"environments"`
	Quotas             map[string]any `yaml:"quotas"`
}

func RenderProject(spec ProjectSpec) (string, error) {
	if spec.OwnerType == "" {
		spec.OwnerType = "team"
	}
	if spec.DefaultEnvironment == "" {
		spec.DefaultEnvironment = "prod"
	}
	if len(spec.Environments) == 0 {
		spec.Environments = []ProjectEnv{DefaultProjectEnv(spec.Project, spec.DefaultEnvironment)}
	}
	if spec.Quotas == nil {
		spec.Quotas = map[string]any{}
	}

	b, err := yaml.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("rendering Project: %w", err)
	}
	return string(b), nil
}

func DefaultProjectEnv(projectSlug, envSlug string) ProjectEnv {
	return ProjectEnv{
		Name:      envSlug,
		Namespace: fmt.Sprintf("%s-%s", projectSlug, envSlug),
		Type:      envSlug,
	}
}

func ProjectGitPath(projectSlug string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/project.yaml", projectSlug)
}
