package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dada-tuda/console/backend/internal/llmchat"
	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var keepTools = []string{
	"listProjects", "getProject",
	"listApps", "getAppState", "getAppLogs", "getAppMetrics",
	"listDeployments", "listBuilds", "getBuild",
	"listEnvVars", "listHostnames", "listEndpoints", "listDatabases",
	"listOperations", "getOperation",
	"searchLogs", "getProjectCost", "getCurrentUser",
	"submitFeedback",
	"deleteAppImpact", "deleteProjectImpact", "moveAppImpact",
}

var writeKeepTools = []string{
	"restartApp", "triggerBuild", "deployTrigger", "cancelBuild", "retryOperation",
	"setEnvVar", "deleteEnvVar",
	"rollbackApp", "rollbackDeployment", "promoteDeployment", "updateAppImage",
	"updateAppProfile", "updateAppStorage",
	"createDatabase",
}

var denyTools = map[string]bool{
	"revealEnvVar":           true,
	"getDatabaseCredentials": true,
	"getS3BucketCredentials": true,
	"revealModelApiKey":      true,
}

const SupportTicketTool = "create_support_ticket"

const supportTicketRoute = "agent-chat"

type Toolset struct {
	Defs     []llmchat.ToolDef
	handlers map[string]internalmcp.ToolHandler
	writeSet map[string]bool
}

func BuildToolset(specBytes []byte, backendURL string) (*Toolset, error) {
	spec, err := internalmcp.ParseSpec(specBytes)
	if err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}

	all := internalmcp.GenerateTools(spec)
	ov := &internalmcp.Overrides{
		Keep:   append(append([]string{}, keepTools...), writeKeepTools...),
		Rename: map[string]string{"submitFeedback": SupportTicketTool},
	}
	curated := internalmcp.ApplyOverrides(all, ov)

	writeSet := make(map[string]bool, len(writeKeepTools))
	for _, name := range writeKeepTools {
		writeSet[name] = true
	}

	ts := &Toolset{handlers: map[string]internalmcp.ToolHandler{}, writeSet: writeSet}
	for _, t := range curated {
		if denyTools[t.Name] {
			continue
		}
		ts.Defs = append(ts.Defs, llmchat.ToolDef{
			Type: "function",
			Function: llmchat.ToolFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
		ts.handlers[t.Name] = internalmcp.MakeHandler(t, backendURL, spec.BasePath)
	}
	return ts, nil
}

func (ts *Toolset) Has(name string) bool {
	_, ok := ts.handlers[name]
	return ok
}

func (ts *Toolset) IsWrite(name string) bool {
	return ts.writeSet[name]
}

func (ts *Toolset) Execute(ctx context.Context, bearer, name, argsJSON string) (text string, isError bool) {
	handler, ok := ts.handlers[name]
	if !ok {
		return fmt.Sprintf("unknown tool %q", name), true
	}

	if name == SupportTicketTool {
		argsJSON = forceSupportTicketRoute(argsJSON)
	}

	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}

	toolCtx := internalmcp.WithBearer(ctx, bearer)
	req := &sdkmcp.CallToolRequest{
		Params: &sdkmcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(argsJSON)},
	}
	res, err := handler(toolCtx, req)
	if err != nil {
		return fmt.Sprintf("tool error: %v", err), true
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func forceSupportTicketRoute(argsJSON string) string {
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}
	args["route"] = supportTicketRoute
	b, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(b)
}
