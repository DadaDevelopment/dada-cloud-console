package renderer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ServiceDatabaseSpec holds parameters for a ServiceDatabase manifest.
//
// Tier selects the quota class (connection limit + per-role postgres
// parameters) applied by the composition. An empty Tier omits the field so the
// XRD default ("unlimited") applies and already-rendered manifests stay
// byte-for-byte unchanged.
//
// Shard is the Postgres instance the database lives on; it selects the
// provider-sql ProviderConfig used for every object of this database. An empty
// Shard omits the field, so the XRD default (the shared instance) applies and
// databases rendered before shards existed keep their exact placement. Editing
// it on a live database does NOT move the data — that is the documented move
// procedure — so it is written once at creation and carried verbatim after.
type ServiceDatabaseSpec struct {
	Name            string
	Namespace       string
	ProjectSlug     string
	EnvSlug         string
	AppRef          string
	Database        string
	Shard           string
	Tier            string
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
  appRef: {{ if .AppRef }}{{ .AppRef }}{{ else }}{{ .Name }}{{ end }}
  namespace: {{ .Namespace }}
  engine: postgresql
  database: {{ .Database }}
{{- if .Shard }}
  shard: {{ .Shard }}
{{- end }}
{{- if .Tier }}
  tier: {{ .Tier }}
{{- end }}
  backup:
    enabled: {{ .BackupEnabled }}
    frequency: {{ .BackupSchedule | printf "%q" }}
    retention: {{ .BackupRetention | printf "%q" }}
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
	Resources          *AppResources
	OperationID        string
	HelmRepoURL        string
	HelmTargetRevision string
	Framework          string
	SecretEnvKeys      []string

	// Env is the resolved non-sensitive runtime environment (env_vars rows with
	// scope runtime|both and is_secret=false). Emitted verbatim into values.yaml's
	// env: block — safe to commit to git.
	Env map[string]string
	// SecretEnvName, when non-empty, is the name of the per-app k8s Secret holding
	// the sensitive runtime env (is_secret=true). The app-resources chart envFrom's
	// it. The Secret manifest itself is rendered separately (RenderAppEnvSecret)
	// into the app's resources.values.yaml.
	SecretEnvName string

	// VolumePath, when non-empty, requests a persistent data directory for the app.
	// It renders a common.pvc block in values.yaml that the workload chart turns into
	// a ReadWriteMany PersistentVolumeClaim mounted at VolumePath on every replica.
	// RWX is deliberate: a single shared volume across all pods removes the single-
	// replica constraint of ReadWriteOnce. VolumeSize is a k8s quantity (e.g. "5Gi").
	VolumePath         string
	VolumeSize         string
	VolumeStorageClass string
	VolumeFSGroup      int64
	WorkloadType       string

	// ResourcesOnly marks a resources-carrier owner app (no workload of its own):
	// the per-project "service-databases-<project>" / "s3-buckets-<project>" charts
	// that exist only to own standalone sibling CRs. Such an app has no OCI/git
	// workload chart, so its App CR must point spec.helm.path directly at the shared
	// passthrough chart "helm/app-resources" fed by ResourcesValueFile — NOT at the
	// per-app "<app>/resources" directory, which no longer exists on disk (ADR 0005)
	// and makes ArgoCD fail with "app path does not exist".
	ResourcesOnly bool
	// ResourcesValueFile is the git path of the resources.values.yaml the passthrough
	// chart renders. Required when ResourcesOnly is set.
	ResourcesValueFile string

	// ArgoName, when non-empty, is the explicit ArgoCD Application name the
	// tenant-apps ApplicationSet must use for this app (via spec.argoName, with a
	// fallback to the legacy "<app>-<env>"). It carries the project identity so two
	// projects can share an app name in the same environment without colliding into
	// one Application. It is stored per-app (resource_snapshots.summary_json) and
	// re-used verbatim on every re-render — NEVER recomputed from the app name — so
	// apps created before this field existed keep their bare "<app>-<env>" name and
	// are never renamed. See ScopedArgoName.
	ArgoName string
}

