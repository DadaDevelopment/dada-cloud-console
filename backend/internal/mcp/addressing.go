package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// addressAliases maps a friendly argument onto the path parameter it fills,
// which is what makes this tool surface addressable by NAME.
//
// The generated tools mirror the REST API, whose resources are addressed by
// UUID: setting one environment variable on internal/prod/telemost-bot meant
// listProjects to find the project, getProject to learn its environment id,
// listApps to learn the app, and only then the write — four calls, three UUIDs,
// one of which returned more than a context window could hold. Names are what
// logs, git and people use; UUIDs are what the API used; the agent spent its
// day stitching the two together, and a mis-stitched environment id writes to
// production instead of staging.
//
// So every tool that takes a projectId/envId/appName path parameter also
// accepts project/env/app names (or a single "project/env/app" ref), and the
// server resolves them before the call goes out. The walk is not made cheaper,
// it is removed.
var addressAliases = map[string]string{
	"project":     "projectId",
	"env":         "envId",
	"environment": "envId",
	"app":         "appName",
}

// addressableParams are the path parameters that a name can stand in for.
var addressableParams = map[string]bool{"projectId": true, "envId": true, "appName": true}

// toolDefaults are arguments applied when the caller left them out.
//
// listApps defaults to the thin view here rather than in the backend: the
// console's own grid needs the full snapshot, and an agent needs a line per
// app. Making the default depend on the caller is the only way both are served.
var toolDefaults = map[string]map[string]any{
	"listApps": {"view": "summary"},
}

// addressingHelp is appended to the description of every tool that accepts
// names, so the model reads the cheap path in the tool list instead of
// discovering it by trial.
const addressingHelp = "\n\nADDRESSING: pass names instead of UUIDs — project, env and app (or ref=\"project/env/app\") are accepted wherever projectId/envId/appName are, and are resolved server-side. There is no need to call listProjects/getProject/listApps first. If the project has exactly one environment, env may be omitted."

// applyAddressing adds the name arguments to a generated tool's input schema
// and drops the id parameters from `required`: an id the caller can supply by
// name is no longer mandatory, and a schema that still demands it forces the
// very walk this exists to remove.
func applyAddressing(g *GeneratedTool) {
	var addressable []string
	for _, p := range g.PathParams {
		if addressableParams[p] {
			addressable = append(addressable, p)
		}
	}
	if len(addressable) == 0 {
		return
	}

	props, _ := g.InputSchema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		g.InputSchema["properties"] = props
	}

	add := func(name, description string) {
		if _, taken := props[name]; taken {
			return
		}
		props[name] = map[string]any{"type": "string", "description": description}
	}
	add("ref", `Address as "project/env/app" (or "project/env"), instead of the UUIDs below.`)
	for _, p := range addressable {
		switch p {
		case "projectId":
			add("project", "Project NAME, resolved server-side (alternative to projectId).")
		case "envId":
			add("env", "Environment NAME, e.g. prod (alternative to envId). May be omitted if the project has one environment.")
		case "appName":
			add("app", "App name (alias of appName).")
		}
	}

	if req, ok := g.InputSchema["required"].([]string); ok {
		kept := req[:0:0]
		for _, r := range req {
			if !addressableParams[r] {
				kept = append(kept, r)
			}
		}
		if len(kept) == 0 {
			delete(g.InputSchema, "required")
		} else {
			g.InputSchema["required"] = kept
		}
	}

	g.Description = strings.TrimSpace(g.Description) + addressingHelp
}

// resolvedRef is the part of the /resolve response this package reads.
type resolvedRef struct {
	Project *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Environment *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"environment"`
	Environments []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"environments"`
}

