package renderer_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

// findataLiveCompose is the exact prod profi-vm stack (fin-data.pro, Portainer
// endpoint 3), sans comments. The adopt cutover MUST reproduce it structurally
// so docker compose recreates nothing beyond the deliberate stack swap and the
// external postgres volume (compose_profi_pg_data) is never orphaned.
const findataLiveCompose = `
services:
  postgres:
    image: mirror.gcr.io/library/postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: feedback
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: pswd
    ports:
      - "65433:5432"
    volumes:
      - profi_pg_data:/var/lib/postgresql/data
  backend:
    image: nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194
    restart: unless-stopped
    env_file:
      - .env
    environment:
      DB_URL: postgresql+asyncpg://postgres:pswd@postgres:5432/feedback
    expose:
      - "8001"
    depends_on:
      - postgres
  frontend:
    image: nexus.dada-tuda.ru/dada/profi:master-1.0.0-174
    restart: unless-stopped
    environment:
      VITE_API_BASE: https://fin-data.pro
    expose:
      - "5173"
  nginx:
    image: mirror.gcr.io/library/nginx:1.27-alpine
    restart: unless-stopped
    depends_on:
      - backend
      - frontend
    environment:
      DOMAIN: fin-data.pro
      NGINX_SSL_CERT_PATH: /etc/nginx/certs/live/fin-data.pro/fullchain.pem
      NGINX_SSL_KEY_PATH: /etc/nginx/certs/live/fin-data.pro/privkey.pem
      BACKEND_UPSTREAM: backend:8001
      FRONTEND_UPSTREAM: frontend:5173
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /home/ubuntuuser/compose/nginx/default.conf.template:/etc/nginx/templates/default.conf.template:ro
      - /home/ubuntuuser/compose/nginx/.htpasswd:/etc/nginx/.htpasswd:ro
      - /etc/letsencrypt:/etc/nginx/certs:ro
volumes:
  profi_pg_data:
    external: true
    name: compose_profi_pg_data
`

// TestAdoptFindataRoundTrip is the prod cutover gate: parsing the live findata
// compose and rendering it back through the adopt path (verbatim per-service
// blocks + preserved top-level volumes) must reproduce the SAME services and
// volumes structure — proving no data-carrying field is dropped and the external
// volume name survives.
func TestAdoptFindataRoundTrip(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(findataLiveCompose), &doc); err != nil {
		t.Fatalf("parse live compose: %v", err)
	}
	services := doc["services"].(map[string]any)
	volumes, _ := doc["volumes"].(map[string]any)
	var specs []renderer.AppServiceSpec
	for name, block := range services {
		specs = append(specs, renderer.AppServiceSpec{AppName: name, Service: block.(map[string]any)})
	}
	got, err := renderer.RenderAggregateCompose(specs, volumes)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("rendered not valid yaml: %v\n%s", err, got)
	}
	if !reflect.DeepEqual(out["services"], doc["services"]) {
		t.Errorf("SERVICES changed by round-trip:\n--- orig ---\n%v\n--- got ---\n%v", doc["services"], out["services"])
	}
	if !reflect.DeepEqual(out["volumes"], doc["volumes"]) {
		t.Errorf("VOLUMES changed by round-trip (DATA-SAFETY BREACH):\n orig=%v\n got=%v", doc["volumes"], out["volumes"])
	}
}

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
		ExternalEnabled: true,
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
		"external:",
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

func TestRenderServiceDatabaseStandaloneAppRef(t *testing.T) {
	got, err := renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:        "zerkalo",
		Namespace:   "ggrk52-prod",
		ProjectSlug: "ggrk52",
		EnvSlug:     "prod",
		AppRef:      "",
		Database:    "zerkalo",
		OperationID: "op-x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "appRef: zerkalo") {
		t.Errorf("standalone DB must self-own (appRef defaults to name), never emit null appRef\nFull output:\n%s", got)
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
		Framework:          "javascript",
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
		"resources: true",
		"helm:",
		"repoURL: " + renderer.WorkloadRepoURL,
		"path: helm/javascript",
		"targetRevision: " + renderer.WorkloadBranch,
		"releaseName: api-service",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered App missing %q\nFull output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/api-service/resources") {
		t.Errorf("workload App must NOT point at the dead per-app resources/ dir\nFull output:\n%s", got)
	}
}

