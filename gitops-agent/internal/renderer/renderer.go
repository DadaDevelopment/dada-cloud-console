package renderer

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ServiceDatabaseSpec holds parameters for a ServiceDatabase manifest.
type ServiceDatabaseSpec struct {
	Name            string
	Namespace       string
	ProjectSlug     string
	EnvSlug         string
	AppRef          string
	Database        string
	BackupEnabled   bool
	BackupSchedule  string
	BackupRetention string
	OperationID     string
}

var serviceDatabaseTmpl = template.Must(template.New("servicedb").Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceDatabaseV2
metadata:
  name: {{ .Name }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  appRef: {{ .AppRef }}
  namespace: {{ .Namespace }}
  engine: postgresql
  database: {{ .Database }}
  backup:
    enabled: {{ .BackupEnabled }}
    frequency: {{ .BackupSchedule }}
    retention: {{ .BackupRetention }}
`))

func RenderServiceDatabase(spec ServiceDatabaseSpec) (string, error) {
	var buf bytes.Buffer
	if err := serviceDatabaseTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering ServiceDatabase: %w", err)
	}
	return buf.String(), nil
}

// StandaloneOwnerApp builds the per-project chart that owns standalone
// (environment-level) resources of one type, e.g. "service-databases-acme".
//
// Why the project slug is baked in: the tenant-apps ApplicationSet names every
// generated ArgoCD Application "<app>-<env>" — the project is NOT part of the
// name. A bare "storage" / "models" chart would therefore collide across any
// two projects sharing an environment (both → "storage-prod"). Embedding the
// project makes "<type>-<project>-<env>" globally unique.
func StandaloneOwnerApp(resourceType, projectSlug string) string {
	return resourceType + "-" + projectSlug
}

// ServiceDatabaseOwnerApp returns the app whose chart owns a database: the
// bound app (appRef) when set, otherwise the per-project standalone
// "service-databases-<project>" chart.
func ServiceDatabaseOwnerApp(appRef, projectSlug string) string {
	if appRef == "" {
		return StandaloneOwnerApp("service-databases", projectSlug)
	}
	return appRef
}

// ServiceDatabaseResourcesValuesGitPath returns the resources.values.yaml of the
// app that owns the database — the bound app (appRef) or, when standalone, the
// shared per-project "service-databases-<project>" app. The CR itself is now an
// entry in that file's manifests: list (keyed by kind+name), not a standalone file.
func ServiceDatabaseResourcesValuesGitPath(projectSlug, envSlug, appRef string) string {
	return AppResourcesValuesGitPath(projectSlug, envSlug, ServiceDatabaseOwnerApp(appRef, projectSlug))
}

// AppSpec holds parameters for an App manifest.
type AppSpec struct {
	Name               string
	Namespace          string
	ProjectSlug        string
	EnvSlug            string
	Image              string
	Port               int
	Replicas           int
	Profile            string
	OperationID        string
	HelmRepoURL        string
	HelmTargetRevision string

	// Env is the resolved non-sensitive runtime environment (env_vars rows with
	// scope runtime|both and is_secret=false). Emitted verbatim into values.yaml's
	// env: block — safe to commit to git.
	Env map[string]string
	// SecretEnvName, when non-empty, is the name of the per-app k8s Secret holding
	// the sensitive runtime env (is_secret=true). The app-resources chart envFrom's
	// it. The Secret manifest itself is rendered separately (RenderAppEnvSecret)
	// into the app's resources.values.yaml.
	SecretEnvName string
}

var appFuncMap = template.FuncMap{
	"appResourcesGitPath":  AppResourcesGitPath,
	"appHelmValuesGitPath": AppHelmValuesGitPath,
}

// NOTE: spec.helm.path still points at the resources/ directory and
// spec.resources: true wires the shared app-resources chart (ADR 0005). The
// per-app chart no longer lives on disk; the ApplicationSet renders the stable
// shared helm/app-resources chart fed by resources.values.yaml
// (ignoreMissingValueFiles: true). The App CR shape here is UNCHANGED.
var appTmpl = template.Must(template.New("app").Funcs(appFuncMap).Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: App
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  namespace: {{ .Namespace }}
  helm:
    repoURL: {{ .HelmRepoURL }}
    path: {{ appResourcesGitPath .ProjectSlug .EnvSlug .Name }}
    targetRevision: {{ .HelmTargetRevision }}
    valueFile: {{ appHelmValuesGitPath .ProjectSlug .EnvSlug .Name }}
`))

