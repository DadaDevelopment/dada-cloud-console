package renderer_test

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

func TestRenderServiceDatabase(t *testing.T) {
	spec := renderer.ServiceDatabaseSpec{
		Name:            "myapp-db",
		Namespace:       "alpha-prod",
		ProjectSlug:     "alpha",
		EnvSlug:         "prod",
		AppRef:          "myapp",
		Database:        "myapp_db",
		BackupEnabled:   true,
		BackupSchedule:  "daily",
		BackupRetention: "14d",
		OperationID:     "op-123",
	}
	got, err := renderer.RenderServiceDatabase(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"apiVersion: platform.dada-tuda.ru/v1alpha1",
		"kind: ServiceDatabaseV2",
		"name: myapp-db",
		"dada.io/project: alpha",
		"dada.io/environment: prod",
		"dada.io/operation: op-123",
		"appRef: myapp",
		"namespace: alpha-prod",
		"engine: postgresql",
		"database: myapp_db",
		"enabled: true",
		"frequency: daily",
		"retention: 14d",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered ServiceDatabase missing %q\nFull output:\n%s", want, got)
		}
	}

	// ServiceDatabaseV2 is cluster-scoped: namespace must appear under spec, not metadata.
	// Parse the rendered YAML and verify the structure directly.
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("rendered ServiceDatabase is not valid YAML: %v", err)
	}
	if meta, ok := doc["metadata"].(map[string]interface{}); ok {
		if _, hasNs := meta["namespace"]; hasNs {
			t.Errorf("rendered ServiceDatabase must NOT have metadata.namespace (cluster-scoped XR)\nFull output:\n%s", got)
		}
	}
	spec2, ok := doc["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("rendered ServiceDatabase missing spec block\nFull output:\n%s", got)
	}
	if ns, _ := spec2["namespace"].(string); ns != "alpha-prod" {
		t.Errorf("spec.namespace = %q, want %q\nFull output:\n%s", ns, "alpha-prod", got)
	}
}

func TestRenderApp(t *testing.T) {
	spec := renderer.AppSpec{
		Name:        "api-service",
		Namespace:   "beta-staging",
		ProjectSlug: "beta",
		EnvSlug:     "staging",
		Image:       "ghcr.io/dada-tuda/api-service:v1.2.3",
		Port:        8080,
		Replicas:    2,
		Profile:     "medium",
		OperationID: "op-456",
		HelmRepoURL:        "https://github.com/DADA-TUDA/argo-infra.git",
		HelmTargetRevision: "main",
	}
	got, err := renderer.RenderApp(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"apiVersion: platform.dada-tuda.ru/v1alpha1",
		"kind: App",
		"name: api-service",
		"namespace: beta-staging",
		"dada.io/project: beta",
		"dada.io/environment: staging",
		"dada.io/operation: op-456",
		"namespace: beta-staging",
		"helm:",
		"repoURL: https://github.com/DADA-TUDA/argo-infra.git",
		"path: clusters/beget-prod/projects/beta/environments/staging/apps/api-service/chart",
		"targetRevision: main",
		"valueFile: clusters/beget-prod/projects/beta/environments/staging/apps/api-service/values.yaml",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered App missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderAppValues(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image:    "ghcr.io/dada-tuda/api-service:v1.2.3",
		Port:     8080,
		Replicas: 2,
		Profile:  "medium",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"image: ghcr.io/dada-tuda/api-service:v1.2.3",
		"port: 8080",
		"replicas: 2",
		"profile: medium",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered App values missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderPublicApi(t *testing.T) {
	spec := renderer.PublicApiSpec{
		Name:           "main-api",
		Namespace:      "gamma-prod",
		ProjectSlug:    "gamma",
		EnvSlug:        "prod",
		ServiceName:    "api-service",
		ServicePort:    8080,
		FQDN:           "api.gamma.dada-tuda.ru",
		LBTarget:       "93.189.231.60",
		AuthEnabled:    true,
		AuthScheme:     "bearer",
		AuthScopes:     []string{"read", "write"},
		SwaggerEnabled: true,
		SwaggerPath:    "/api-docs",
		SwaggerTitle:   "Gamma API",
		OperationID:    "op-789",
	}
	got, err := renderer.RenderPublicApi(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"kind: PublicApi",
		"name: main-api",
		"serviceName: api-service",
		"servicePort: 8080",
		"enabled: true",
		"scheme: bearer",
		"- read",
		"- write",
		"fqdn: api.gamma.dada-tuda.ru",
		"target: 93.189.231.60",
		"path: /api-docs",
		"title: Gamma API",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered PublicApi missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderPublicApi_NoAuth(t *testing.T) {
	spec := renderer.PublicApiSpec{
		Name:        "public-site",
		Namespace:   "delta-prod",
		ProjectSlug: "delta",
		EnvSlug:     "prod",
		ServiceName: "web",
		ServicePort: 3000,
		FQDN:        "www.delta.dada-tuda.ru",
		LBTarget:    "93.189.231.60",
		AuthEnabled: false,
		OperationID: "op-000",
	}
	got, err := renderer.RenderPublicApi(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With auth disabled, no scopes block should appear
	if strings.Contains(got, "scopes:") {
		t.Errorf("expected no scopes block when auth disabled, got:\n%s", got)
	}
}

func TestRenderProject(t *testing.T) {
	spec := renderer.ProjectSpec{
		Project:            "client-a",
		DisplayName:        "Client A Corp",
		OwnerType:          "client",
		DefaultEnvironment: "prod",
	}
	got, err := renderer.RenderProject(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"project: client-a",
		"displayName: Client A Corp",
		"ownerType: client",
		"defaultEnvironment: prod",
		"quotas: {}",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Project missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestGitPaths(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			"ServiceDatabaseGitPath",
			renderer.ServiceDatabaseGitPath("alpha", "prod", "myapp"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/myapp/chart/templates/servicedatabase.yaml",
		},
		{
			"AppGitPath",
			renderer.AppGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/application.yaml",
		},
		{
			"AppHelmChartGitPath",
			renderer.AppHelmChartGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/chart",
		},
		{
			"AppChartTemplatesGitPath",
			renderer.AppChartTemplatesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/chart/templates",
		},
		{
			"AppChartYamlGitPath",
			renderer.AppChartYamlGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/chart/Chart.yaml",
		},
		{
			"AppHelmValuesGitPath",
			renderer.AppHelmValuesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/values.yaml",
		},
		{
			"AppComposeGitPath",
			renderer.AppComposeGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/compose.yaml",
		},
		{
			"AppEnvGitPath",
			renderer.AppEnvGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/.env",
		},
		{
			"PublicApiGitPath",
			renderer.PublicApiGitPath("alpha", "prod", "api", "main"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/chart/templates/publicapi-main.yaml",
		},
		{
			"ProjectGitPath",
			renderer.ProjectGitPath("alpha"),
			"clusters/beget-prod/projects/alpha/project.yaml",
		},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestRenderChartYaml(t *testing.T) {
	got := renderer.RenderChartYaml("orders")
	wantSubstrings := []string{
		"apiVersion: v2",
		"name: orders",
		"type: application",
		"version: 0.1.0",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Chart.yaml missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestFQDNToName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"api.gamma.dada-tuda.ru", "api-gamma-dada-tuda-ru"},
		{"console.dada-tuda.ru", "console-dada-tuda-ru"},
	}
	for _, c := range cases {
		if got := renderer.FQDNToName(c.in); got != c.want {
			t.Errorf("FQDNToName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