// ScopedArgoName builds a collision-free ArgoCD Application name for a NEW app:
// "<app>-<env>-<projhash>" where projhash is the first 4 bytes of sha256(projectID)
// as hex. The project hash guarantees two different projects never produce the same
// Application name for the same (app, env), while a stable input keeps re-renders
// idempotent. The result is a valid RFC1123 label (lowercase alnum + hyphens, <=63).
// It is deliberately distinct from the legacy bare "<app>-<env>" scheme, so a new
// app can never collide with an existing bare-named one either.
func ScopedArgoName(app, env, projectID string) string {
	sum := sha256.Sum256([]byte(projectID))
	suffix := fmt.Sprintf("%s-%x", env, sum[:4])
	if max := 63 - len(suffix) - 1; len(app) > max && max > 0 {
		app = app[:max]
	}
	return app + "-" + suffix
}

const (
	WorkloadRepoURL = "https://bitbucket.dada-tuda.ru/scm/dada/dada-argo.git"
	WorkloadBranch  = "develop"
)

func ChartFor(framework string) string {
	switch framework {
	case "python", "fastapi", "django", "flask":
		return "python"
	case "javascript", "web", "nextjs", "nuxt", "sveltekit", "react",
		"nestjs", "express", "fastify", "remix", "vite", "node":
		return "javascript"
	case "spring", "spring-maven", "spring-gradle", "maven", "gradle":
		return "spring"
	default:
		return "app"
	}
}

// DefaultPortForFramework is the port a framework's dev/serve process listens on
// when no explicit port was captured, mirroring the helm common chart's
// stack-based default ($defaultServicePort: javascript => 5173, else 8080).
// Used so a deploy that lost its detected port does not blindly pin 8080 on a
// javascript app that actually serves 5173.
func DefaultPortForFramework(framework string) int {
	if ChartFor(framework) == "javascript" {
		return 5173
	}
	return 8080
}