func RenderApp(spec AppSpec) (string, error) {
	var buf bytes.Buffer
	if err := appTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering App: %w", err)
	}
	return buf.String(), nil
}

type AppValuesSpec struct {
	Image    string `yaml:"image"`
	Port     int    `yaml:"port"`
	Replicas int    `yaml:"replicas"`
	Profile  string `yaml:"profile"`
	// Env is the non-sensitive runtime environment injected into the workload
	// container (app-resources chart emits `env:` from this map). Omitted when
	// empty to keep diffs minimal for apps with no env vars.
	Env map[string]string `yaml:"env,omitempty"`
	// SecretEnvName references a per-app k8s Secret (rendered into
	// resources.values.yaml) carrying the sensitive runtime env. The chart wires
	// `envFrom: - secretRef: { name: <SecretEnvName> }`. Omitted when there are no
	// sensitive vars.
	SecretEnvName string `yaml:"secretEnvName,omitempty"`
}

func RenderAppValues(spec AppSpec) (string, error) {
	values := AppValuesSpec{
		Image:         spec.Image,
		Port:          spec.Port,
		Replicas:      spec.Replicas,
		Profile:       spec.Profile,
		Env:           spec.Env,
		SecretEnvName: spec.SecretEnvName,
	}
	b, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("rendering App values: %w", err)
	}
	return string(b), nil
}

// AppEnvSecretSpec holds the parameters for a per-app sensitive-env k8s Secret.
type AppEnvSecretSpec struct {
	Name        string
	Namespace   string
	ProjectSlug string
	EnvSlug     string
	OperationID string
	// Data is the plaintext sensitive runtime env (is_secret=true). Rendered into
	// stringData. SECURITY: this commits secret PLAINTEXT to git (the argo-infra
	// repo) because gitops-agent has no kube/SealedSecret channel — see
	// AppEnvSecretName / the renderer package docs. Treat the argo-infra repo as a
	// secret store (restricted access, audit). Replace with SealedSecrets/SOPS or a
	// direct kube-apply when such a channel exists.
	Data map[string]string
}

// RenderAppEnvSecret renders an Opaque k8s Secret CR carrying the app's sensitive
// runtime env. It is upserted into the owning app's resources.values.yaml so
// ArgoCD applies it to the env namespace; the app-resources chart envFrom's it via
// values.secretEnvName.
//
// SECURITY (plaintext-in-git): values land in stringData IN CLEARTEXT in the
// gitops repo. gitops-agent is git-only (no kube client, no SealedSecret CRD in
// cluster), so this is the only available delivery channel today. The argo-infra
// repo MUST be treated as a secret store. The proper fix is SealedSecrets/SOPS.
func RenderAppEnvSecret(spec AppEnvSecretSpec) (string, error) {
	type secretManifest struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   map[string]any    `yaml:"metadata"`
		Type       string            `yaml:"type"`
		StringData map[string]string `yaml:"stringData"`
	}
	m := secretManifest{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: map[string]any{
			"name":      spec.Name,
			"namespace": spec.Namespace,
			"labels": map[string]string{
				"dada.io/project":     spec.ProjectSlug,
				"dada.io/environment": spec.EnvSlug,
				"dada.io/operation":   spec.OperationID,
			},
		},
		Type:       "Opaque",
		StringData: spec.Data,
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("rendering app env Secret: %w", err)
	}
	return string(b), nil
}

// AppEnvSecretName returns the deterministic name of an app's sensitive-env
// Secret. Stable across deploys so envFrom keeps resolving.
func AppEnvSecretName(appName string) string {
	return appName + "-env"
}

// AppServiceName returns the in-cluster Service name the common app subchart
// generates for an app: always "<app>-service". Native manifests (e.g. a
// custom-domain Ingress) must target this — unlike the PublicApi CR, whose
// composition derives the service name itself from the bare app name.
func AppServiceName(appName string) string {
	return appName + "-service"
}

// DefaultAppServicePortName is the name the common app subchart gives the
// Service's single port. Referencing the port by name keeps the Ingress correct
// regardless of the numeric port (5173, 8080, …), which varies per app.
const DefaultAppServicePortName = "http"

