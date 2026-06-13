package renderer

import (
	"bytes"
	"fmt"
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
}

func RenderAppValues(spec AppSpec) (string, error) {
	values := AppValuesSpec{
		Image:    spec.Image,
		Port:     spec.Port,
		Replicas: spec.Replicas,
		Profile:  spec.Profile,
	}
	b, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("rendering App values: %w", err)
	}
	return string(b), nil
}

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

// RenderEnvSkeleton returns a minimal .env placeholder for a compose app.
func RenderEnvSkeleton() string {
	return "# Environment variables for this compose app (KEY=VALUE per line).\n"
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