var appFuncMap = template.FuncMap{
	"chartFor":        ChartFor,
	"workloadRepoURL": func() string { return WorkloadRepoURL },
	"workloadBranch":  func() string { return WorkloadBranch },
}

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
{{- if .ArgoName }}
  argoName: {{ .ArgoName }}
{{- end }}
{{- if not .ResourcesOnly }}
  resources: true
{{- end }}
  namespace: {{ .Namespace }}
  helm:
{{- if .ResourcesOnly }}
    repoURL: {{ .HelmRepoURL }}
    path: helm/app-resources
    targetRevision: {{ .HelmTargetRevision }}
    valueFile: {{ .ResourcesValueFile }}
{{- else }}
    repoURL: {{ workloadRepoURL }}
    path: helm/{{ chartFor .Framework }}
    targetRevision: {{ workloadBranch }}
    releaseName: {{ .Name }}
{{- end }}
`))

func RenderApp(spec AppSpec) (string, error) {
	var buf bytes.Buffer
	if err := appTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering App: %w", err)
	}
	return buf.String(), nil
}

type commonImage struct {
	Name string `yaml:"name,omitempty"`
	Tag  string `yaml:"tag,omitempty"`
}

type commonSecretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type commonEnvValueRef struct {
	SecretKeyRef commonSecretKeyRef `yaml:"secretKeyRef"`
}

type commonEnvVar struct {
	Name      string             `yaml:"name"`
	Value     string             `yaml:"value,omitempty"`
	ValueFrom *commonEnvValueRef `yaml:"valueFrom,omitempty"`
}

type commonResources struct {
	Requests map[string]string `yaml:"requests"`
	Limits   map[string]string `yaml:"limits"`
}

// commonPvc mirrors the optional pvc: block the common chart detects via
// hasKey .Values "pvc". AccessMode is left empty so the chart's ReadWriteMany
// default applies.
type commonPvc struct {
	Size         string `yaml:"size"`
	StorageClass string `yaml:"storageClass"`
	AccessMode   string `yaml:"accessMode,omitempty"`
	Path         string `yaml:"path"`
}

// commonService carries the public-service contract into the shared Helm
// chart. It is deliberately independent from workload type: a configured port
// means an HTTP service; no port means the chart must emit neither Service nor
// Ingress nor default HTTP probes.
type commonService struct {
	Enabled bool `yaml:"enabled"`
}

type commonValues struct {
	Image        commonImage     `yaml:"image"`
	Service      commonService   `yaml:"service"`
	ServicePort  int             `yaml:"servicePort,omitempty"`
	Replicas     int             `yaml:"replicas,omitempty"`
	UseDotEnv    string          `yaml:"useDotEnv"`
	Resources    commonResources `yaml:"resources"`
	ExtraEnv     []commonEnvVar  `yaml:"extraEnv,omitempty"`
	Pvc          *commonPvc      `yaml:"pvc,omitempty"`
	WorkloadType string          `yaml:"workloadType,omitempty"`

	PodSecurityContext *commonPodSecurityContext `yaml:"podSecurityContext,omitempty"`
}

// commonPodSecurityContext hands a persistent volume to a non-root image.
//
// A Longhorn volume arrives owned by root:root, so an image that drops to its
// own user (grafana 472, node 1000) cannot write to the data directory it was
// just given and crash-loops on first start with a permission error that reads
// like an application bug. fsGroup makes the kubelet chown the mount to that
// group, which is the only place this can be fixed: the image is upstream's and
// the volume is created empty by the CSI driver.
type commonPodSecurityContext struct {
	FSGroup int64 `yaml:"fsGroup"`
}

type appValuesFile struct {
	Common commonValues `yaml:"common"`
}

// AppResources is an explicit per-app resource envelope in Kubernetes quantity
// notation ("250m", "1", "512Mi", "2Gi").
//
// It supersedes the small/medium/large profile: a preset ladder cannot express
// what a build container, an ML worker or a video transcoder actually needs,
// and its top rung doubles as a platform-wide ceiling that no app can pass.
// Profile stays only as the fallback for snapshots written before this field
// existed.
// The json tags are the on-disk contract: this is exactly how the console
// writes the envelope into resource_snapshots.summary_json["resources"].
// EphemeralRequest and EphemeralLimit are optional and are carried through
// verbatim rather than being sized by anything. They exist because a render
// that omits them deletes an ephemeral-storage limit an app was given by hand,
// and a container that then writes past the node default is evicted.
type AppResources struct {
	CPURequest       string `json:"cpu_request"`
	MemoryRequest    string `json:"memory_request"`
	CPULimit         string `json:"cpu_limit"`
	MemoryLimit      string `json:"memory_limit"`
	EphemeralRequest string `json:"ephemeral_request,omitempty"`
	EphemeralLimit   string `json:"ephemeral_limit,omitempty"`
}

// Complete reports whether every field is set. A partially filled envelope is
// treated as absent rather than merged with the profile defaults: a half-known
// envelope is far more likely to be a snapshot the console wrote wrong than a
// deliberate request, and merging it would silently shrink whichever dimension
// went missing.
func (r *AppResources) Complete() bool {
	return r != nil &&
		r.CPURequest != "" && r.MemoryRequest != "" &&
		r.CPULimit != "" && r.MemoryLimit != ""
}

// resolveResources prefers an explicit envelope and falls back to the profile
// ladder for apps that have never been sized.
func resolveResources(r *AppResources, profile string) commonResources {
	out := profileResources(profile)
	if r.Complete() {
		out = commonResources{
			Requests: map[string]string{"cpu": r.CPURequest, "memory": r.MemoryRequest},
			Limits:   map[string]string{"cpu": r.CPULimit, "memory": r.MemoryLimit},
		}
	}
	if r != nil {
		if r.EphemeralRequest != "" {
			out.Requests["ephemeral-storage"] = r.EphemeralRequest
		}
		if r.EphemeralLimit != "" {
			out.Limits["ephemeral-storage"] = r.EphemeralLimit
		}
	}
	return out
}

func profileResources(profile string) commonResources {
	switch profile {
	case "medium":
		return commonResources{
			Requests: map[string]string{"cpu": "100m", "memory": "256Mi"},
			Limits:   map[string]string{"cpu": "500m", "memory": "512Mi"},
		}
	case "large":
		return commonResources{
			Requests: map[string]string{"cpu": "250m", "memory": "512Mi"},
			Limits:   map[string]string{"cpu": "1", "memory": "1Gi"},
		}
	default:
		return commonResources{
			Requests: map[string]string{"cpu": "10m", "memory": "128Mi"},
			Limits:   map[string]string{"cpu": "250m", "memory": "256Mi"},
		}
	}
}

func splitImageRef(image string) (name, tag string) {
	if at := strings.LastIndex(image, "@"); at >= 0 {
		digest := image[at+1:]
		if colon := strings.Index(digest, ":"); colon >= 0 {
			return image[:at] + "@" + digest[:colon], digest[colon+1:]
		}
		return image[:at], digest
	}
	if colon := strings.LastIndex(image, ":"); colon >= 0 && !strings.Contains(image[colon:], "/") {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

func RenderAppValues(spec AppSpec) (string, error) {
	name, tag := splitImageRef(spec.Image)
	values := appValuesFile{Common: commonValues{
		Image:        commonImage{Name: name, Tag: tag},
		Service:      commonService{Enabled: spec.Port > 0},
		ServicePort:  spec.Port,
		Replicas:     spec.Replicas,
		UseDotEnv:    "false",
		Resources:    resolveResources(spec.Resources, spec.Profile),
		WorkloadType: spec.WorkloadType,
	}}

	if spec.VolumePath != "" {
		values.Common.Pvc = &commonPvc{
			Size:         spec.VolumeSize,
			StorageClass: spec.VolumeStorageClass,
			Path:         spec.VolumePath,
		}
		if spec.VolumeFSGroup > 0 {
			values.Common.PodSecurityContext = &commonPodSecurityContext{FSGroup: spec.VolumeFSGroup}
		}
	}

	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		values.Common.ExtraEnv = append(values.Common.ExtraEnv, commonEnvVar{Name: k, Value: spec.Env[k]})
	}

	secretKeys := append([]string(nil), spec.SecretEnvKeys...)
	sort.Strings(secretKeys)
	for _, k := range secretKeys {
		values.Common.ExtraEnv = append(values.Common.ExtraEnv, commonEnvVar{
			Name:      k,
			ValueFrom: &commonEnvValueRef{SecretKeyRef: commonSecretKeyRef{Name: spec.SecretEnvName, Key: k}},
		})
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

// EnvBaseGitPath is the root of everything an environment owns in git: app
// folders, standalone resource manifests, compose/env files. The orphan GC
// scans this whole subtree when testing whether a child resource is still
// git-backed, so it must stay the common ancestor of every per-env path below.
func EnvBaseGitPath(projectSlug, envSlug string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s",
		projectSlug, envSlug)
}

func AppBaseGitPath(projectSlug, envSlug, appName string) string {
	return fmt.Sprintf("%s/apps/%s", EnvBaseGitPath(projectSlug, envSlug), appName)
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

// AuthoredNamedVolumes returns the named volumes the aggregate will pin
// `external: true` for AUTHORED apps, sorted for a deterministic payload.
//
// Adopted stacks are skipped for the same reason externalVolumesFor skips them:
// their volumes already exist on the machine (that is what adoption means), and
// their original top-level volumes block carries an external-NAME mapping, so a
// creation attempt here would invent a volume under the wrong name.
//
// The deploy worker uses this to create missing volumes before the stack is
// deployed. It has to come from the renderer rather than be re-derived from the
// compose file, because the renderer is what decided which volumes are external
// in the first place — two independent derivations would eventually disagree,
// and the disagreement would surface as a failed deploy on a stateful app.
func AuthoredNamedVolumes(specs []AppServiceSpec) []string {
	vols := externalVolumesFor(specs)
	if len(vols) == 0 {
		return nil
	}
	out := make([]string, 0, len(vols))
	for name := range vols {
		out = append(out, name)
	}
	sort.Strings(out)
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
//
// Each `$` in a value is doubled to `$$`. Compose v2 interpolates env_file
// values by default, so a bare `$` would be read as a variable reference and the
// value silently truncated (a password `ab$cd` → `ab`, and a `$`-leading secret
// → empty). Proven on the findata edge endpoint: `ab$cd12$xy` arrives as `ab`
// unescaped, and correctly as `ab$cd12$xy` once doubled. `$$` is the compose
// literal-$ escape and de-escapes back to a single `$` in the container.
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
		fmt.Fprintf(&b, "%s=%s\n", k, strings.ReplaceAll(env[k], "$", "$$"))
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

// FQDNToName turns a hostname into an RFC 1123 resource name by replacing dots
// with dashes. The input is lowercased and stripped of surrounding whitespace
// and dots first: a canonical FQDN carries a trailing dot ("ggrk52.ru."), and
// replacing that dot verbatim would yield a trailing dash ("ggrk52-ru-") that
// k8s rejects as an invalid metadata.name.
func FQDNToName(fqdn string) string {
	fqdn = strings.Trim(strings.ToLower(strings.TrimSpace(fqdn)), ".")
	return strings.ReplaceAll(fqdn, ".", "-")
}

// CustomIngressSpec holds parameters for a user-owned custom-domain Ingress.
// Unlike PublicApi (Beget DNS only), this is a plain k8s Ingress that routes a
// hostname the user points at our ingress-nginx-pub LB themselves, with a
// cert-manager (letsencrypt-prod, HTTP-01) per-host TLS cert. No DNS is managed
// by the platform — the user owns their zone.
type CustomIngressSpec struct {
	Name              string // FQDNToName(hostname), the manifest name + TLS secret base
	Namespace         string
	ProjectSlug       string
	EnvSlug           string
	Hostname          string
	ServiceName       string
	ServicePortName   string
	OperationID       string
	WildcardTLSSecret string
	Managed           bool
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
{{- if .Managed }}
    dada.io/managed-domain: "true"
{{- end }}
{{- if not .WildcardTLSSecret }}
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
{{- end }}
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - {{ .Hostname }}
      secretName: {{ if .WildcardTLSSecret }}{{ .WildcardTLSSecret }}{{ else }}{{ .Name }}-tls{{ end }}
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

// DefaultDomainDNSSpec holds parameters for the platform-owned A record that
// backs a managed default (surrogate) hostname under our own base zone.
type DefaultDomainDNSSpec struct {
	Name        string
	ProjectSlug string
	EnvSlug     string
	Hostname    string
	ServiceName string
	ServicePort int
	Target      string
	OperationID string
}

var defaultDomainDNSTmpl = template.Must(template.New("defaultdomaindns").Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: PublicApi
metadata:
  name: {{ .Name }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
    dada.io/managed-domain: "true"
spec:
  gatewayRoute: false
  upstream:
    serviceName: {{ .ServiceName }}
    servicePort: {{ .ServicePort }}
  route:
    prefix: /
    stripPrefix: false
  dns:
    enabled: true
    fqdn: {{ .Hostname }}
    recordType: A
    target: {{ .Target | printf "%q" }}
  crossplane:
    compositionRef:
      name: publicapi-beget-dns
`))

