package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxPreflightToolCalls = 3

const preflightListProjectsTool = "listProjects"

const preflightGetProjectTool = "getProject"

const preflightListAppsTool = "listApps"

const inventoryHeader = "INVENTORY (already looked up for you, do not re-query): "

const inventoryNoAppsMarker = "apps=[] -- THE USER HAS NOTHING DEPLOYED YET."

const inventoryNoAppsInstruction = " Do not ask which application the user means -- there is none. Do not ask which project either. Say plainly that nothing is deployed yet, then give one concrete first step to deploy (connect a GitHub repository, deploy a Docker image, or upload an archive) together with the console path for it."

const inventoryNoProjectsMarker = "projects=[] -- THE USER HAS NO PROJECTS."

const inventoryAppsUnknown = "apps=<not looked up: no single project/environment resolved>"

const inventoryAppsUnreadable = "apps=<UNKNOWN: listApps ran but its result could not be read (likely truncated), so the engine learned nothing about the applications>. Do NOT assume anything about what is or is not deployed. If the answer depends on which applications exist, call listApps yourself first."

const inventoryProjectsUnreadable = "projects=<UNKNOWN: listProjects ran but its result could not be read>. Do NOT assume the user has no projects. Call listProjects yourself if you need them."

const inventoryMaxProjects = 20

const inventoryMaxApps = 30

// TurnContext is the console context the frontend sends along with a chat turn
// (POST /agent/chat): the project/environment/app page the user is sitting on.
// A non-empty AppName means the turn is already grounded on a concrete app, so
// the inventory preflight is skipped -- there is nothing left to disambiguate.
// SkipPreflight is the caller-side kill switch for the preflight lookups.
type TurnContext struct {
	ProjectID     string
	EnvID         string
	AppName       string
	SkipPreflight bool
}

// InventoryProject is one project as seen by the preflight listProjects call.
type InventoryProject struct {
	ID                 string
	Name               string
	DefaultEnvironment string
}

// InventoryApp is one app as seen by the preflight listApps call.
type InventoryApp struct {
	Name  string
	Phase string
}

// Inventory is what the engine saw on its own before the first LLM call.
// AppsLookedUp separates "the user has zero apps" from "listApps never ran or
// its result was unreadable" -- without it the engine cannot honestly tell the
// model that nothing is deployed. ProjectsLookedUp is the same guarantee for
// listProjects. Both are false unless the corresponding tool result parsed
// cleanly, so a truncated payload is reported as unknown, never as empty.
type Inventory struct {
	Projects         []InventoryProject
	Apps             []InventoryApp
	ProjectID        string
	EnvID            string
	EnvName          string
	ProjectsLookedUp bool
	AppsLookedUp     bool
}

func preflightExecute(ctx context.Context, tools *ToolView, bearer string, name, argsJSON string) (string, ToolLogEntry) {
	started := time.Now()
	text, isError := tools.Execute(ctx, bearer, name, argsJSON)
	return text, ToolLogEntry{
		Name:       name,
		ArgsJSON:   argsJSON,
		Result:     text,
		IsError:    isError,
		DurationMs: time.Since(started).Milliseconds(),
		Preflight:  true,
	}
}

// runInventoryPreflight grounds the turn before the model gets a say: it runs
// listProjects, then getProject (only when the turn context carries no envId),
// then listApps, and returns what it found plus the tool log of those calls.
//
// Preflight spends none of MaxToolCallsPerTurn -- it is the engine grounding
// itself, not the model burning its budget -- and is separately capped at
// maxPreflightToolCalls calls. Its entries land in the tool log (marked
// Preflight) so the eval trace still shows that listProjects/listApps really
// ran, but it emits no tool-call chips: the user asked a question, not for
// three lookups, and an explanatory turn should not flash calls at them. Any
// tool error aborts the chain and yields whatever was learned so far; it never
// fails the turn. A result that does not parse leaves the corresponding
// LookedUp flag false, so an unreadable payload is never mistaken for "empty".
//
// The emit parameter is deliberately unused; it stays in the signature because
// callers pass the turn emitter to every engine step.
func runInventoryPreflight(ctx context.Context, tools *ToolView, bearer string, turnCtx TurnContext, _ Emitter) (*Inventory, []ToolLogEntry) {
	if turnCtx.SkipPreflight || strings.TrimSpace(turnCtx.AppName) != "" || tools == nil {
		return nil, nil
	}

	var log []ToolLogEntry

	projectsText, projectsEntry := preflightExecute(ctx, tools, bearer, preflightListProjectsTool, "{}")
	log = append(log, projectsEntry)
	if projectsEntry.IsError {
		return nil, log
	}

	projects, projectsOK := parseInventoryProjects(projectsText)
	inv := &Inventory{Projects: projects, ProjectsLookedUp: projectsOK}

	projectID := strings.TrimSpace(turnCtx.ProjectID)
	if projectID == "" && len(inv.Projects) == 1 {
		projectID = inv.Projects[0].ID
	}
	if projectID == "" {
		return inv, log
	}

	inv.ProjectID = projectID
	envID := strings.TrimSpace(turnCtx.EnvID)

	if envID == "" {
		projectText, projectEntry := preflightExecute(ctx, tools, bearer, preflightGetProjectTool, fmt.Sprintf(`{"projectId":%q}`, projectID))
		log = append(log, projectEntry)
		if projectEntry.IsError {
			return inv, log
		}
		envID, inv.EnvName = pickInventoryEnv(projectText)
	}

	if envID == "" {
		return inv, log
	}

	inv.EnvID = envID
	appsText, appsEntry := preflightExecute(ctx, tools, bearer, preflightListAppsTool, fmt.Sprintf(`{"projectId":%q,"envId":%q}`, projectID, envID))
	log = append(log, appsEntry)
	if appsEntry.IsError {
		return inv, log
	}
	apps, appsOK := parseInventoryApps(appsText)
	if appsOK {
		inv.Apps = apps
		inv.AppsLookedUp = true
	}

	if len(log) > maxPreflightToolCalls {
		log = log[:maxPreflightToolCalls]
	}
	return inv, log
}