func AppBaseGitPath(projectSlug, envSlug, appName string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/apps/%s",
		projectSlug, envSlug, appName)
}

func AppGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/app.yaml"
}

// AppResourcesGitPath is the resources/ value the App CR points its helm.path
// at (kept for the App CR template). Under ADR 0005 the platform controller no
// longer renders a per-app chart from this directory; instead spec.resources
// wires the shared helm/app-resources chart fed by resources.values.yaml. The
// path string is preserved so the App CR shape is unchanged.
func AppResourcesGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/resources"
}

// AppResourcesValuesGitPath is the single resources artifact per app under ADR
// 0005: resources.values.yaml with one top-level "manifests:" list, each entry a
// full platform CR. Replaces the former per-app resources/ Helm chart
// (Chart.yaml + templates/<kind>.yaml). The shared helm/app-resources chart
// renders this file (ignoreMissingValueFiles: true), so an absent file is safe.
func AppResourcesValuesGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/resources.values.yaml"
}

func AppHelmValuesGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/values.yaml"
}

// ── Compose apps (VM runtime) ────────────────────────────────────────────────
// Compose apps live in the same app tree as Helm apps but are deployed to a
// Portainer endpoint (the environment's AppServer) rather than the K8s cluster.
// Portainer pulls compose.yaml from git; a sibling .env is auto-loaded by
// docker compose from the same directory.

// AppComposeGitPath is the docker-compose file for a compose app.
func AppComposeGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/compose.yaml"
}

// AppEnvGitPath is the .env file deployed alongside compose.yaml.
func AppEnvGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/.env"
}

// RenderComposeSkeleton returns a minimal, valid docker-compose file used when a
// compose app is first created. The user edits it via the two-pane editor.
func RenderComposeSkeleton(appName string) string {
	return fmt.Sprintf(`# Auto-created by DADA Console for app %q.
# Edit this file and save to redeploy. A sibling .env is loaded automatically.
services:
  app:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
`, appName)
}

// ImportServiceSpec is one discovered container the caller chose to adopt into
// an imported compose app. Mirrors models.ImportServiceSpec on the backend
// (JSON tags are a hard contract — do not rename without updating both sides).
type ImportServiceSpec struct {
	ContainerName string   `json:"container_name"`
	ServiceName   string   `json:"service_name"`
	Image         string   `json:"image"`
	Ports         []string `json:"ports,omitempty"`
	Volumes       []string `json:"volumes,omitempty"`
	Include       bool     `json:"include"`
}

// isBindMountSource reports whether a volume source (the part before ':' in a
// compose "source:target" mount string) is a bind-mount host path rather than a
// named volume: absolute paths and "./"-relative paths pass through as-is,
// everything else is treated as a named volume that must be pinned external.
func isBindMountSource(source string) bool {
	return strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

// RenderComposeFromDiscovery renders a docker-compose file adopting a
// discovered VM workload: each included service is keyed by its ServiceName
// with image/ports/volumes carried over verbatim, and env_file: [.env] wired
// when env vars were supplied. DATA SAFETY: every named volume referenced by an
// included service is pinned in the top-level volumes: block with
// `external: true` and the literal live name, so the first `docker compose up`
// ATTACHES the existing prod data instead of minting a fresh empty
// `<stack>_<vol>` — the documented PG-data-loss risk (see
// scripts/vm-discover.sh, tasks/vm-gitops-migration-plan.md Phase 4). Bind
// mounts (absolute or ./-relative sources) pass through unchanged; they are not
// named volumes and need no pinning.
func RenderComposeFromDiscovery(services []ImportServiceSpec, hasEnv bool) (string, error) {
	type serviceDoc struct {
		Image   string   `yaml:"image"`
		Restart string   `yaml:"restart,omitempty"`
		Ports   []string `yaml:"ports,omitempty"`
		Volumes []string `yaml:"volumes,omitempty"`
		EnvFile []string `yaml:"env_file,omitempty"`
	}
	type volumeDoc struct {
		External bool   `yaml:"external"`
		Name     string `yaml:"name"`
	}
	type composeDoc struct {
		Services map[string]serviceDoc `yaml:"services"`
		Volumes  map[string]volumeDoc  `yaml:"volumes,omitempty"`
	}

	doc := composeDoc{Services: map[string]serviceDoc{}}
	namedVolumes := map[string]bool{}

	for _, svc := range services {
		if !svc.Include {
			continue
		}
		sd := serviceDoc{
			Image:   svc.Image,
			Restart: "unless-stopped",
			Ports:   svc.Ports,
			Volumes: svc.Volumes,
		}
		if hasEnv {
			sd.EnvFile = []string{".env"}
		}
		doc.Services[svc.ServiceName] = sd

		for _, v := range svc.Volumes {
			source := v
			if idx := strings.Index(v, ":"); idx >= 0 {
				source = v[:idx]
			}
			if source == "" || isBindMountSource(source) {
				continue
			}
			namedVolumes[source] = true
		}
	}

	if len(namedVolumes) > 0 {
		doc.Volumes = make(map[string]volumeDoc, len(namedVolumes))
		names := make([]string, 0, len(namedVolumes))
		for name := range namedVolumes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			doc.Volumes[name] = volumeDoc{External: true, Name: name}
		}
	}

	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("rendering compose from discovery: %w", err)
	}
	return string(b), nil
}

