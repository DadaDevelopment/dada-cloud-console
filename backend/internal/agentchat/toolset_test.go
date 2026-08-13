package agentchat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestToolset(t *testing.T) *Toolset {
	t.Helper()
	return loadTestToolsetAt(t, "http://localhost:8080")
}

// loadTestToolsetAt builds the real toolset against an arbitrary backend URL.
// Pass an unroutable URL when a test needs Execute to fail fast on connection
// refused instead of touching the network.
func loadTestToolsetAt(t *testing.T, backendURL string) *Toolset {
	t.Helper()
	specPath := filepath.Join("..", "api", "docs", "swagger.json")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read embedded swagger spec %q: %v", specPath, err)
	}
	ts, err := BuildToolset(spec, backendURL)
	if err != nil {
		t.Fatalf("BuildToolset: %v", err)
	}
	return ts
}

func TestIsWrite_KnownWriteTool(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("restartApp") {
		t.Fatal("expected restartApp to be a registered tool")
	}
	if !ts.IsWrite("restartApp") {
		t.Fatal("expected restartApp to be classified as a write tool")
	}
}

func TestIsWrite_KnownReadTool(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("listApps") {
		t.Fatal("expected listApps to be a registered tool")
	}
	if ts.IsWrite("listApps") {
		t.Fatal("expected listApps to be classified as a read tool")
	}
}

func TestIsWrite_AllWriteKeepToolsRegisteredAndClassified(t *testing.T) {
	ts := loadTestToolset(t)
	for _, name := range writeKeepTools {
		if !ts.Has(name) {
			t.Errorf("write tool %q is not registered in the toolset", name)
			continue
		}
		if !ts.IsWrite(name) {
			t.Errorf("write tool %q was not classified as a write tool", name)
		}
	}
}

// TestProbeAppNetwork_WriteButNotRiskyOrConfirmationGatedInEditMode pins the
// design intent: the probe execs inside a pod so it belongs on the write path
// like restartApp, but it destroys nothing, spends nothing and mints no
// credential, so ModeEdit (the default) must run it without a confirmation
// card the same way it runs restartApp.
func TestProbeAppNetwork_WriteButNotRiskyOrConfirmationGatedInEditMode(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("probeAppNetwork") {
		t.Fatal("expected probeAppNetwork to be a registered tool")
	}
	if !ts.IsWrite("probeAppNetwork") {
		t.Fatal("expected probeAppNetwork to be classified as a write tool")
	}
	view := ts.NewView(ModeEdit)
	if view.NeedsConfirmation("probeAppNetwork") {
		t.Error("expected probeAppNetwork to run without a confirmation card in ModeEdit")
	}
	manual := ts.NewView(ModeManual)
	if !manual.NeedsConfirmation("probeAppNetwork") {
		t.Error("expected probeAppNetwork to still confirm once in ModeManual, like every other write")
	}
}

func TestKeepTools_AllRegisteredAndClassifiedAsRead(t *testing.T) {
	ts := loadTestToolset(t)
	for _, name := range keepTools {
		if denyTools[name] {
			continue
		}
		registered := name
		if name == "submitFeedback" {
			registered = SupportTicketTool
		}
		if !ts.Has(registered) {
			t.Errorf("read tool %q is not registered: it must match an operationId in the embedded swagger spec, otherwise the assistant silently loses the capability", registered)
			continue
		}
		if ts.IsWrite(registered) {
			t.Errorf("read tool %q was classified as a write tool", registered)
		}
	}
}

func TestKeepTools_SourceArchiveDownloadReachableFromChat(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("downloadSourceArchive") {
		t.Fatal("downloadSourceArchive must be reachable from chat: without it the assistant tells upload-deploy users that recovering their own source is impossible and files a support ticket instead")
	}
	if ts.IsWrite("downloadSourceArchive") {
		t.Fatal("downloadSourceArchive mutates nothing and must not require a write confirmation card")
	}
}

