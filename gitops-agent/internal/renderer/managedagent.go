package renderer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// ManagedAgentToolRef is one MCP server an agent is pointed at.
//
// URL is empty for a shared server that already exists in the runtime: the
// composition then only references it by name instead of declaring a second
// RemoteMCPServer for the same endpoint. Two CRs claiming the same name is not
// a merge, it is a fight, and the loser's tools disappear from the agent that
// declared them.
type ManagedAgentToolRef struct {
	Name           string
	URL            string
	Description    string
	Timeout        string
	AllowedHeaders []string
}

// ManagedAgentEnvVar is one plain environment variable of the agent runtime.
type ManagedAgentEnvVar struct {
	Name  string
	Value string
}

// ManagedAgentSpec holds the parameters of one ManagedAgent claim.
//
// Mirror of the platform.dada-tuda.ru/v1alpha1 ManagedAgent XRD. The claim is
// cluster-scoped: it composes an Agent, its prompt ConfigMap and its
// RemoteMCPServers into the shared agent-runtime namespace, which is why
// Namespace is a spec field rather than metadata.
type ManagedAgentSpec struct {
	Name          string
	Namespace     string
	ProjectSlug   string
	EnvSlug       string
	OperationID   string
	DisplayName   string
	Description   string
	Prompt        string
	PromptVersion string
	ModelConfig   string
	Runtime       string
	Tools         []ManagedAgentToolRef
	Env           []ManagedAgentEnvVar
}

var managedAgentTmpl = template.Must(template.New("managedagent").Funcs(template.FuncMap{
	"indent": indentBlock,
	"quote":  yamlQuote,
}).Parse(`apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ManagedAgent
metadata:
  name: {{ .Name }}
  labels:
    dada.io/project: {{ .ProjectSlug }}
    dada.io/environment: {{ .EnvSlug }}
    dada.io/operation: {{ .OperationID }}
spec:
  namespace: {{ .Namespace }}
  projectRef: {{ .ProjectSlug }}{{ if .DisplayName }}
  displayName: {{ quote .DisplayName }}{{ end }}{{ if .Description }}
  description: {{ quote .Description }}{{ end }}{{ if .PromptVersion }}
  promptVersion: {{ quote .PromptVersion }}{{ end }}{{ if .ModelConfig }}
  modelConfig: {{ .ModelConfig }}{{ end }}{{ if .Runtime }}
  runtime: {{ .Runtime }}{{ end }}
  prompt: |-
{{ indent .Prompt 4 }}{{ if .Tools }}
  tools:
{{- range .Tools }}
    - name: {{ .Name }}{{ if .URL }}
      url: {{ .URL }}{{ end }}{{ if .Description }}
      description: {{ quote .Description }}{{ end }}{{ if .Timeout }}
      timeout: {{ .Timeout }}{{ end }}{{ if .AllowedHeaders }}
      allowedHeaders:
{{- range .AllowedHeaders }}
        - {{ . }}
{{- end }}{{ end }}
{{- end }}{{ end }}{{ if .Env }}
  env:
{{- range .Env }}
    - name: {{ .Name }}
      value: {{ quote .Value }}
{{- end }}{{ end }}
`))

// indentBlock indents every line of a block scalar, including the empty ones.
//
// An empty line left at column zero ends the block and turns the rest of the
// prompt into top-level YAML keys, so a prompt with a blank paragraph break --
// which is every prompt a human writes -- would produce a file that either
// fails to parse or, worse, parses into something else.
func indentBlock(s string, indent int) string {
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// yamlQuote emits a double-quoted YAML scalar, so a colon, a leading dash or a
// "yes" in free text stays the text the user typed.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RenderManagedAgent produces the YAML for one ManagedAgent CR.
func RenderManagedAgent(spec ManagedAgentSpec) (string, error) {
	if spec.Name == "" {
		return "", fmt.Errorf("agent name is required")
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return "", fmt.Errorf("agent %s has an empty prompt: it would start and answer as the bare model", spec.Name)
	}
	var buf bytes.Buffer
	if err := managedAgentTmpl.Execute(&buf, spec); err != nil {
		return "", fmt.Errorf("rendering ManagedAgent: %w", err)
	}
	return buf.String(), nil
}

// ManagedAgentOwnerApp returns the carrier app whose resources.values.yaml
// holds a project's agents. Agents have no workload of their own in the
// project's namespace -- the runtime pods live in the shared agent namespace --
// so they always ride the standalone "agents-<project>" carrier rather than a
// user app.
func ManagedAgentOwnerApp(projectSlug string) string {
	return StandaloneOwnerApp("agents", projectSlug)
}

// ManagedAgentResourcesValuesGitPath returns the resources.values.yaml the
// project's agents live in.
func ManagedAgentResourcesValuesGitPath(projectSlug, envSlug string) string {
	return AppResourcesValuesGitPath(projectSlug, envSlug, ManagedAgentOwnerApp(projectSlug))
}