// ComposeAppLabel is the docker label every rendered VM service carries so the
// platform can scope live state, logs and metrics to one first-class
// Application within the shared per-environment stack. The service KEY is also
// the app name, so com.docker.compose.service == this label value.
const ComposeAppLabel = "dada.io/app"

// EnvComposeGitPath is the AGGREGATE docker-compose file for a whole VM
// environment. The AppServer layer assembles every Application in the
// environment into this single file, which Portainer pulls and deploys as ONE
// stack per VM. Each Application keeps its own service fragment
// (AppServiceGitPath) as the durable desired spec; this file is the rendered
// union of all of them. Supersedes the former per-app AppComposeGitPath.
func EnvComposeGitPath(projectSlug, envSlug string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/compose.yaml",
		projectSlug, envSlug)
}

// AppServiceGitPath is the per-Application desired compose service block (one
// service) for a VM app: the durable source of truth for that Application's
// image/ports/volumes. renderEnvAggregate reads every app's fragment to rebuild
// EnvComposeGitPath.
func AppServiceGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/service.yaml"
}

// EnvDotEnvGitPath is the environment-level .env sitting beside the aggregate
// compose.yaml. An ADOPTED stack whose verbatim service blocks reference
// `env_file: [.env]` resolves it here (relative to the aggregate's directory).
func EnvDotEnvGitPath(projectSlug, envSlug string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/.env", projectSlug, envSlug)
}

// AppServiceSpec is one first-class Application's desired compose service. For
// AUTHORED apps (create/import/managed) Image/Ports/Volumes/HasEnv build a
// minimal service block. For ADOPTED apps (an existing hand-authored stack
// migrated into per-service Applications) Service holds the VERBATIM compose
// service block, so the aggregate reproduces the live stack without recreating
// containers — preserving environment/expose/depends_on/bind-mounts and, most
// critically, the exact fields (never dropping data-carrying config).
type AppServiceSpec struct {
	AppName string
	Image   string
	Ports   []string
	Volumes []string
	HasEnv  bool
	Service map[string]any
}

// serviceBlock returns the compose service block for one Application: the
// verbatim adopted block when present, else a minimal authored one. Per-app
// telemetry is scoped by com.docker.compose.service (== the service key == app
// name), so no extra label is stamped — that keeps an adopted block byte-equal
// to the live stack and avoids a config-hash change that would recreate the
// container.
func (s AppServiceSpec) serviceBlock(envFile string) map[string]any {
	if s.Service != nil {
		return s.Service
	}
	b := map[string]any{"image": s.Image, "restart": "unless-stopped"}
	if len(s.Ports) > 0 {
		b["ports"] = s.Ports
	}
	if len(s.Volumes) > 0 {
		b["volumes"] = s.Volumes
	}
	if s.HasEnv {
		b["env_file"] = []string{envFile}
	}
	return b
}

