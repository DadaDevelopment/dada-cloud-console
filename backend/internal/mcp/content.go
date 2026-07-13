package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerContent adds MCP prompts and resources to the server. These are the
// non-tool half of the MCP surface: prompts are guided, argument-templated
// runbooks that steer an agent through a multi-step platform flow (deploy,
// configure env, diagnose); resources are read-only reference material (the
// deploy guide and the live OpenAPI spec) an agent can pull for grounding.
//
// Prompts/resources reference the real generated tool names so the model calls
// the right tool at each step. Tool names come from the same GeneratedTool
// slice registered as callable tools, so the runbooks never drift from the API.
func registerContent(srv *sdkmcp.Server, specBytes []byte, tools []GeneratedTool) {
	registerPrompts(srv)
	registerResources(srv, specBytes, tools)
}

// deployAppRunbook is the templated body of the deploy-app prompt. %s slots are
// filled from the prompt arguments.
func promptMsg(text string) *sdkmcp.GetPromptResult {
	return &sdkmcp.GetPromptResult{
		Messages: []*sdkmcp.PromptMessage{
			{Role: "user", Content: &sdkmcp.TextContent{Text: text}},
		},
	}
}

func arg(a map[string]string, k, def string) string {
	if v := strings.TrimSpace(a[k]); v != "" {
		return v
	}
	return def
}

func registerPrompts(srv *sdkmcp.Server) {
	srv.AddPrompt(&sdkmcp.Prompt{
		Name:        "deploy-app",
		Title:       "Deploy an app",
		Description: "Guided flow to deploy a container image as a Helm app and set its environment.",
		Arguments: []*sdkmcp.PromptArgument{
			{Name: "project", Description: "Project slug to deploy into", Required: true},
			{Name: "name", Description: "App name", Required: true},
			{Name: "image", Description: "Container image ref (e.g. registry/app:tag)", Required: false},
			{Name: "port", Description: "Container port the app listens on", Required: false},
		},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		a := req.Params.Arguments
		project := arg(a, "project", "<project>")
		name := arg(a, "name", "<app>")
		image := arg(a, "image", "<image>")
		port := arg(a, "port", "8080")
		return promptMsg(fmt.Sprintf(`Deploy app %q into project %q on DADA Cloud. Work step by step, and stop to report if any step errors.

1. Confirm the project exists: call listProjects and check %q is present. If not, ask before calling createProject.
2. Create the app: call createApp with project=%q, name=%q, image=%q, port=%s. A container app requires image and port.
3. Set environment variables the app needs: call setEnvVar once per variable (project, app, key, value). Secrets are stored encrypted; list current keys with listEnvVars.
4. After env changes, call restartApp so the app picks them up.
5. Verify: poll listApps for project %q until app %q reports phase Healthy (changes apply asynchronously). If it does not become Healthy, call getAppLogs to see why.

Report the final phase.`,
			name, project, project, project, name, image, port, project, name)), nil
	})

	srv.AddPrompt(&sdkmcp.Prompt{
		Name:        "configure-env",
		Title:       "Configure app environment",
		Description: "Set or update environment variables on an existing app and roll them out.",
		Arguments: []*sdkmcp.PromptArgument{
			{Name: "project", Description: "Project slug", Required: true},
			{Name: "app", Description: "App name", Required: true},
			{Name: "vars", Description: "Variables to set, e.g. KEY1=val1, KEY2=val2", Required: false},
		},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		a := req.Params.Arguments
		project := arg(a, "project", "<project>")
		app := arg(a, "app", "<app>")
		vars := arg(a, "vars", "(the variables the user asked for)")
		return promptMsg(fmt.Sprintf(`Configure environment for app %q in project %q on DADA Cloud.

1. Inspect current state: call listEnvVars(project=%q, app=%q) to see which keys exist.
2. Apply changes: for each variable in [%s], call setEnvVar(project=%q, app=%q, key, value). setEnvVar creates or updates one key; values are stored encrypted. To remove a key use deleteEnvVar.
3. Roll out: call restartApp(project=%q, app=%q).
4. Verify: poll listApps until the app is Healthy again; on failure call getAppLogs.

Report which keys changed and the final phase.`,
			app, project, project, app, vars, project, app, project, app)), nil
	})

	srv.AddPrompt(&sdkmcp.Prompt{
		Name:        "diagnose-app",
		Title:       "Diagnose a failing app",
		Description: "Pull status, logs and the latest build for an app and explain what is wrong.",
		Arguments: []*sdkmcp.PromptArgument{
			{Name: "project", Description: "Project slug", Required: true},
			{Name: "app", Description: "App name", Required: true},
		},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		a := req.Params.Arguments
		project := arg(a, "project", "<project>")
		app := arg(a, "app", "<app>")
		return promptMsg(fmt.Sprintf(`Diagnose app %q in project %q on DADA Cloud.

1. Current phase: call listApps(project=%q) and read the phase for %q.
2. Runtime logs: call getAppLogs(project=%q, app=%q) and look for the error.
3. If it looks deploy/build related, call getBuild for the latest build outcome.

Summarize the root cause and the single most likely fix. Do not mutate anything without asking.`,
			app, project, project, app, project, app)), nil
	})
}

