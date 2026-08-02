package agentchat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/llmchat"
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
	for _, want := range []string{"createApp", SearchToolsTool, "listProjects"} {
		if !names[want] {
			t.Errorf("Toolset.Defs must stay the full catalog (loop.go still reads the field directly), missing %q", want)
		}
	}
	if len(ts.Defs) != len(ts.order)+1 {
		t.Errorf("Toolset.Defs = %d entries, want %d curated tools + search_tools", len(ts.Defs), len(ts.order)+1)
	}
}

func TestBaseView_IsSmall(t *testing.T) {
	ts := loadTestToolset(t)
	defs := ts.NewView().Defs()
	if len(defs) < 8 || len(defs) > 14 {
		t.Fatalf("base view exposes %d tools, want between 8 and 14: the whole point of lazy loading is a small prompt", len(defs))
	}
	got := map[string]bool{}
	for _, d := range defs {
		got[d.Function.Name] = true
	}
	if !got[SearchToolsTool] {
		t.Error("base view must expose search_tools, otherwise nothing else is discoverable")
	}
	for _, name := range baseTools {
		if !got[name] {
			t.Errorf("base view is missing base tool %q", name)
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

func TestSearchAliases_AllTargetsExistInCatalog(t *testing.T) {
	ts := loadTestToolset(t)
	for key, names := range searchAliases {
		for _, name := range names {
			if !ts.Has(name) {
				t.Errorf("search alias %q points at %q which is not in the catalog: the search would advertise a tool the model cannot call", key, name)
			}
		}
	}
}

func TestSearchTools_FindsAndActivatesRussian(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	if view.IsActive("addDomainAuthorization") {
		t.Fatal("addDomainAuthorization must not be active before a search")
	}
	text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"домен biba.ru"}`)
	if isErr {
		t.Fatalf("search_tools reported an error: %s", text)
	}
	if !strings.Contains(text, "addDomainAuthorization") {
		t.Fatalf("search for a Russian domain question did not surface addDomainAuthorization, got:\n%s", text)
	}
	if !view.IsActive("addDomainAuthorization") {
		t.Fatal("search_tools must activate what it returns, otherwise the model still cannot call it")
	}
	found := false
	for _, d := range view.Defs() {
		if d.Function.Name == "addDomainAuthorization" {
			found = true
		}
	}
	if !found {
		t.Fatal("activated tool did not appear in view.Defs()")
	}
}

func TestSearchTools_FindsEnglish(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"database backup restore"}`)
	if isErr {
		t.Fatalf("search_tools reported an error: %s", text)
	}
	for _, want := range []string{"listDatabaseBackups", "restoreDatabase"} {
		if !strings.Contains(text, want) {
			t.Errorf("search %q did not surface %q, got:\n%s", "database backup restore", want, text)
		}
	}
}

func TestSearchTools_NoMatch(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	before := len(view.Defs())
	text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"quantum flux capacitor"}`)
	if isErr {
		t.Fatalf("an empty result is not a tool error, got isError=true: %s", text)
	}
	if !strings.Contains(text, "found no tool") {
		t.Fatalf("expected a no-match explanation, got:\n%s", text)
	}
	if len(view.Defs()) != before {
		t.Fatalf("a no-match search changed the active set: %d -> %d", before, len(view.Defs()))
	}
}

func TestSearchTools_EmptyQuery(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	_, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{}`)
	if !isErr {
		t.Fatal("search_tools without a query must report an error so the model retries with one")
	}
}

func TestSearchTools_CallLimit(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	for i := 0; i < maxSearchCallsPerTurn; i++ {
		if _, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"domain"}`); isErr {
			t.Fatalf("search call %d unexpectedly errored", i+1)
		}
	}
	text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"domain"}`)
	if !isErr {
		t.Fatal("search_tools must stop after the per-turn limit")
	}
	if !strings.Contains(text, "limit") {
		t.Fatalf("limit message should say so, got: %s", text)
	}
}