func TestCriticalTools_ReachableFromChat(t *testing.T) {
	ts := loadTestToolset(t)
	cases := []struct{ name, why string }{
		{"createApp", "TC-01: 'разверни ты' must create the app, not send the user to the console UI"},
		{"connectGitRepo", "TC-04: linking a GitHub repo is the most common first-deploy path"},
		{"addDomainAuthorization", "TC-30: hosting on a custom domain starts with authorizing the apex"},
		{"verifyDomainAuthorization", "TC-30: the user needs the DNS check re-run from chat"},
		{"attachHostname", "TC-30: without it an authorized domain never reaches the app"},
		{"listAppFiles", "TC-24: inspecting the app's persistent volume from chat"},
		{"readAppFile", "TC-24: reading a config/log file off the volume from chat"},
		{"listDatabaseBackups", "TC-29: a restore proposal must be grounded in real backups"},
		{"restoreDatabase", "TC-29: recovering a database is the highest-stakes support ask"},
		{"listBoxes", "TC-28: boxes must not be denied as a non-existent feature"},
		{"getBoxCatalog", "TC-28: the assistant must be able to describe box images and sizes"},
		{"listS3Buckets", "TC-15: storage inventory"},
		{"getBillingUsage", "TC-31: money questions must be answered from data"},
		{"getProjectQuotas", "TC-31: plan limits explain most 'why can't I create another app'"},
		{"listDomainAuthorizations", "TC-30: the assistant must see pending domain authorizations"},
	}
	for _, c := range cases {
		if !ts.Has(c.name) {
			t.Errorf("tool %q must be reachable from chat -- %s", c.name, c.why)
		}
	}
}

func TestToolsetDefs_RemainsFullCatalog(t *testing.T) {
	ts := loadTestToolset(t)
	names := map[string]bool{}
	for _, d := range ts.Defs {
		names[d.Function.Name] = true
	}
	for _, want := range []string{"createApp", "listProjects"} {
		if !names[want] {
			t.Errorf("Toolset.Defs must stay the full catalog, missing %q", want)
		}
	}
	if len(ts.Defs) != len(ts.order) {
		t.Errorf("Toolset.Defs = %d entries, want %d curated tools", len(ts.Defs), len(ts.order))
	}
}

func TestView_StartsSmallAndGrowsOnlyByLoading(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView(ModeManual)
	before := view.Defs()
	if len(before) != len(baseTools)+2 {
		t.Fatalf("view exposes %d tools, want %d base tools plus load_tool and %s", len(before), len(baseTools)+2, OpenPageTool)
	}
	got := map[string]bool{}
	for _, d := range before {
		got[d.Function.Name] = true
	}
	for _, name := range append(append([]string{}, baseTools...), LoadToolTool, OpenPageTool) {
		if !got[name] {
			t.Errorf("view is missing %q", name)
		}
	}
	if len(ts.Defs) <= len(before)+10 {
		t.Fatalf("catalog is %d tools and the view exposes %d; the point of loading is that the array starts far smaller", len(ts.Defs), len(before))
	}

	if _, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["listDatabaseBackups"]}`); isErr {
		t.Fatal("load_tool of a real catalog tool must succeed")
	}
	after := view.Defs()
	if len(after) != len(before)+1 {
		t.Fatalf("tools after load_tool = %d, want %d: a loaded tool must become a real definition the model can call natively", len(after), len(before)+1)
	}
	loaded := after[len(after)-3]
	if loaded.Function.Name != "listDatabaseBackups" {
		t.Fatalf("loaded definition is %q, want listDatabaseBackups", loaded.Function.Name)
	}
	if len(loaded.Function.Parameters) == 0 {
		t.Fatal("the loaded definition carries no schema; the model would be guessing arguments again")
	}
	if after[len(after)-2].Function.Name != LoadToolTool {
		t.Fatal("load_tool must stay available so the model can reach for more")
	}

	if _, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["listDatabaseBackups"]}`); isErr {
		t.Fatal("re-loading an already loaded tool must not fail")
	}
	if len(view.Defs()) != len(after) {
		t.Fatalf("loading the same tool twice duplicated it: %d defs", len(view.Defs()))
	}
}

