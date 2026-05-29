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
kind: ServiceDatabase
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  appRef: {{ .AppRef }}
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

func ServiceDatabaseGitPath(projectSlug, envSlug, appRef string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/apps/%s/database.yaml",
		projectSlug, envSlug, appRef)
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
	"appHelmChartGitPath":  AppHelmChartGitPath,
	"appHelmValuesGitPath": AppHelmValuesGitPath,
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
  namespace: {{ .Namespace }}
  helm:
    repoURL: {{ .HelmRepoURL }}
    path: {{ appHelmChartGitPath .ProjectSlug .EnvSlug .Name }}
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

func AppHelmChartGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/chart"
}

func AppHelmValuesGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/values.yaml"
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

func PublicApiGitPath(projectSlug, envSlug, appName, publicApiName string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/apps/%s/publicapi-%s.yaml",
		projectSlug, envSlug, appName, publicApiName)
}

func FQDNToName(fqdn string) string {
	return strings.ReplaceAll(fqdn, ".", "-")
}
