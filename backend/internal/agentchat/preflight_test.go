package agentchat

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// bigAppsJSON is a realistic listApps payload: n apps of roughly 600 bytes
// each, which is what a heavy user's project actually returns.
func bigAppsJSON(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"apps":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"name":"app-%02d","phase":"Ready","image":"registry.example.com/org/app-%02d:sha-%s","url":"https://app-%02d.example.com","summary":%q}`,
			i, i, strings.Repeat("d", 40), i, strings.Repeat("x", 420))
	}
	sb.WriteString(`]}`)
	return sb.String()
}

func TestPreflightTruncatedAppsResultIsNotReportedAsNothingDeployed(t *testing.T) {
	raw := bigAppsJSON(45)
	if len(raw) <= maxToolResultChars {
		t.Fatalf("payload len=%d, want > %d so that the tool result really gets truncated", len(raw), maxToolResultChars)
	}
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, raw)

	inv, log := runInventoryPreflight(context.Background(), ts.NewView(), "Bearer test", TurnContext{}, Emitter{})
	if inv == nil {
		t.Fatal("inventory is nil, want the preflight result")
	}
	if len(log) != 3 {
		t.Fatalf("preflight log=%+v, want 3 entries", log)
	}
	if inv.AppsLookedUp {
		t.Fatal("AppsLookedUp=true after an unparseable listApps result - the engine did not learn anything about the apps")
	}
	msg := inv.systemMessage()
	if strings.Contains(msg, inventoryNoAppsMarker) {
		t.Fatalf("msg=%q, must never claim nothing is deployed when listApps could not be parsed", msg)
	}
	if strings.Contains(msg, inventoryNoAppsInstruction) {
		t.Fatalf("msg=%q, must not push the deploy-your-first-app script at a user with live apps", msg)
	}
	if !strings.Contains(msg, inventoryAppsUnreadable) {
		t.Fatalf("msg=%q, want the apps-unreadable marker telling the model to re-check with listApps", msg)
	}
}

func TestPreflightParsedAppsAreReportedHonestly(t *testing.T) {
	ctx := context.Background()

	empty := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, `{"apps":[]}`)
	inv, _ := runInventoryPreflight(ctx, empty.NewView(), "Bearer test", TurnContext{}, Emitter{})
	if inv == nil || !inv.AppsLookedUp {
		t.Fatalf("inv=%+v, want AppsLookedUp=true for a valid empty list", inv)
	}
	msg := inv.systemMessage()
	if !strings.Contains(msg, inventoryNoAppsMarker) || !strings.Contains(msg, inventoryNoAppsInstruction) {
		t.Fatalf("msg=%q, want the nothing-deployed marker and instruction", msg)
	}

	full := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, groundedAppsJSON)
	inv, _ = runInventoryPreflight(ctx, full.NewView(), "Bearer test", TurnContext{}, Emitter{})
	if inv == nil || !inv.AppsLookedUp || len(inv.Apps) != 2 {
		t.Fatalf("inv=%+v, want the two parsed apps", inv)
	}
	msg = inv.systemMessage()
	if !strings.Contains(msg, "web (Ready)") || !strings.Contains(msg, "worker (Pending)") {
		t.Fatalf("msg=%q, want both apps with their phases", msg)
	}
	if strings.Contains(msg, inventoryNoAppsMarker) || strings.Contains(msg, inventoryAppsUnreadable) {
		t.Fatalf("msg=%q, want no unknown/empty marker when the list parsed", msg)
	}
}

func TestPreflightUnparseableProjectsIsNotReportedAsNoProjects(t *testing.T) {
	ts := newInventoryToolset("<html>502 Bad Gateway</html>", groundedProjectJSON, groundedAppsJSON)

	inv, _ := runInventoryPreflight(context.Background(), ts.NewView(), "Bearer test", TurnContext{}, Emitter{})
	if inv == nil {
		t.Fatal("inventory is nil, want the preflight result")
	}
	msg := inv.systemMessage()
	if strings.Contains(msg, inventoryNoProjectsMarker) {
		t.Fatalf("msg=%q, must not claim the user has no projects when listProjects could not be parsed", msg)
	}
}

func TestPreflightEmitsNoToolCallChips(t *testing.T) {
	ts := newInventoryToolset(groundedProjectsJSON, groundedProjectJSON, groundedAppsJSON)
	var chips []string
	emit := Emitter{ToolCall: func(name string) { chips = append(chips, name) }}

	inv, log := runInventoryPreflight(context.Background(), ts.NewView(), "Bearer test", TurnContext{}, emit)
	if inv == nil || len(log) != 3 {
		t.Fatalf("inv=%+v log=%+v, want a full three-call preflight", inv, log)
	}
	if len(chips) != 0 {
		t.Fatalf("preflight emitted tool chips %v, want none - preflight is engine grounding, not a visible tool call", chips)
	}
}

func TestParseInventoryProjects_IgnoresGarbage(t *testing.T) {
	if got, ok := parseInventoryProjects("not json at all"); got != nil || ok {
		t.Fatalf("parseInventoryProjects(garbage)=(%+v,%v), want (nil,false)", got, ok)
	}
	if got, ok := parseInventoryProjects(`{"projects":[]}`); len(got) != 0 || !ok {
		t.Fatalf("parseInventoryProjects(empty)=(%+v,%v), want (none,true)", got, ok)
	}

	got, ok := parseInventoryProjects(`{"projects":[{"id":"","name":"skipme"},{"id":"p1","name":"demo","default_environment":"prod"}]}`)
	if !ok {
		t.Fatal("parseInventoryProjects(valid) reported failure")
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want exactly the project with an id", got)
	}
	if got[0].ID != "p1" || got[0].Name != "demo" || got[0].DefaultEnvironment != "prod" {
		t.Fatalf("got %+v, want {p1 demo prod}", got[0])
	}
}

func TestParseInventoryApps_IgnoresGarbage(t *testing.T) {
	if got, ok := parseInventoryApps("<html>502</html>"); got != nil || ok {
		t.Fatalf("parseInventoryApps(garbage)=(%+v,%v), want (nil,false)", got, ok)
	}
	if got, ok := parseInventoryApps(`{"apps":[]}`); len(got) != 0 || !ok {
		t.Fatalf("parseInventoryApps(empty)=(%+v,%v), want (none,true)", got, ok)
	}
	truncated := bigAppsJSON(45)[:maxToolResultChars] + "\n... [truncated]"
	if got, ok := parseInventoryApps(truncated); got != nil || ok {
		t.Fatalf("parseInventoryApps(truncated)=(%+v,%v), want (nil,false)", got, ok)
	}
	got, ok := parseInventoryApps(`{"apps":[{"name":"","phase":"Ready"},{"name":"web","phase":"Ready"},{"name":"worker"}]}`)
	if !ok {
		t.Fatal("parseInventoryApps(valid) reported failure")
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 named apps", got)
	}
	if got[0].Name != "web" || got[0].Phase != "Ready" {
		t.Fatalf("got[0]=%+v want {web Ready}", got[0])
	}
	if got[1].Name != "worker" || got[1].Phase != "" {
		t.Fatalf("got[1]=%+v want {worker }", got[1])
	}
}

func TestPickInventoryEnv_PrefersDefaultThenProdAndSkipsEphemeral(t *testing.T) {
	withDefault := `{"project":{"default_environment":"staging"},"environments":[
		{"id":"e1","name":"prod","is_ephemeral":false},
		{"id":"e2","name":"staging","is_ephemeral":false}]}`
	id, name := pickInventoryEnv(withDefault)
	if id != "e2" || name != "staging" {
		t.Fatalf("got (%q,%q), want the project default environment (e2,staging)", id, name)
	}

	noDefault := `{"project":{},"environments":[
		{"id":"e1","name":"dev","is_ephemeral":false},
		{"id":"e2","name":"prod","is_ephemeral":false}]}`
	id, name = pickInventoryEnv(noDefault)
	if id != "e2" || name != "prod" {
		t.Fatalf("got (%q,%q), want prod (e2,prod)", id, name)
	}

	onlyEphemeral := `{"project":{"default_environment":"prod"},"environments":[
		{"id":"e1","name":"pr-7","is_ephemeral":true}]}`
	id, name = pickInventoryEnv(onlyEphemeral)
	if id != "" || name != "" {
		t.Fatalf("got (%q,%q), want empty - preview environments must not be picked", id, name)
	}

	fallback := `{"project":{"default_environment":"gone"},"environments":[
		{"id":"e9","name":"dev","is_ephemeral":false}]}`
	id, name = pickInventoryEnv(fallback)
	if id != "e9" || name != "dev" {
		t.Fatalf("got (%q,%q), want the only real environment (e9,dev)", id, name)
	}

	if id, name = pickInventoryEnv("boom"); id != "" || name != "" {
		t.Fatalf("got (%q,%q) for garbage, want empty", id, name)
	}
}

func TestInventorySystemMessage_NoAppsMarkerOnlyWhenLookedUp(t *testing.T) {
	notLookedUp := &Inventory{
		Projects:         []InventoryProject{{ID: "p1", Name: "demo"}, {ID: "p2", Name: "other"}},
		ProjectsLookedUp: true,
	}
	msg := notLookedUp.systemMessage()
	if !strings.Contains(msg, inventoryAppsUnknown) {
		t.Fatalf("msg=%q, want the apps-unknown marker", msg)
	}
	if strings.Contains(msg, inventoryNoAppsMarker) {
		t.Fatalf("msg=%q, must not claim nothing is deployed when listApps never ran", msg)
	}

	lookedUp := &Inventory{
		Projects:         []InventoryProject{{ID: "p1", Name: "demo"}},
		ProjectID:        "p1",
		EnvID:            "e1",
		EnvName:          "prod",
		ProjectsLookedUp: true,
		AppsLookedUp:     true,
	}
	msg = lookedUp.systemMessage()
	if !strings.Contains(msg, inventoryNoAppsMarker) || !strings.Contains(msg, inventoryNoAppsInstruction) {
		t.Fatalf("msg=%q, want the nothing-deployed marker and instruction", msg)
	}
	if !strings.Contains(msg, "environment=prod (id=e1)") {
		t.Fatalf("msg=%q, want the environment that was actually inspected", msg)
	}

	noProjects := &Inventory{ProjectsLookedUp: true}
	msg = noProjects.systemMessage()
	if !strings.Contains(msg, inventoryNoProjectsMarker) {
		t.Fatalf("msg=%q, want the no-projects marker", msg)
	}

	appsUnreadable := &Inventory{
		Projects:         []InventoryProject{{ID: "p1", Name: "demo"}},
		ProjectID:        "p1",
		EnvID:            "e1",
		EnvName:          "prod",
		ProjectsLookedUp: true,
	}
	msg = appsUnreadable.systemMessage()
	if !strings.Contains(msg, inventoryAppsUnreadable) {
		t.Fatalf("msg=%q, want the apps-unreadable marker when listApps ran and gave nothing usable", msg)
	}
	if strings.Contains(msg, inventoryNoAppsMarker) || strings.Contains(msg, inventoryAppsUnknown) {
		t.Fatalf("msg=%q, an unreadable listApps result is neither empty nor never-ran", msg)
	}

	projectsUnreadable := &Inventory{}
	msg = projectsUnreadable.systemMessage()
	if !strings.Contains(msg, inventoryProjectsUnreadable) {
		t.Fatalf("msg=%q, want the projects-unreadable marker", msg)
	}
	if strings.Contains(msg, inventoryNoProjectsMarker) {
		t.Fatalf("msg=%q, must not claim the user has no projects", msg)
	}

	var nilInv *Inventory
	if got := nilInv.systemMessage(); got != "" {
		t.Fatalf("nil inventory message=%q, want empty", got)
	}
}

func TestInventorySystemMessage_TruncatesLongLists(t *testing.T) {
	inv := &Inventory{ProjectID: "p1", EnvID: "e1", EnvName: "prod", ProjectsLookedUp: true, AppsLookedUp: true}
	for i := 0; i < inventoryMaxProjects+5; i++ {
		inv.Projects = append(inv.Projects, InventoryProject{ID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("proj%d", i)})
	}
	for i := 0; i < inventoryMaxApps+7; i++ {
		inv.Apps = append(inv.Apps, InventoryApp{Name: fmt.Sprintf("app%d", i), Phase: "Ready"})
	}

	msg := inv.systemMessage()
	if !strings.Contains(msg, "(+5 more)") {
		t.Fatalf("msg=%q, want a +5 more tail for projects", msg)
	}
	if !strings.Contains(msg, "(+7 more)") {
		t.Fatalf("msg=%q, want a +7 more tail for apps", msg)
	}
	if strings.Contains(msg, fmt.Sprintf("proj%d ", inventoryMaxProjects)) {
		t.Fatalf("msg=%q, project past the cap leaked in", msg)
	}
	if strings.Contains(msg, fmt.Sprintf("app%d ", inventoryMaxApps)) {
		t.Fatalf("msg=%q, app past the cap leaked in", msg)
	}
}