// externalVolumesFor pins every named volume referenced by AUTHORED apps as
// external with its literal name, so a fresh stack never mints an empty
// <stack>_<vol> over existing prod data. Adopted stacks pass their original
// top-level volumes block explicitly instead (external-name mapping preserved),
// so their specs are skipped here. Bind mounts pass through.
func externalVolumesFor(specs []AppServiceSpec) map[string]any {
	named := map[string]bool{}
	for _, spec := range specs {
		if spec.Service != nil {
			continue
		}
		for _, v := range spec.Volumes {
			source := v
			if idx := strings.Index(v, ":"); idx >= 0 {
				source = v[:idx]
			}
			if source == "" || isBindMountSource(source) {
				continue
			}
			named[source] = true
		}
	}
	if len(named) == 0 {
		return nil
	}
	out := make(map[string]any, len(named))
	for name := range named {
		out[name] = map[string]any{"external": true, "name": name}
	}
	return out
}

// RenderAppServiceFragment renders one Application's durable service.yaml: a
// single-service compose document keyed by the app name. env_file is a bare
// ".env" (sibling of the fragment); the aggregate rewrites it to the app's
// per-app .env path when it assembles the stack.
func RenderAppServiceFragment(spec AppServiceSpec) (string, error) {
	doc := map[string]any{
		"services": map[string]any{spec.AppName: spec.serviceBlock(".env")},
	}
	if v := externalVolumesFor([]AppServiceSpec{spec}); v != nil {
		doc["volumes"] = v
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("rendering app service fragment %q: %w", spec.AppName, err)
	}
	return string(b), nil
}

// RenderAggregateCompose assembles N first-class Applications into ONE
// per-environment docker-compose file: one service per app (keyed by app name).
// volumes, when non-nil, is the stack's verbatim top-level volumes block — used
// for ADOPTED stacks to preserve external-volume name mappings exactly (the
// data-safety invariant); when nil, external pins are derived from the authored
// apps' named volumes. Deterministic key order (yaml map encoding is sorted).
func RenderAggregateCompose(specs []AppServiceSpec, volumes map[string]any) (string, error) {
	services := make(map[string]any, len(specs))
	for _, spec := range specs {
		services[spec.AppName] = spec.serviceBlock(fmt.Sprintf("apps/%s/.env", spec.AppName))
	}
	doc := map[string]any{"services": services}
	if volumes == nil {
		volumes = externalVolumesFor(specs)
	}
	if len(volumes) > 0 {
		doc["volumes"] = volumes
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("rendering aggregate compose: %w", err)
	}
	return string(b), nil
}

// RenderEnvSkeleton returns a minimal .env placeholder for a compose app.
func RenderEnvSkeleton() string {
	return "# Environment variables for this compose app (KEY=VALUE per line).\n"
}

// RenderEnvFile renders a docker-compose .env file from resolved env vars
// (scope runtime|both). Keys are emitted in sorted order for deterministic
// diffs. Both sensitive and non-sensitive values are written here — docker
// compose has no out-of-band secret channel, so the .env IS the delivery
// mechanism. SECURITY: sensitive values land in cleartext in the gitops repo;
// the same plaintext-in-git caveat as RenderAppEnvSecret applies (treat the repo
// as a secret store). When env is empty the skeleton placeholder is returned.
func RenderEnvFile(env map[string]string) string {
	if len(env) == 0 {
		return RenderEnvSkeleton()
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Managed by DADA Console — do not edit (regenerated on deploy).\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, env[k])
	}
	return b.String()
}

// RenderBareAppValues renders a minimal values.yaml for an app that was created
// automatically to own a child resource's chart. It declares no workload, so the
// platform controller provisions no Deployment until a user sets an image here.
func RenderBareAppValues() string {
	return "# Auto-created by DADA Console to own this app's chart.\n" +
		"# No workload is deployed until an image is configured here.\n{}\n"
}

// PublicApiSpec holds parameters for a PublicApi manifest.
type PublicApiSpec struct {
	Name           string
	Namespace      string
	ProjectSlug    string
	EnvSlug        string
	ServiceName    string
	ServicePort    int
	FQDN           string
	LBTarget       string
	AuthEnabled    bool
	AuthScheme     string
	AuthScopes     []string
	SwaggerEnabled bool
	SwaggerPath    string
	SwaggerTitle   string
	OperationID    string
}

