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
		Name:               "api-service",
		Namespace:          "beta-staging",
		ProjectSlug:        "beta",
		EnvSlug:            "staging",
		Image:              "ghcr.io/dada-tuda/api-service:v1.2.3",
		Port:               8080,
		Replicas:           2,
		Profile:            "medium",
		OperationID:        "op-456",
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
		"path: clusters/beget-prod/projects/beta/environments/staging/apps/api-service/resources",
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

func TestRenderCustomIngress(t *testing.T) {
	spec := renderer.CustomIngressSpec{
		Name:            "shop-acme-com",
		Namespace:       "delta-prod",
		ProjectSlug:     "delta",
		EnvSlug:         "prod",
		Hostname:        "shop.acme.com",
		ServiceName:     "web-service",
		ServicePortName: "http",
		OperationID:     "op-ci",
	}
	got, err := renderer.RenderCustomIngress(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSubstrings := []string{
		"kind: Ingress",
		"name: shop-acme-com",
		"namespace: delta-prod",
		"cert-manager.io/cluster-issuer: letsencrypt-prod",
		"ingressClassName: nginx",
		"secretName: shop-acme-com-tls",
		"host: shop.acme.com",
		"name: web-service",
		"name: http",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered custom Ingress missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderComposeFromDiscovery(t *testing.T) {
	tests := []struct {
		name           string
		services       []renderer.ImportServiceSpec
		hasEnv         bool
		wantSubstrings []string
		wantAbsent     []string
	}{
		{
			name: "named volume pinned external",
			services: []renderer.ImportServiceSpec{
				{ContainerName: "web_1", ServiceName: "web", Image: "nginx:1.25", Ports: []string{"80:80"}, Volumes: []string{"data:/var/lib/x"}, Include: true},
			},
			wantSubstrings: []string{
				"services:", "web:", "image: nginx:1.25", "80:80", "data:/var/lib/x",
				"volumes:", "data:", "external: true", "name: data",
			},
		},
		{
			name: "bind mount passes through, no volumes block",
			services: []renderer.ImportServiceSpec{
				{ContainerName: "web_1", ServiceName: "web", Image: "nginx:1.25", Volumes: []string{"/opt/app/data:/var/lib/x"}, Include: true},
			},
			wantSubstrings: []string{"web:", "/opt/app/data:/var/lib/x"},
			wantAbsent:     []string{"external: true"},
		},
		{
			name: "excluded service dropped",
			services: []renderer.ImportServiceSpec{
				{ContainerName: "web_1", ServiceName: "web", Image: "nginx:1.25", Include: true},
				{ContainerName: "cache_1", ServiceName: "cache", Image: "redis:7", Include: false},
			},
			wantSubstrings: []string{"web:"},
			wantAbsent:     []string{"cache:", "redis:7"},
		},
		{
			name: "env_file wired when env vars present",
			services: []renderer.ImportServiceSpec{
				{ContainerName: "web_1", ServiceName: "web", Image: "nginx:1.25", Include: true},
			},
			hasEnv:         true,
			wantSubstrings: []string{"env_file:", "- .env"},
		},
		{
			name: "no env_file when no env vars",
			services: []renderer.ImportServiceSpec{
				{ContainerName: "web_1", ServiceName: "web", Image: "nginx:1.25", Include: true},
			},
			hasEnv:     false,
			wantAbsent: []string{"env_file:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderer.RenderComposeFromDiscovery(tt.services, tt.hasEnv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var doc map[string]interface{}
			if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("rendered compose is not valid YAML: %v\nFull output:\n%s", err, got)
			}
			for _, want := range tt.wantSubstrings {
				if !strings.Contains(got, want) {
					t.Errorf("rendered compose missing %q\nFull output:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("rendered compose unexpectedly contains %q\nFull output:\n%s", absent, got)
				}
			}
		})
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
			"ServiceDatabaseResourcesValuesGitPath/bound",
			renderer.ServiceDatabaseResourcesValuesGitPath("alpha", "prod", "myapp"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/myapp/resources.values.yaml",
		},
		{
			"ServiceDatabaseResourcesValuesGitPath/standalone",
			renderer.ServiceDatabaseResourcesValuesGitPath("alpha", "prod", ""),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/service-databases-alpha/resources.values.yaml",
		},
		{
			"AppGitPath",
			renderer.AppGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/app.yaml",
		},
		{
			"AppResourcesGitPath",
			renderer.AppResourcesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/resources",
		},
		{
			"AppResourcesValuesGitPath",
			renderer.AppResourcesValuesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/resources.values.yaml",
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
			"PublicApiResourcesValuesGitPath",
			renderer.PublicApiResourcesValuesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/resources.values.yaml",
		},
		{
			"S3BucketResourcesValuesGitPath/bound",
			renderer.S3BucketResourcesValuesGitPath("alpha", "prod", "api"),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/api/resources.values.yaml",
		},
		{
			"S3BucketResourcesValuesGitPath/standalone",
			renderer.S3BucketResourcesValuesGitPath("alpha", "prod", ""),
			"clusters/beget-prod/projects/alpha/environments/prod/apps/s3-buckets-alpha/resources.values.yaml",
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

func TestRenderAggregateCompose(t *testing.T) {
	specs := []renderer.AppServiceSpec{
		{AppName: "api", Image: "ghcr.io/acme/api:2.1", Ports: []string{"8080:8080"}, Volumes: []string{"api_data:/var/lib/api"}, HasEnv: true},
		{AppName: "redis", Image: "redis:7"},
		{AppName: "nginx", Image: "nginx:1.25", Ports: []string{"443:443", "80:80"}, Volumes: []string{"/etc/nginx:/etc/nginx:ro"}},
	}
	got, err := renderer.RenderAggregateCompose(specs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var doc struct {
		Services map[string]struct {
			Image   string   `yaml:"image"`
			Restart string   `yaml:"restart"`
			Ports   []string `yaml:"ports"`
			Volumes []string `yaml:"volumes"`
			EnvFile []string `yaml:"env_file"`
		} `yaml:"services"`
		Volumes map[string]struct {
			External bool   `yaml:"external"`
			Name     string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("aggregate is not valid yaml: %v\n%s", err, got)
	}

	if len(doc.Services) != 3 {
		t.Fatalf("want 3 services, got %d: %v", len(doc.Services), got)
	}
	for name, svc := range doc.Services {
		if svc.Restart != "unless-stopped" {
			t.Errorf("service %q: restart = %q, want unless-stopped", name, svc.Restart)
		}
	}
	if ef := doc.Services["api"].EnvFile; len(ef) != 1 || ef[0] != "apps/api/.env" {
		t.Errorf("api env_file = %v, want [apps/api/.env]", ef)
	}
	if len(doc.Services["redis"].EnvFile) != 0 {
		t.Errorf("redis should have no env_file, got %v", doc.Services["redis"].EnvFile)
	}
	if v, ok := doc.Volumes["api_data"]; !ok || !v.External || v.Name != "api_data" {
		t.Errorf("named volume api_data must be pinned external with literal name, got %+v", doc.Volumes)
	}
	if _, ok := doc.Volumes["/etc/nginx"]; ok {
		t.Errorf("bind mount /etc/nginx must NOT be pinned as an external volume")
	}
}

// TestRenderAggregateComposeAdopted is the findata data-safety guarantee: an
// adopted stack's verbatim service blocks + original top-level volumes (with the
// external-name mapping profi_pg_data -> compose_profi_pg_data) round-trip
// unchanged through the aggregate, so a redeploy never orphans prod data or drops
// environment/expose/depends_on/bind-mounts.
func TestRenderAggregateComposeAdopted(t *testing.T) {
	pg := map[string]any{
		"image":   "mirror.gcr.io/library/postgres:16-alpine",
		"restart": "unless-stopped",
		"environment": map[string]any{
			"POSTGRES_DB": "feedback", "POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "pswd",
		},
		"ports":   []any{"65433:5432"},
		"volumes": []any{"profi_pg_data:/var/lib/postgresql/data"},
	}
	backend := map[string]any{
		"image":      "nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194",
		"restart":    "unless-stopped",
		"env_file":   []any{".env"},
		"expose":     []any{"8001"},
		"depends_on": []any{"postgres"},
	}
	stackVols := map[string]any{
		"profi_pg_data": map[string]any{"external": true, "name": "compose_profi_pg_data"},
	}
	got, err := renderer.RenderAggregateCompose([]renderer.AppServiceSpec{
		{AppName: "postgres", Service: pg},
		{AppName: "backend", Service: backend},
	}, stackVols)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not valid yaml: %v\n%s", err, got)
	}
	for _, want := range []string{
		"name: compose_profi_pg_data", "POSTGRES_PASSWORD: pswd",
		"depends_on:", "expose:", "env_file:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("adopted aggregate dropped %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dada.io/app") {
		t.Errorf("adopted service must NOT gain a dada.io/app label (would recreate the container):\n%s", got)
	}
}

func TestRenderAppServiceFragment(t *testing.T) {
	got, err := renderer.RenderAppServiceFragment(renderer.AppServiceSpec{
		AppName: "api", Image: "ghcr.io/acme/api:2.1", Ports: []string{"8080:8080"},
		Volumes: []string{"api_data:/var/lib/api"}, HasEnv: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"services:", "api:", "image: ghcr.io/acme/api:2.1", "external: true", "name: api_data"} {
		if !strings.Contains(got, want) {
			t.Errorf("fragment missing %q:\n%s", want, got)
		}
	}
}