func TestSearchTools_NeverReturnsDenyTools(t *testing.T) {
	ts := loadTestToolset(t)
	for name := range denyTools {
		view := ts.NewView()
		text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"`+name+`"}`)
		if isErr {
			t.Fatalf("search_tools errored for query %q: %s", name, text)
		}
		if strings.Contains(text, "- "+name+":") {
			t.Errorf("search_tools leaked deny-listed tool %q into its results:\n%s", name, text)
		}
		if view.IsActive(name) {
			t.Errorf("deny-listed tool %q became active", name)
		}
	}
}

func TestSearchTools_DoesNotReturnItself(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	text, isErr := view.Execute(context.Background(), "", SearchToolsTool, `{"query":"search tools"}`)
	if isErr {
		t.Fatalf("search_tools errored: %s", text)
	}
	if strings.Contains(text, "- "+SearchToolsTool+":") {
		t.Fatalf("search_tools must not list itself:\n%s", text)
	}
}

func TestToolView_AutoActivatesKnownInactiveTool(t *testing.T) {
	ts := loadTestToolsetAt(t, "http://127.0.0.1:1")
	view := ts.NewView()
	if view.IsActive("listHostnames") {
		t.Fatal("listHostnames should not be part of the base set")
	}
	text, _ := view.Execute(context.Background(), "", "listHostnames", `{}`)
	if strings.Contains(text, "unknown tool") {
		t.Fatalf("a catalog tool the model guessed correctly must be executed, not rejected: %s", text)
	}
	if !view.IsActive("listHostnames") {
		t.Fatal("a successfully guessed tool must stay active for the rest of the turn")
	}
}

func TestToolView_UnknownToolGivesSearchHint(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	text, isErr := view.Execute(context.Background(), "", "deployMyThing", `{}`)
	if !isErr {
		t.Fatal("an unknown tool name must be reported as an error")
	}
	if !strings.Contains(text, "unknown tool") || !strings.Contains(text, SearchToolsTool) {
		t.Fatalf("the unknown-tool message must point at search_tools instead of dead-ending, got: %s", text)
	}
}

func TestActivateFromHistory(t *testing.T) {
	ts := loadTestToolset(t)
	view := ts.NewView()
	msgs := []llmchat.Message{
		{Role: "user", Content: "покажи бэкапы"},
		{Role: "assistant", ToolCalls: []llmchat.ToolCall{
			{ID: "1", Function: llmchat.ToolCallFunction{Name: "listDatabaseBackups"}},
			{ID: "2", Function: llmchat.ToolCallFunction{Name: "thisToolNeverExisted"}},
		}},
	}
	view.ActivateFromHistory(msgs)
	if !view.IsActive("listDatabaseBackups") {
		t.Fatal("a tool already called in this conversation must stay callable after a confirm-card resume")
	}
	if view.IsActive("thisToolNeverExisted") {
		t.Fatal("history must not activate a tool that does not exist")
	}
}

// TestSearchAliases_EvalCorpusQueries drives searchCatalog with the verbatim
// "Ввод юзера" lines from docs/product/agent-eval-personas-and-cases.md. The
// system prompt makes search_tools a hard gate in front of the word "нельзя",
// so an empty or wrong result set is what turns a live capability into a false
// denial. Each case names the test case id it comes from.
func TestSearchAliases_EvalCorpusQueries(t *testing.T) {
	ts := loadTestToolset(t)
	cases := []struct {
		tc    string
		query string
		want  []string
	}{
		{"TC-35", "я снёс папку на компе, а в облаке сервис работает. код можно как-то достать обратно?", []string{"downloadSourceArchive"}},
		{"TC-11", "удали всё лишнее, а то бардак", []string{"deleteAppImpact"}},
		{"TC-37", "перенеси landing из нашего проекта в проект клиента", []string{"moveAppImpact"}},
		{"TC-29", "клиент снёс таблицу, можно откатить базу на вчера?", []string{"listDatabaseBackups", "restoreDatabase"}},
		{"TC-24", "мне нужно закинуть датасет прямо в контейнер, файлы как открыть", []string{"listAppFiles", "readAppFile"}},
		{"TC-30", "домен клиента не подхватывается, висит уже сутки", []string{"listDomainAuthorizations", "verifyDomainAuthorization"}},
		{"TC-28", "подними мне бокс на час, надо агенту рутовую среду", []string{"listBoxes", "getBoxCatalog"}},
		{"TC-38", "мне нужен ключ к вашей ллм, из приложения дёргать", []string{"listAIGatewayKeys"}},
		{"TC-04", "у меня репа на гитхабе github.com/vasya/shop-api, задеплой её", []string{"connectGitRepo"}},
		{"TC-15", "нужен постгрес и редис, накинь оба", []string{"createDatabase"}},
		{"TC-21", "перезалить архив", []string{"downloadSourceArchive"}},
		{"TC-21", "выгрузить исходники", []string{"downloadSourceArchive"}},
		{"TC-11", "удалить приложение", []string{"deleteAppImpact"}},
		{"TC-37", "перенести приложение в другой проект", []string{"moveAppImpact"}},
	}
	for _, c := range cases {
		got := ts.searchCatalog(c.query)
		have := map[string]bool{}
		for _, n := range got {
			have[n] = true
		}
		for _, want := range c.want {
			if !have[want] {
				t.Errorf("%s: search %q did not surface %q; got %v", c.tc, c.query, want, got)
			}
		}
	}
}

func TestSearchAliases_DeleteAndMoveImpactAreDiscoverableInRussian(t *testing.T) {
	ts := loadTestToolset(t)
	for _, q := range []string{"удали", "удалить", "удаление", "снести", "delete", "remove"} {
		got := ts.searchCatalog(q)
		found := false
		for _, n := range got {
			if n == "deleteAppImpact" {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q must reach deleteAppImpact (TC-11 requires impact before any delete advice); got %v", q, got)
		}
	}
	for _, q := range []string{"перенеси", "перенести", "переезд", "move"} {
		got := ts.searchCatalog(q)
		found := false
		for _, n := range got {
			if n == "moveAppImpact" {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q must reach moveAppImpact (TC-37 requires the impact call); got %v", q, got)
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
	if !IsMetaTool(SearchToolsTool) {
		t.Fatal("search_tools is a meta-tool and must not be charged to the backend tool budget")
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