var publicApiTmpl = template.Must(template.New("publicapi").Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: PublicApi
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  upstream:
    serviceName: {{ .ServiceName }}
    servicePort: {{ .ServicePort }}
  route:
    prefix: /
    pathPattern: /**
    stripPrefix: false
  auth:
    enabled: {{ .AuthEnabled }}
    scheme: {{ .AuthScheme }}{{ if and .AuthEnabled .AuthScopes }}
    scopes:{{ range .AuthScopes }}
      - {{ . }}{{ end }}{{ end }}
  swagger:
    enabled: {{ .SwaggerEnabled }}
    path: {{ .SwaggerPath }}
    title: {{ .SwaggerTitle }}
  dns:
    enabled: true
    fqdn: {{ .FQDN }}
    recordType: A
    target: {{ .LBTarget }}
`))

func RenderPublicApi(spec PublicApiSpec) (string, error) {
	var buf bytes.Buffer
	if err := publicApiTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering PublicApi: %w", err)
	}
	return buf.String(), nil
}

// PublicApiResourcesValuesGitPath returns the resources.values.yaml of the app
// that owns the PublicApi. The CR is an entry in that file's manifests: list
// (keyed by kind+name).
func PublicApiResourcesValuesGitPath(projectSlug, envSlug, appName string) string {
	return AppResourcesValuesGitPath(projectSlug, envSlug, appName)
}

func FQDNToName(fqdn string) string {
	return strings.ReplaceAll(fqdn, ".", "-")
}

// CustomIngressSpec holds parameters for a user-owned custom-domain Ingress.
// Unlike PublicApi (Beget DNS only), this is a plain k8s Ingress that routes a
// hostname the user points at our ingress-nginx-pub LB themselves, with a
// cert-manager (letsencrypt-prod, HTTP-01) per-host TLS cert. No DNS is managed
// by the platform — the user owns their zone.
type CustomIngressSpec struct {
	Name            string // FQDNToName(hostname), the manifest name + TLS secret base
	Namespace       string
	ProjectSlug     string
	EnvSlug         string
	Hostname        string
	ServiceName     string
	ServicePortName string // the Service's named port; the common subchart always uses "http"
	OperationID     string
}

var customIngressTmpl = template.Must(template.New("customingress").Parse(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - {{ .Hostname }}
      secretName: {{ .Name }}-tls
  rules:
    - host: {{ .Hostname }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ .ServiceName }}
                port:
                  name: {{ .ServicePortName }}
`))

// RenderCustomIngress renders a native k8s Ingress (one manifest) for an
// attached custom hostname. It is upserted into the owning app's
// resources.values.yaml manifests list (keyed Ingress/<Name>).
func RenderCustomIngress(spec CustomIngressSpec) (string, error) {
	var buf bytes.Buffer
	if err := customIngressTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering custom Ingress: %w", err)
	}
	return buf.String(), nil
}

// S3BucketSpec holds parameters for an S3Bucket manifest.
type S3BucketSpec struct {
	Name          string
	BucketName    string
	Region        string
	Description   string
	Public        bool
	FtpSftpEnable bool
	ProjectSlug   string
	EnvSlug       string
	OperationID   string
}

var s3BucketTmpl = template.Must(template.New("s3bucket").Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: S3Bucket
metadata:
  name: {{ .Name }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  bucketName: {{ .BucketName }}
  region: {{ .Region }}
  description: {{ .Description | printf "%q" }}
  public: {{ .Public }}
  ftpSftpEnable: {{ .FtpSftpEnable }}
`))

func RenderS3Bucket(spec S3BucketSpec) (string, error) {
	var buf bytes.Buffer
	if err := s3BucketTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering S3Bucket: %w", err)
	}
	return buf.String(), nil
}

// S3BucketOwnerApp returns the app whose chart owns a bucket: the bound app
// (appRef) when set, otherwise the per-project standalone "s3-buckets-<project>"
// chart — buckets created as first-class environment resources, not tied to any
// single app. Apps reference them by endpoint/secret.
func S3BucketOwnerApp(appRef, projectSlug string) string {
	if appRef == "" {
		return StandaloneOwnerApp("s3-buckets", projectSlug)
	}
	return appRef
}

// S3BucketResourcesValuesGitPath returns the resources.values.yaml of the app
// that owns the bucket — the bound app (appRef) or the per-project standalone
// "s3-buckets-<project>" app. The CR is an entry in that file's manifests: list.
func S3BucketResourcesValuesGitPath(projectSlug, envSlug, appRef string) string {
	return AppResourcesValuesGitPath(projectSlug, envSlug, S3BucketOwnerApp(appRef, projectSlug))
}