// TestView_LoadedToolIsCallable is the property the whole redesign exists for:
// after loading, the model calls the tool by its own name, with its own
// arguments, through the provider's native function calling.
func TestView_LoadedToolIsCallable(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
	out, isErr := view.Execute(context.Background(), "", "getProject", `{}`)
	if !isErr || !strings.Contains(out, "missing required field") {
		t.Fatalf("a call missing a required argument must fail locally with the reason, got: %s", out)
	}
	if !strings.Contains(out, "schema:") {
		t.Errorf("a validation error must carry the schema so the model fixes itself on the next attempt, got: %s", out)
	}
}

func TestView_UnknownToolNamesTheCatalog(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
	out, isErr := view.Execute(context.Background(), "", "totallyMadeUpTool", `{}`)
	if !isErr {
		t.Fatalf("calling a non-existent tool must fail: %s", out)
	}
	if !strings.Contains(out, "unknown tool") || !strings.Contains(out, LoadToolTool) {
		t.Errorf("the error must name the problem and the way out, got: %s", out)
	}
}

func TestView_LoadCapBoundsTheToolsArray(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView(ModeManual)
	var names []string
	for _, name := range view.CatalogNames() {
		names = append(names, name)
		if len(names) > maxLoadedTools+4 {
			break
		}
	}
	for _, name := range names {
		payload, _ := json.Marshal(map[string]any{"names": []string{name}})
		view.Execute(context.Background(), "", LoadToolTool, string(payload))
	}
	if got := len(view.LoadedNames()); got != maxLoadedTools {
		t.Fatalf("loaded %d tools, want the cap of %d", got, maxLoadedTools)
	}
	out, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["`+names[len(names)-1]+`"]}`)
	if isErr || !strings.Contains(out, "already holds") {
		t.Fatalf("hitting the cap must be reported honestly, got isErr=%v: %s", isErr, out)
	}
}

func TestLoadTool_AnnouncesWhatItMadeAvailable(t *testing.T) {
	view := loadTestToolset(t).NewView(ModeManual)
	out, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["listDatabaseBackups","restoreDatabase"]}`)
	if isErr {
		t.Fatalf("load_tool failed: %s", out)
	}
	for _, want := range []string{"listDatabaseBackups", "restoreDatabase", "is now available"} {
		if !strings.Contains(out, want) {
			t.Errorf("load_tool output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"properties"`) {
		t.Errorf("the schema belongs in the tools array, not duplicated into the conversation:\n%s", out)
	}
	if !strings.Contains(out, "changes state") {
		t.Error("load_tool must mark mutating tools so the model knows a confirmation card follows")
	}
	names := view.LoadedNames()
	if len(names) != 2 || names[0] != "listDatabaseBackups" || names[1] != "restoreDatabase" {
		t.Fatalf("LoadedNames=%v, want both tools in load order", names)
	}
}

// TestLoadTool_WriteMarkerFollowsTheMode keeps the model's promise to the user
// honest: in edit mode a reversible write runs without a card, so telling the
// model one is coming would make it announce a confirmation that never appears.
func TestLoadTool_WriteMarkerFollowsTheMode(t *testing.T) {
	manual, _ := loadTestToolset(t).NewView(ModeManual).Execute(context.Background(), "", LoadToolTool, `{"names":["restartApp"]}`)
	if !strings.Contains(manual, "the user confirms it") {
		t.Errorf("manual mode confirms every write, got: %s", manual)
	}
	edit, _ := loadTestToolset(t).NewView(ModeEdit).Execute(context.Background(), "", LoadToolTool, `{"names":["restartApp"]}`)
	if !strings.Contains(edit, "changes state immediately") {
		t.Errorf("edit mode runs restartApp without a card, got: %s", edit)
	}
}

func TestLoadTool_UnknownNameIsAnHonestError(t *testing.T) {
	view := loadTestToolset(t).NewView(ModeManual)
	out, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["totallyMadeUpTool"]}`)
	if isErr {
		t.Fatalf("a partly unknown name list must still return the known ones: %s", out)
	}
	if !strings.Contains(out, "not in the catalog") {
		t.Errorf("the model must be told the name does not exist, got: %s", out)
	}
	if _, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":[]}`); !isErr {
		t.Error("an empty names array must be an error, not a silent no-op")
	}
}