// RenderDefaultDomainDNS renders a DNS-only PublicApi composite that publishes
// the A record for a managed default hostname into our Beget zone via the
// publicapi-beget-dns composition. gatewayRoute is false: the surrogate host is
// served by the plain Ingress rendered separately, so this composite only owns
// the DNS record. It is upserted into the owning app's resources.values.yaml
// (keyed PublicApi/<Name>) so its lifecycle follows the app.
func RenderDefaultDomainDNS(spec DefaultDomainDNSSpec) (string, error) {
	var buf bytes.Buffer
	if err := defaultDomainDNSTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering default-domain DNS: %w", err)
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

// maxS3BucketDescriptionLen mirrors the upstream Beget provider limit:
// beget_s3_bucket.description rejects more than 45 characters. A longer value
// does not fail the render — it strands the Terraform workspace in
// ReconcileError, so the bucket never provisions and its credentials never
// appear. Truncating here keeps a legacy or hand-edited snapshot from wedging
// a bucket; the console rejects over-long input at the API boundary.
const maxS3BucketDescriptionLen = 45

// s3BucketDescriptionAllowedExtra lists the punctuation Beget accepts in
// beget_s3_bucket.description beyond Unicode letters, digits and space.
// There is no published character-set spec from Beget for this field; this
// set was derived empirically on 2026-08-03 from a live incident where user
// artemmendeleev's bucket create sat in ReconcileError for 72 minutes
// because the description "Cold storage: Fonbet raw bodies offloaded"
// (43 runes, under the length cap) was silently rejected for its colon.
// Keep this in sync with the validator in
// backend/internal/api/s3buckets.go (validateS3BucketDescriptionCharset).
const s3BucketDescriptionAllowedExtra = ".,_-"

// stripS3BucketDescriptionCharset removes runes outside Unicode letters,
// digits, space and s3BucketDescriptionAllowedExtra. A legacy or hand-edited
// snapshot can carry a character Beget rejects; the render must not fail on
// it, since a failing render stalls the whole deploy, so this strips rather
// than errors. The console rejects the same characters at the API boundary
// so a fresh create never reaches here with an invalid description.
func stripS3BucketDescriptionCharset(desc string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(s3BucketDescriptionAllowedExtra, r) {
			return r
		}
		return -1
	}, desc)
}

func RenderS3Bucket(spec S3BucketSpec) (string, error) {
	spec.Description = stripS3BucketDescriptionCharset(spec.Description)
	if utf8.RuneCountInString(spec.Description) > maxS3BucketDescriptionLen {
		spec.Description = string([]rune(spec.Description)[:maxS3BucketDescriptionLen])
	}
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
