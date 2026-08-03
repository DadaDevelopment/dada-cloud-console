package agentchat

import (
	"context"
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

func TestView_DefsAreFixedAndSmall(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	before := view.Defs()
	if len(before) != len(baseTools)+2 {
		t.Fatalf("view exposes %d tools, want %d base tools plus load_tool and call_tool", len(before), len(baseTools)+2)
	}
	got := map[string]bool{}
	for _, d := range before {
		got[d.Function.Name] = true
	}
	for _, name := range append(append([]string{}, baseTools...), LoadToolTool, CallToolTool) {
		if !got[name] {
			t.Errorf("view is missing %q", name)
		}
	}

	if _, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["listDatabaseBackups"]}`); isErr {
		t.Fatal("load_tool of a real catalog tool must succeed")
	}
	after := view.Defs()
	if len(after) != len(before) {
		t.Fatalf("the tools block changed after load_tool (%d -> %d); it is serialized ahead of the system prompt, so a change invalidates the whole prefix cache", len(before), len(after))
	}
	for i := range after {
		if after[i].Function.Name != before[i].Function.Name {
			t.Fatalf("tool order changed at %d: %s -> %s", i, before[i].Function.Name, after[i].Function.Name)
		}
	}
}

func TestBaseTools_AllExistInCatalog(t *testing.T) {
	ts := loadTestToolset(t)
	for _, name := range baseTools {
		if !ts.Has(name) {
			t.Errorf("base tool %q does not exist in the catalog: the turn would start with a dangling definition", name)
		}
	}
}

func TestBaseTools_ContainNoWriteTool(t *testing.T) {
	ts := loadTestToolset(t)
	for _, name := range baseTools {
		if ts.IsWrite(name) {
			t.Errorf("base tool %q is a write tool; the always-on set must be read-only", name)
		}
	}
}

func TestDownloadDatabaseBackup_RequiresConfirmationCard(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("downloadDatabaseBackup") {
		t.Fatal("downloadDatabaseBackup must stay in the catalog")
	}
	if !ts.IsWrite("downloadDatabaseBackup") {
		t.Fatal("downloadDatabaseBackup hands out a presigned SigV4 GET on the full pg_dump that needs no Keycloak session; it must be gated behind a confirmation card, not executed silently as a read")
	}
}

func TestRedactToolResult_MintedDeployHookTokenNeverPersisted(t *testing.T) {
	raw := `{"id":"9f0","token":"dhk_live_7Yq2s1AbCdEf","deploy_url":"https://console.dada-tuda.ru/api/v1/deploy"}`
	got := RedactToolResult("createDeployHook", raw)
	if strings.Contains(got, "dhk_live_7Yq2s1AbCdEf") {
		t.Fatalf("the one-time deploy-hook token survived redaction: %s", got)
	}
	if got == raw {
		t.Fatal("createDeployHook result must not pass through verbatim")
	}
	if !strings.Contains(got, RedactedMarker) {
		t.Fatalf("a redacted result must be recognisable as redacted, got: %s", got)
	}
}

func TestRedactToolResult_StripsPresignedSignature(t *testing.T) {
	raw := `{"url":"https://s3.dada.ru/dumps/db-1.sql.gz?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAEXAMPLE%2F20260803%2Fru-1%2Fs3%2Faws4_request&X-Amz-Signature=deadbeefcafe","expires_at":"2026-08-03T10:05:00Z"}`
	for _, name := range []string{"downloadDatabaseBackup", "downloadSourceArchive"} {
		got := RedactToolResult(name, raw)
		for _, secret := range []string{"X-Amz-Signature", "deadbeefcafe", "AKIAEXAMPLE"} {
			if strings.Contains(got, secret) {
				t.Errorf("%s: presigned material %q survived redaction: %s", name, secret, got)
			}
		}
		if !strings.Contains(got, "https://s3.dada.ru/dumps/db-1.sql.gz") {
			t.Errorf("%s: redaction must keep the URL origin and path for support, got: %s", name, got)
		}
		if !strings.Contains(got, "2026-08-03T10:05:00Z") {
			t.Errorf("%s: redaction must not eat non-URL fields, got: %s", name, got)
		}
	}
}

func TestRedactToolResult_LeavesOrdinaryResultsAlone(t *testing.T) {
	raw := `{"apps":[{"name":"shop-web","phase":"Ready"}]}`
	if got := RedactToolResult("listApps", raw); got != raw {
		t.Fatalf("an ordinary tool result must pass through untouched, got: %s", got)
	}
	if got := RedactToolResult("listApps", ""); got != "" {
		t.Fatalf("empty result must stay empty, got: %q", got)
	}
}

func TestRedactToolResult_CoversEveryKeyMintingWriteTool(t *testing.T) {
	for name := range mintedSecretTools {
		if !contains(writeKeepTools, name) {
			t.Errorf("key-minting tool %q must be a write tool so the user sees a confirmation card before it is minted", name)
		}
	}
	for name := range presignedResultTools {
		if !contains(keepTools, name) && !contains(writeKeepTools, name) {
			t.Errorf("presigned-result tool %q is redacted but not in any allowlist; the redaction rule is dead code", name)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestSanitizeArgs_StripsGitToken(t *testing.T) {
	got := sanitizeArgs("connectGitRepo", `{"repo_full_name":"a/b","token":"ghp_x"}`)
	if strings.Contains(got, "token") || strings.Contains(got, "ghp_x") {
		t.Fatalf("connectGitRepo token must be stripped, got: %s", got)
	}
	if !strings.Contains(got, "a/b") {
		t.Fatalf("sanitizeArgs dropped a legitimate argument: %s", got)
	}
	other := `{"token":"keep-me"}`
	if sanitizeArgs("createApp", other) != other {
		t.Fatal("sanitizeArgs must leave tools without a deny-list untouched")
	}
}

func TestTruncateToolResult(t *testing.T) {
	short := "small result"
	if truncateToolResult(short) != short {
		t.Fatal("short results must pass through unchanged")
	}
	long := strings.Repeat("x", maxToolResultChars+100)
	got := truncateToolResult(long)
	if len(got) >= len(long) {
		t.Fatalf("oversized result was not truncated: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "[truncated]") {
		t.Fatal("a truncated result must say so, otherwise the model treats it as complete")
	}
}

func TestIsMetaTool(t *testing.T) {
	if !IsMetaTool(LoadToolTool) {
		t.Fatal("load_tool is a meta-tool and must not be charged to the backend tool budget")
	}
	if IsMetaTool(CallToolTool) {
		t.Fatal("call_tool performs a real backend call and must be charged as the tool it dispatches to")
	}
	if IsMetaTool("listApps") {
		t.Fatal("a real backend tool must not be reported as a meta-tool")
	}
}

func TestSecretDenyList_WinsOverReadAndWrite(t *testing.T) {
	ts := loadTestToolset(t)
	for name := range denyTools {
		if ts.Has(name) {
			t.Errorf("deny-listed tool %q must not be registered in the toolset (read or write)", name)
		}
		if ts.IsWrite(name) {
			t.Errorf("deny-listed tool %q must not report IsWrite==true", name)
		}
	}
}

func TestSecretDenyList_NotAccidentallyAddedToWriteKeep(t *testing.T) {
	for _, name := range writeKeepTools {
		if denyTools[name] {
			t.Fatalf("write tool %q is also deny-listed; deny-list must win, so it must never appear registered at all", name)
		}
	}
}

func TestKeepAndWriteKeep_DoNotOverlap(t *testing.T) {
	read := map[string]bool{}
	for _, name := range keepTools {
		read[name] = true
	}
	for _, name := range writeKeepTools {
		if read[name] {
			t.Errorf("tool %q is in both keepTools and writeKeepTools; a mutating call would then skip the confirmation card", name)
		}
	}
}

func TestLoadTool_ReturnsRealSchema(t *testing.T) {
	view := loadTestToolset(t).NewView()
	out, isErr := view.Execute(context.Background(), "", LoadToolTool, `{"names":["listDatabaseBackups","restoreDatabase"]}`)
	if isErr {
		t.Fatalf("load_tool failed: %s", out)
	}
	for _, want := range []string{"listDatabaseBackups", "restoreDatabase", "schema:", `"properties"`} {
		if !strings.Contains(out, want) {
			t.Errorf("load_tool output is missing %q, the model cannot build a call from it:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "changes state") {
		t.Error("load_tool must mark mutating tools so the model knows a confirmation card follows")
	}
}

func TestLoadTool_UnknownNameIsAnHonestError(t *testing.T) {
	view := loadTestToolset(t).NewView()
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

func TestCallTool_ValidatesArgumentsAgainstTheRealSchema(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView()
	out, isErr := view.Execute(context.Background(), "", CallToolTool, `{"name":"getProject","arguments":{}}`)
	if !isErr {
		t.Fatalf("a call missing a required argument must fail locally, not reach the backend: %s", out)
	}
	if !strings.Contains(out, "schema:") {
		t.Errorf("a validation error must carry the schema so the model fixes itself on the next attempt, got: %s", out)
	}
}

func TestCallTool_UnknownInnerName(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewView()
	out, isErr := view.Execute(context.Background(), "", CallToolTool, `{"name":"totallyMadeUpTool","arguments":{}}`)
	if !isErr {
		t.Fatalf("call_tool of a non-existent tool must fail: %s", out)
	}
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("the error must name the problem, got: %s", out)
	}
}

func TestCallTool_AcceptsStringifiedArguments(t *testing.T) {
	name, args, ok := loadTestToolset(t).NewView().Resolve(CallToolTool, `{"name":"getProject","arguments":"{\"projectId\":\"p1\"}"}`)
	if !ok {
		t.Fatal("models frequently send arguments as a JSON string; that must resolve, not dead-end")
	}
	if name != "getProject" {
		t.Fatalf("resolved to %q, want getProject", name)
	}
	if !strings.Contains(args, "p1") {
		t.Fatalf("the stringified arguments were lost: %s", args)
	}
}

func TestResolve_UnwrapsWriteToolFromCallTool(t *testing.T) {
	view := loadTestToolset(t).NewView()
	name, _, ok := view.Resolve(CallToolTool, `{"name":"restartApp","arguments":{"appId":"a1"}}`)
	if !ok || name != "restartApp" {
		t.Fatalf("Resolve returned (%q, %v), want restartApp: a write wrapped in call_tool must be visible to the confirmation gate", name, ok)
	}
	if !view.IsWrite(name) {
		t.Fatal("the unwrapped tool must classify as a write, otherwise the mutation runs without a confirmation card")
	}
}

func TestReadOnlyView_RefusesWritesAndHidesThemFromTheCatalog(t *testing.T) {
	view := loadTestToolsetAt(t, "http://127.0.0.1:1").NewReadOnlyView()
	out, isErr := view.Execute(context.Background(), "", CallToolTool, `{"name":"restartApp","arguments":{"appId":"a1"}}`)
	if !isErr {
		t.Fatalf("a read-only session must refuse a write: %s", out)
	}
	for _, name := range view.CatalogNames() {
		if view.IsWrite(name) {
			t.Errorf("write tool %q is listed to a read-only session; it would be proposed and then refused", name)
		}
	}
}

func TestCatalogNames_CoversCapabilitiesAndExcludesTheBaseSet(t *testing.T) {
	view := loadTestToolset(t).NewView()
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
