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
	if len(before) != len(baseTools)+1 {
		t.Fatalf("view exposes %d tools, want %d base tools plus load_tool", len(before), len(baseTools)+1)
	}
	got := map[string]bool{}
	for _, d := range before {
		got[d.Function.Name] = true
	}
	for _, name := range append(append([]string{}, baseTools...), LoadToolTool) {
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
	loaded := after[len(after)-2]
	if loaded.Function.Name != "listDatabaseBackups" {
		t.Fatalf("loaded definition is %q, want listDatabaseBackups", loaded.Function.Name)
	}
	if len(loaded.Function.Parameters) == 0 {
		t.Fatal("the loaded definition carries no schema; the model would be guessing arguments again")
	}
	if after[len(after)-1].Function.Name != LoadToolTool {
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