func registerResources(srv *sdkmcp.Server, specBytes []byte, tools []GeneratedTool) {
	srv.AddResource(&sdkmcp.Resource{
		URI:         "dada://guide/getting-started",
		Name:        "getting-started",
		Title:       "DADA Cloud — getting started",
		Description: "How the platform models apps, environment variables, domains and async operations.",
		MIMEType:    "text/markdown",
	}, func(_ context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, "text/markdown", gettingStartedGuide), nil
	})

	srv.AddResource(&sdkmcp.Resource{
		URI:         "dada://reference/tools",
		Name:        "tool-index",
		Title:       "Tool index",
		Description: "All MCP tools grouped by resource, with one-line summaries.",
		MIMEType:    "text/markdown",
	}, func(_ context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, "text/markdown", toolIndexMarkdown(tools)), nil
	})

	srv.AddResource(&sdkmcp.Resource{
		URI:         "dada://reference/openapi.json",
		Name:        "openapi",
		Title:       "Backend OpenAPI spec",
		Description: "The backend OpenAPI (Swagger 2.0) document the tools are generated from.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, "application/json", string(specBytes)), nil
	})
}

func textResource(uri, mime, body string) *sdkmcp.ReadResourceResult {
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{
			{URI: uri, MIMEType: mime, Text: body},
		},
	}
}

// toolIndexMarkdown renders the generated tools grouped by their leading verb-less
// noun (best-effort: the OpenAPI tag is not carried on GeneratedTool, so group by
// the trailing resource word of the operation name).
func toolIndexMarkdown(tools []GeneratedTool) string {
	sorted := make([]GeneratedTool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	b.WriteString("# DADA Cloud MCP tools\n\n")
	for _, t := range sorted {
		summary := t.Description
		if i := strings.IndexByte(summary, '\n'); i >= 0 {
			summary = summary[:i]
		}
		b.WriteString(fmt.Sprintf("- **%s** — %s\n", t.Name, summary))
	}
	return b.String()
}

const gettingStartedGuide = `# DADA Cloud — getting started

DADA Cloud is a self-service platform. You (an AI agent) drive it through these MCP tools.
Mutations apply asynchronously; confirm the result by polling the app phase with ` + "`listApps`" + `.

## Core model
- **Project** — the tenant boundary. List with ` + "`listProjects`" + `, create with ` + "`createProject`" + `.
- **App** — a container workload in a project: needs an **image** and a **port**.
  List with ` + "`listApps`" + `, create with ` + "`createApp`" + `.
- **Environment variables** — per app, stored **encrypted**. Manage with
  ` + "`listEnvVars` / `setEnvVar` / `deleteEnvVar`" + `. Changes take effect after ` + "`restartApp`" + `.
- **Databases** — ` + "`createDatabase`" + `, then ` + "`getDatabaseCredentials`" + ` for the connection.
- **Domains** — attach custom hostnames with ` + "`addDomainAuthorization`" + ` then ` + "`verifyDomainAuthorization`" + `.
- **Builds** — ` + "`triggerBuild`" + ` builds from a connected repo; check with ` + "`getBuild`" + `.
- **App servers (VMs)** — ` + "`listAppServers`" + `, ` + "`createAppServer`" + `.

## The golden path (deploy)
1. ` + "`listProjects`" + ` → pick or ` + "`createProject`" + `.
2. ` + "`createApp`" + ` (image + port).
3. ` + "`setEnvVar`" + ` for each variable the app needs.
4. ` + "`restartApp`" + ` so it picks up the env.
5. ` + "`listApps`" + ` → poll until phase is **Healthy**; if it does not get there, ` + "`getAppLogs`" + `.

Mutations are applied asynchronously, so a successful tool call means the change was
accepted, not that the app is up yet — always confirm with ` + "`listApps`" + `.

## Auth
All tool calls carry your DADA ID (id.dada-tuda.ru) bearer token and run under your
own permissions — you can only touch projects your token grants a role on.
`