func TestRenderAppResourcesOnly(t *testing.T) {
	spec := renderer.AppSpec{
		Name:               "service-databases-beta",
		Namespace:          "beta-prod",
		ProjectSlug:        "beta",
		EnvSlug:            "prod",
		OperationID:        "op-789",
		HelmRepoURL:        "https://github.com/DADA-TUDA/argo-infra.git",
		HelmTargetRevision: "console-migration",
		ResourcesOnly:      true,
		ResourcesValueFile: "clusters/beget-prod/projects/beta/environments/prod/apps/service-databases-beta/resources.values.yaml",
	}
	got, err := renderer.RenderApp(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "/service-databases-beta/resources\n") {
		t.Errorf("resources-only App must NOT point spec.helm.path at the dead per-app resources/ dir\nFull output:\n%s", got)
	}
	for _, want := range []string{
		"path: helm/app-resources",
		"valueFile: clusters/beget-prod/projects/beta/environments/prod/apps/service-databases-beta/resources.values.yaml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("resources-only App missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderAppValues(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image:    "ghcr.io/dada-tuda/api-service:v1.2.3",
		Port:     8080,
		Replicas: 2,
		Profile:  "medium",
		Env:      map[string]string{"LOG_LEVEL": "info"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSubstrings := []string{
		"common:",
		"name: ghcr.io/dada-tuda/api-service",
		"tag: v1.2.3",
		"servicePort: 8080",
		"replicas: 2",
		"cpu: 100m",
		"name: LOG_LEVEL",
		"value: info",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered App values missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderAppValuesPersistentVolume(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image:              "ghcr.io/dada-tuda/api-service:v1",
		Port:               8080,
		Replicas:           3,
		Profile:            "small",
		VolumePath:         "/data",
		VolumeSize:         "5Gi",
		VolumeStorageClass: "longhorn-prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"pvc:",
		"size: 5Gi",
		"storageClass: longhorn-prod",
		"path: /data",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("pvc App values missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderAppValuesNoVolumeOmitsPvc(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image: "ghcr.io/dada-tuda/api-service:v1", Port: 8080, Replicas: 1, Profile: "small",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "pvc:") {
		t.Errorf("expected no pvc block when no volume set\nFull output:\n%s", got)
	}
}

func TestRenderAppValuesDigest(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image:    "nexus.dada-tuda.ru/ggrk52/magic-mirror@sha256:d1aceff1453361656f36ef154a5d7badead284272986e7d3f8148b360f66d1cb",
		Port:     1488,
		Replicas: 2,
		Profile:  "small",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"name: nexus.dada-tuda.ru/ggrk52/magic-mirror@sha256",
		"tag: d1aceff1453361656f36ef154a5d7badead284272986e7d3f8148b360f66d1cb",
		"servicePort: 1488",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest App values missing %q\nFull output:\n%s", want, got)
		}
	}
}

func TestRenderAppValuesSecretEnv(t *testing.T) {
	got, err := renderer.RenderAppValues(renderer.AppSpec{
		Image:         "ghcr.io/x/y:1",
		Port:          8080,
		Profile:       "small",
		SecretEnvName: "y-env",
		SecretEnvKeys: []string{"API_KEY"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"name: API_KEY",
		"secretKeyRef:",
		"name: y-env",
		"key: API_KEY",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("secret-env App values missing %q\nFull output:\n%s", want, got)
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

func TestRenderDefaultIngress(t *testing.T) {
	spec := renderer.CustomIngressSpec{
		Name:              "myapp-a1b2c3-apps-dada-tuda-ru",
		Namespace:         "delta-prod",
		ProjectSlug:       "delta",
		EnvSlug:           "prod",
		Hostname:          "myapp-a1b2c3.apps.dada-tuda.ru",
		ServiceName:       "myapp-service",
		ServicePortName:   "http",
		OperationID:       "op-ci",
		WildcardTLSSecret: "apps-dada-tuda-wildcard-tls",
		Managed:           true,
	}
	got, err := renderer.RenderCustomIngress(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSubstrings := []string{
		"kind: Ingress",
		"dada.io/managed-domain: \"true\"",
		"host: myapp-a1b2c3.apps.dada-tuda.ru",
		"secretName: apps-dada-tuda-wildcard-tls",
		"name: myapp-service",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered default Ingress missing %q\nFull output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "cert-manager.io/cluster-issuer") {
		t.Errorf("default Ingress must NOT request per-host issuance\nFull output:\n%s", got)
	}
	if strings.Contains(got, "secretName: myapp-a1b2c3-apps-dada-tuda-ru-tls") {
		t.Errorf("default Ingress must use the wildcard secret, not a per-host one\nFull output:\n%s", got)
	}
}

func TestRenderDefaultDomainDNS(t *testing.T) {
	spec := renderer.DefaultDomainDNSSpec{
		Name:        "myapp-a1b2c3-dada-tuda-ru",
		ProjectSlug: "delta",
		EnvSlug:     "prod",
		Hostname:    "myapp-a1b2c3.dada-tuda.ru",
		ServiceName: "myapp-service",
		ServicePort: 8080,
		Target:      "155.212.223.198",
		OperationID: "op-ci",
	}
	got, err := renderer.RenderDefaultDomainDNS(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSubstrings := []string{
		"kind: PublicApi",
		"name: myapp-a1b2c3-dada-tuda-ru",
		"dada.io/managed-domain: \"true\"",
		"gatewayRoute: false",
		"fqdn: myapp-a1b2c3.dada-tuda.ru",
		"recordType: A",
		"target: \"155.212.223.198\"",
		"name: publicapi-beget-dns",
		"servicePort: 8080",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered default-domain DNS missing %q\nFull output:\n%s", want, got)
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
		"environments:",
		"namespace: client-a-prod",
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

// TestChartForAndDefaultPort locks the framework->chart/port mapping the deploy
// path relies on: javascript-family frameworks must select the javascript chart
// (common stack => :5173), never the generic app chart's :8080 default.
func TestChartForAndDefaultPort(t *testing.T) {
	js := []string{"javascript", "web", "nextjs", "nuxt", "sveltekit", "react", "nestjs", "express", "fastify", "remix", "vite", "node"}
	for _, fw := range js {
		if got := renderer.ChartFor(fw); got != "javascript" {
			t.Errorf("ChartFor(%q) = %q, want javascript", fw, got)
		}
		if got := renderer.DefaultPortForFramework(fw); got != 5173 {
			t.Errorf("DefaultPortForFramework(%q) = %d, want 5173", fw, got)
		}
	}
	for _, fw := range []string{"python", "fastapi", "django", "flask"} {
		if got := renderer.ChartFor(fw); got != "python" {
			t.Errorf("ChartFor(%q) = %q, want python", fw, got)
		}
		if got := renderer.DefaultPortForFramework(fw); got != 8080 {
			t.Errorf("DefaultPortForFramework(%q) = %d, want 8080", fw, got)
		}
	}
	for _, fw := range []string{"spring", "spring-gradle", "maven", "gradle"} {
		if got := renderer.ChartFor(fw); got != "spring" {
			t.Errorf("ChartFor(%q) = %q, want spring", fw, got)
		}
	}
	for _, fw := range []string{"", "go", "scala", "static", "dockerfile", "auto"} {
		if got := renderer.ChartFor(fw); got != "app" {
			t.Errorf("ChartFor(%q) = %q, want app", fw, got)
		}
		if got := renderer.DefaultPortForFramework(fw); got != 8080 {
			t.Errorf("DefaultPortForFramework(%q) = %d, want 8080", fw, got)
		}
	}
}
