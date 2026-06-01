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

// ServiceDatabaseGitPath places the ServiceDatabase manifest inside the owning
// app's Helm chart (chart/templates/), so it is reconciled as part of that app.
func ServiceDatabaseGitPath(projectSlug, envSlug, appRef string) string {
	return AppChartTemplatesGitPath(projectSlug, envSlug, appRef) + "/servicedatabase.yaml"
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
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/application.yaml"
}

func AppHelmChartGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/chart"
}

// AppChartTemplatesGitPath is the templates/ dir of an app's Helm chart. Child
// resources (ServiceDatabase, AIModel, PublicApi) are committed here so they are
// reconciled together with the app that owns them.
func AppChartTemplatesGitPath(projectSlug, envSlug, appName string) string {
	return AppHelmChartGitPath(projectSlug, envSlug, appName) + "/templates"
}

// AppChartYamlGitPath is the Chart.yaml at the root of an app's Helm chart.
func AppChartYamlGitPath(projectSlug, envSlug, appName string) string {
	return AppHelmChartGitPath(projectSlug, envSlug, appName) + "/Chart.yaml"
}

// RenderChartYaml renders a minimal valid Helm Chart.yaml so the app's chart/
// directory is a well-formed chart that the platform controller can render,
// even before any child resource is added under templates/.
func RenderChartYaml(appName string) string {
	return fmt.Sprintf("apiVersion: v2\nname: %s\ndescription: Auto-generated chart for app %s\ntype: application\nversion: 0.1.0\n",
		appName, appName)
}

func AppHelmValuesGitPath(projectSlug, envSlug, appName string) string {
	return AppBaseGitPath(projectSlug, envSlug, appName) + "/values.yaml"
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

// PublicApiGitPath places the PublicApi manifest inside the owning app's Helm
// chart (chart/templates/), alongside the app's other resources.
func PublicApiGitPath(projectSlug, envSlug, appName, publicApiName string) string {
	return AppChartTemplatesGitPath(projectSlug, envSlug, appName) +
		fmt.Sprintf("/publicapi-%s.yaml", publicApiName)
}

func FQDNToName(fqdn string) string {
	return strings.ReplaceAll(fqdn, ".", "-")
}