// resolveAddressArgs rewrites args in place so that the path parameters hold
// UUIDs, whatever the caller wrote. It returns a message for the caller when
// the address cannot be turned into ids; the message names what it could see,
// because "not found" without candidates is what sends an agent back to
// listing everything.
func resolveAddressArgs(ctx context.Context, g GeneratedTool, args map[string]any, backendURL, basePath string) string {
	pathSet := toSet(g.PathParams)
	querySet := toSet(g.QueryParams)

	projectName := argString(args, "project")
	envName := argString(args, "env")
	if envName == "" {
		envName = argString(args, "environment")
	}
	appName := argString(args, "app")

	if ref := argString(args, "ref"); ref != "" {
		parts := splitRef(ref)
		if len(parts) > 0 && projectName == "" {
			projectName = parts[0]
		}
		if len(parts) > 1 && envName == "" {
			envName = parts[1]
		}
		if len(parts) > 2 && appName == "" {
			appName = parts[2]
		}
	}
	for alias := range addressAliases {
		if !pathSet[alias] && !querySet[alias] {
			delete(args, alias)
		}
	}
	if !pathSet["ref"] && !querySet["ref"] {
		delete(args, "ref")
	}

	if pathSet["appName"] && argString(args, "appName") == "" && appName != "" {
		args["appName"] = appName
	}

	needProject := pathSet["projectId"] && !isUUID(argString(args, "projectId"))
	needEnv := pathSet["envId"] && !isUUID(argString(args, "envId"))
	if !needProject && !needEnv {
		return ""
	}

	if projectName == "" {
		projectName = argString(args, "projectId")
	}
	if envName == "" {
		envName = argString(args, "envId")
	}
	if projectName == "" {
		return "missing project: pass project=\"<name>\" (or ref=\"project/env/app\"), or projectId as a UUID"
	}

	ref, msg := resolveByName(ctx, backendURL, basePath, projectName, envName)
	if msg != "" {
		return msg
	}

	if needProject {
		if ref.Project == nil {
			return fmt.Sprintf("could not resolve project %q", projectName)
		}
		args["projectId"] = ref.Project.ID
	}
	if !needEnv {
		return ""
	}
	switch {
	case ref.Environment != nil:
		args["envId"] = ref.Environment.ID
	case len(ref.Environments) == 1:
		args["envId"] = ref.Environments[0].ID
	case len(ref.Environments) == 0:
		return fmt.Sprintf("project %q has no environments", projectName)
	default:
		names := make([]string, 0, len(ref.Environments))
		for _, e := range ref.Environments {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("project %q has several environments — pass env=<one of: %s>",
			projectName, strings.Join(names, ", "))
	}
	return ""
}

// resolveByName asks the backend's own resolve endpoint, with the caller's
// bearer, so name lookup obeys exactly the same visibility rules as every other
// read. It never widens what the caller can reach.
func resolveByName(ctx context.Context, backendURL, basePath, projectName, envName string) (resolvedRef, string) {
	ref := projectName
	if envName != "" {
		ref += "/" + envName
	}
	target := backendURL + basePath + "/resolve?ref=" + url.QueryEscape(ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return resolvedRef{}, fmt.Sprintf("resolve %q: %v", ref, err)
	}
	if bearer := BearerFromContext(ctx); bearer != "" {
		req.Header.Set("Authorization", bearer)
	}

	resp, err := proxyClient.Do(req)
	if err != nil {
		return resolvedRef{}, fmt.Sprintf("resolve %q: backend error (transient), retry: %v", ref, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resolvedRef{}, fmt.Sprintf("could not resolve %q (status %d): %s",
			ref, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out resolvedRef
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return resolvedRef{}, fmt.Sprintf("resolve %q: unreadable response: %v", ref, err)
	}
	return out, ""
}

// applyToolDefaults fills arguments the caller omitted.
func applyToolDefaults(toolName string, args map[string]any) {
	for k, v := range toolDefaults[toolName] {
		if _, given := args[k]; !given {
			args[k] = v
		}
	}
}

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return strings.TrimSpace(s)
}

func splitRef(ref string) []string {
	var parts []string
	for _, p := range strings.Split(strings.Trim(strings.TrimSpace(ref), "/"), "/") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func isUUID(s string) bool {
	if s == "" {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}