// parseInventoryProjects reports ok=false when the payload does not parse --
// a truncated or non-JSON result must read as "unknown", never as "none".
func parseInventoryProjects(raw string) ([]InventoryProject, bool) {
	var payload struct {
		Projects []struct {
			ID                 string `json:"id"`
			Name               string `json:"name"`
			DefaultEnvironment string `json:"default_environment"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	var out []InventoryProject
	for _, p := range payload.Projects {
		if strings.TrimSpace(p.ID) == "" {
			continue
		}
		out = append(out, InventoryProject{ID: p.ID, Name: p.Name, DefaultEnvironment: p.DefaultEnvironment})
	}
	return out, true
}

// parseInventoryApps reports ok=false when the payload does not parse. A
// listApps result long enough to hit truncateToolResult arrives as broken JSON,
// and answering a heavy user with "you have nothing deployed" is the worst
// possible failure mode -- so an unreadable result yields ok=false and the
// caller keeps the inventory unknown.
func parseInventoryApps(raw string) ([]InventoryApp, bool) {
	var payload struct {
		Apps []struct {
			Name  string `json:"name"`
			Phase string `json:"phase"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	var out []InventoryApp
	for _, a := range payload.Apps {
		if strings.TrimSpace(a.Name) == "" {
			continue
		}
		out = append(out, InventoryApp{Name: a.Name, Phase: a.Phase})
	}
	return out, true
}

func pickInventoryEnv(raw string) (string, string) {
	var payload struct {
		Project struct {
			DefaultEnvironment string `json:"default_environment"`
		} `json:"project"`
		Environments []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			IsEphemeral bool   `json:"is_ephemeral"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", ""
	}

	type env struct {
		id   string
		name string
	}
	var candidates []env
	for _, e := range payload.Environments {
		if e.IsEphemeral || strings.TrimSpace(e.ID) == "" {
			continue
		}
		candidates = append(candidates, env{id: e.ID, name: e.Name})
	}
	if len(candidates) == 0 {
		return "", ""
	}

	if def := strings.TrimSpace(payload.Project.DefaultEnvironment); def != "" {
		for _, c := range candidates {
			if c.name == def {
				return c.id, c.name
			}
		}
	}
	for _, c := range candidates {
		if c.name == "prod" {
			return c.id, c.name
		}
	}
	return candidates[0].id, candidates[0].name
}

// systemMessage renders the inventory as a standalone system message that the
// engine injects directly before the user's message rather than folding into
// the main prompt: the inventory describes this turn's state, and keeping it
// adjacent to the question keeps it fresh instead of stale from the start of
// the conversation. The "nothing deployed" marker is emitted only when listApps
// actually ran and its result parsed, so the model is never told a fact the
// engine did not verify. A non-empty EnvID means listApps was executed, so
// EnvID set together with AppsLookedUp false is exactly the case where the call
// ran and its result was unusable -- that is reported as unknown, with an
// instruction to re-check with the tool.
func (inv *Inventory) systemMessage() string {
	if inv == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(inventoryHeader)

	switch {
	case !inv.ProjectsLookedUp:
		sb.WriteString(inventoryProjectsUnreadable)
	case len(inv.Projects) == 0:
		sb.WriteString(inventoryNoProjectsMarker)
	default:
		sb.WriteString("projects=[")
		shown := inv.Projects
		extra := 0
		if len(shown) > inventoryMaxProjects {
			extra = len(shown) - inventoryMaxProjects
			shown = shown[:inventoryMaxProjects]
		}
		for i, p := range shown {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s (id=%s)", p.Name, p.ID))
		}
		sb.WriteString("]")
		if extra > 0 {
			sb.WriteString(fmt.Sprintf(" (+%d more)", extra))
		}
	}

	sb.WriteString("; ")

	if inv.EnvID != "" {
		if inv.EnvName != "" {
			sb.WriteString(fmt.Sprintf("environment=%s (id=%s)", inv.EnvName, inv.EnvID))
		} else {
			sb.WriteString(fmt.Sprintf("environment=(id=%s)", inv.EnvID))
		}
		sb.WriteString("; ")
	}

	switch {
	case !inv.AppsLookedUp && inv.EnvID != "":
		sb.WriteString(inventoryAppsUnreadable)
	case !inv.AppsLookedUp:
		sb.WriteString(inventoryAppsUnknown)
	case len(inv.Apps) == 0:
		sb.WriteString(inventoryNoAppsMarker)
		sb.WriteString(inventoryNoAppsInstruction)
	default:
		sb.WriteString("apps=[")
		shown := inv.Apps
		extra := 0
		if len(shown) > inventoryMaxApps {
			extra = len(shown) - inventoryMaxApps
			shown = shown[:inventoryMaxApps]
		}
		for i, a := range shown {
			if i > 0 {
				sb.WriteString(", ")
			}
			if a.Phase != "" {
				sb.WriteString(fmt.Sprintf("%s (%s)", a.Name, a.Phase))
			} else {
				sb.WriteString(a.Name)
			}
		}
		sb.WriteString("]")
		if extra > 0 {
			sb.WriteString(fmt.Sprintf(" (+%d more)", extra))
		}
	}

	return sb.String()
}