func TestReadOnlyView_RefusesWritesAndHidesThemFromTheCatalog(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewReadOnlyView()
	out, isErr := view.Execute(context.Background(), "", "restartApp", `{"appId":"a1"}`)
	if !isErr {
		t.Fatalf("a read-only session must refuse a write: %s", out)
	}
	if loaded, _ := view.Execute(context.Background(), "", LoadToolTool, `{"names":["restartApp"]}`); !strings.Contains(loaded, "read-only") {
		t.Errorf("a read-only session must not be able to load a write into its tools array, got: %s", loaded)
	}
	for _, name := range view.CatalogNames() {
		if view.IsWrite(name) {
			t.Errorf("write tool %q is listed to a read-only session; it would be proposed and then refused", name)
		}
	}
}

func TestCatalogNames_CoversCapabilitiesAndExcludesTheBaseSet(t *testing.T) {
	view := loadTestToolset(t).NewView(ModeManual)
	listed := map[string]bool{}
	for _, name := range view.CatalogNames() {
		listed[name] = true
	}
	cases := []struct{ name, why string }{
		{"createApp", "TC-01: creating an app from chat"},
		{"connectGitRepo", "TC-04: linking a repo"},
		{"addDomainAuthorization", "TC-30: authorizing a custom domain"},
		{"listAppFiles", "TC-24: inspecting the persistent volume"},
		{"listDatabaseBackups", "TC-29: grounding a restore in real backups"},
		{"restoreDatabase", "TC-29: recovering a database"},
		{"listBoxes", "TC-28: boxes exist and must be reachable"},
		{"getBillingUsage", "TC-31: money questions answered from data"},
		{SupportTicketTool, "the escape hatch must always be listed"},
	}
	for _, c := range cases {
		if !listed[c.name] {
			t.Errorf("catalog is missing %q -- %s", c.name, c.why)
		}
	}
	for _, name := range baseTools {
		if listed[name] {
			t.Errorf("base tool %q is repeated in the name-only catalog; it already ships with a full schema", name)
		}
	}
	for name := range denyTools {
		if listed[name] {
			t.Errorf("deny-listed tool %q is listed in the catalog", name)
		}
	}
}

// TestOpenPage_MovesTheUserOncePerTurn covers the reason the tool exists: an
// answer that ends in "go to /projects/{id}/git" leaves the last step to the
// user for no reason, since the console is the product's own UI. The tool must
// move them, refuse a page that does not exist rather than parking them on a
// 404, and refuse a second move so the page does not shift under them while
// they read.
func TestOpenPage_MovesTheUserOncePerTurn(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
	var moved []string
	view.SetNavigator(func(path string) bool {
		if path != "/projects/p1/git/import" {
			return false
		}
		moved = append(moved, path)
		return true
	})

	out, isErr := view.Execute(context.Background(), "", OpenPageTool, `{"path":"/projects/p1/git/import"}`)
	if isErr {
		t.Fatalf("opening a real console page must succeed, got: %s", out)
	}
	if len(moved) != 1 {
		t.Fatalf("navigator was called %d times, want exactly once", len(moved))
	}
	if !strings.Contains(out, "/projects/p1/git/import") {
		t.Errorf("the result must tell the model where the user now is, got: %s", out)
	}

	out, isErr = view.Execute(context.Background(), "", OpenPageTool, `{"path":"/projects/p1/apps"}`)
	if !isErr {
		t.Fatalf("a second move in one turn must be refused, got: %s", out)
	}
	if len(moved) != 1 {
		t.Fatalf("navigator was called %d times; the second call must not reach the browser", len(moved))
	}
}

func TestOpenPage_RefusesPathsThatGoNowhere(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"empty path", `{"path":""}`},
		{"no path at all", `{}`},
		{"unknown route", `{"path":"/projects/p1/logs"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
			called := false
			view.SetNavigator(func(path string) bool {
				called = true
				return path == "/projects/p1"
			})
			out, isErr := view.Execute(context.Background(), "", OpenPageTool, c.args)
			if !isErr {
				t.Fatalf("%s must be an error the model can recover from, got: %s", c.name, out)
			}
			if c.name != "unknown route" && called {
				t.Error("the navigator must not be reached before the arguments are valid")
			}
		})
	}
}

// TestOpenPage_WithoutANavigatorSaysSo guards the history endpoint and any
// other caller that has no live stream to the browser: silently doing nothing
// would have the assistant claim it moved the user when it did not.
func TestOpenPage_WithoutANavigatorSaysSo(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
	out, isErr := view.Execute(context.Background(), "", OpenPageTool, `{"path":"/projects/p1"}`)
	if !isErr {
		t.Fatalf("a view with no navigator must not report a move, got: %s", out)
	}
	if !strings.Contains(out, "write the path") {
		t.Errorf("the refusal must point at the fallback the user can still use, got: %s", out)
	}
}

// TestOpenPage_IsFreeOfTheToolBudget keeps navigation from costing the user an
// answer: it touches no backend, so charging it against MaxToolCallsPerTurn
// would mean moving the page instead of, say, reading the app state.
func TestOpenPage_IsFreeOfTheToolBudget(t *testing.T) {
	if !IsMetaTool(OpenPageTool) {
		t.Errorf("%s must be a meta-tool; it calls nothing on the backend", OpenPageTool)
	}
}

// TestExcludeVMOnlyTools_K8sCatalogDropsVMToolsButKeepsALogsAndStateAnswer pins
// the fix for the incident where a Kubernetes environment's assistant made ten
// straight failed calls into getAppState/getAppServerState/getAppLogs (all
// Portainer-only, state.go:113) before giving up. Once a view is scoped away
// from the VM runtime, those names must not appear in the catalog, the base
// tool defs, or dispatch -- and searchLogs (works for both runtimes) plus
// getAppHealth (DB-backed, runtime-agnostic) must still be reachable, so the
// model always has a real state-and-logs answer for a Kubernetes app.
func TestExcludeVMOnlyTools_K8sCatalogDropsVMToolsButKeepsALogsAndStateAnswer(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)
	view.ExcludeVMOnlyTools()

	catalog := map[string]bool{}
	for _, name := range view.CatalogNames() {
		catalog[name] = true
	}
	for name := range vmOnlyTools {
		if catalog[name] {
			t.Errorf("VM-only tool %q must not be listed in a runtime-excluded catalog", name)
		}
	}
	if !catalog["getAppHealth"] {
		t.Error("getAppHealth (the runtime-agnostic state answer) must remain in the catalog")
	}

	for _, def := range view.Defs() {
		if vmOnlyTools[def.Function.Name] {
			t.Errorf("VM-only tool %q must not be one of the base tool definitions sent to the model", def.Function.Name)
		}
	}
	sawSearchLogs := false
	for _, def := range view.Defs() {
		if def.Function.Name == "searchLogs" {
			sawSearchLogs = true
		}
	}
	if !sawSearchLogs {
		t.Error("searchLogs must remain a base tool so a Kubernetes turn always has a logs answer available with no load_tool round trip")
	}

	for name := range vmOnlyTools {
		out, isErr := view.Execute(context.Background(), "", name, `{}`)
		if !isErr {
			t.Errorf("dispatching excluded tool %q must fail even if the model calls it directly by name, got: %s", name, out)
		}
	}

	loaded, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["getAppState"]}`)
	if isErr {
		t.Fatalf("load_tool itself must not error even when every requested name is excluded: %s", loaded)
	}
	if !strings.Contains(loaded, "not available for this environment") {
		t.Errorf("load_tool must explain why getAppState was not loaded, got: %s", loaded)
	}
}

// TestExcludeVMOnlyTools_VMCatalogKeepsThem is the control case: a view that is
// never told to exclude VM-only tools (the pre-existing, VM-runtime behaviour)
// still offers all of them.
func TestExcludeVMOnlyTools_VMCatalogKeepsThem(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView(ModeManual)

	catalog := map[string]bool{}
	for _, name := range view.CatalogNames() {
		catalog[name] = true
	}
	for name := range vmOnlyTools {
		if name == "getAppState" {
			continue
		}
		if !catalog[name] {
			t.Errorf("VM-only tool %q must be listed in the catalog when the view is not runtime-restricted", name)
		}
	}

	sawGetAppState := false
	for _, def := range view.Defs() {
		if def.Function.Name == "getAppState" {
			sawGetAppState = true
		}
	}
	if !sawGetAppState {
		t.Error("getAppState is a base tool and must be sent to the model when the view is not runtime-restricted")
	}
}
