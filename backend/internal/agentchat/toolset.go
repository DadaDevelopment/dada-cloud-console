package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dada-tuda/console/backend/internal/llmchat"
	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// keepTools is the read-only allowlist of backend operationIds the console
// assistant may call without a confirmation card. Every entry must match an
// operationId in the embedded swagger spec: ApplyOverrides silently drops
// unknown keep-names, so a typo costs the assistant a capability without any
// error, which is exactly how the assistant ended up telling users to go to the
// UI. TestKeepTools_AllRegisteredAndClassifiedAsRead guards that.
//
// Deliberately absent: downloadAppDirectory and downloadAppFile (binary
// octet-stream bodies would be dumped into the model context), getBoxConnection
// (hands out one-time access to a box), diagnoseApp and triggerAutofix (they
// call the LLM gateway themselves, so an LLM turn would recurse into another
// paid LLM run), every /admin/* and /mlflow/* operation, ingestLogs and
// ingestMetrics, and everything in denyTools.
var keepTools = []string{
	"listProjects", "getProject",
	"listApps", "getAppState", "getAppLogs", "getAppMetrics",
	"listDeployments", "listBuilds", "getBuild",
	"listEnvVars", "listHostnames", "listEndpoints", "listDatabases",
	"listOperations", "getOperation",
	"searchLogs", "getProjectCost", "getCurrentUser",
	"downloadSourceArchive",
	"submitFeedback",
	"deleteAppImpact", "deleteProjectImpact", "moveAppImpact",

	"getProjectQuotas", "getBillingPlans", "getBillingUsage",
	"getProjectConsumption", "getBillingAccount", "recommendBillingPlan",

	"listInfra", "listGitRepos", "listDeployHooks",

	"listAppFiles", "readAppFile", "getAppVolumeUsage",

	"listDatabaseBackups",

	"listDomainAuthorizations", "getManagedZone", "listManagedRecords", "previewZoneImport",

	"listS3Buckets",

	"listGitInstallations", "listAvailableInstallations", "listInstallationRepos",
	"getGitInstallUrl", "detectFramework", "detectPublicFramework",

	"listBoxes", "getBox", "getBoxState", "getBoxCatalog", "getBoxUsage",
	"listBoxAttachments", "listBoxCrystallizations",

	"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics",

	"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage",
}

// writeKeepTools is the allowlist of mutating operations the assistant may
// propose. Every one of them is paused by the loop and rendered as a
// confirmation card, so the user, not the model, performs the mutation.
//
// createAppServer stays out on purpose: it provisions a real, billed virtual
// machine via Terraform. Box lifecycle (createBox, boxUp, crystallizeBox) also
// stays out because it burns metered minutes; boxes are read-only from chat.
//
// downloadDatabaseBackup is here rather than in keepTools even though it
// mutates nothing: it mints a presigned SigV4 GET on the whole pg_dump that
// anyone holding the URL can fetch without a Keycloak session. Exfiltrating a
// customer database must cost the user one deliberate click, not a silent
// read-tool call the model made up on its own. The backend agrees: the handler
// requires write access and audits every download.
var writeKeepTools = []string{
	"restartApp", "triggerBuild", "deployTrigger", "cancelBuild", "retryOperation",
	"setEnvVar", "deleteEnvVar",
	"rollbackApp", "rollbackDeployment", "promoteDeployment", "updateAppImage",
	"updateAppProfile", "updateAppStorage",
	"createDatabase",
	"createEndpoint", "createS3Bucket",

	"createApp", "createProject", "ensureDefaultProject",
	"connectGitRepo",
	"addDomainAuthorization", "verifyDomainAuthorization", "attachHostname",
	"upsertManagedRecord",
	"createDatabaseBackup", "restoreDatabase", "downloadDatabaseBackup",
	"bulkSetEnvVars", "createDeployHook",
}

// denyTools are operations that reveal secret material. The deny-list wins over
// both allowlists: a denied tool is never registered at all, so it can neither
// be called nor discovered through search_tools.
var denyTools = map[string]bool{
	"revealEnvVar":           true,
	"getDatabaseCredentials": true,
	"getS3BucketCredentials": true,
	"revealModelApiKey":      true,
}

const SupportTicketTool = "create_support_ticket"

const supportTicketRoute = "agent-chat"

// SearchToolsTool is the meta-tool name the model calls to discover tools that
// are in the catalog but not yet in its per-turn tool list.
const SearchToolsTool = "search_tools"

const maxSearchResults = 12

const maxSearchDescriptionChars = 240

const maxSearchCallsPerTurn = 4

const maxToolResultChars = 24000

// IsMetaTool reports whether a tool name is a client-side meta-tool that does
// not touch the backend. The loop must not charge those against the per-turn
// tool-call budget, otherwise discovering a tool costs the user an answer.
func IsMetaTool(name string) bool {
	return name == SearchToolsTool
}

var searchToolsDef = llmchat.ToolDef{
	Type: "function",
	Function: llmchat.ToolFunctionDef{
		Name:        SearchToolsTool,
		Description: "Search the full catalog of platform tools by keyword (English or Russian) when the tool you need is not in your current tool list. Matching tools become callable immediately in this turn. Call this BEFORE ever telling the user that something is impossible or must be done in the console UI.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keywords describing the capability, e.g. 'domain dns', 'database backup restore', 'container files', 'box sandbox', 'github repo', 'billing quota plan', 's3 bucket', 'ai gateway key'.",
				},
			},
			"required": []string{"query"},
		},
	},
}

// baseTools is the navigation-and-inventory set every turn starts with. Adding
// to it is expensive: each entry ships its full JSON schema in every prompt of
// every turn. Everything else is reached through SearchToolsTool.
var baseTools = []string{
	"listProjects", "getProject",
	"listApps", "getAppState",
	"listDatabases",
	"getCurrentUser",
	"searchLogs",
	SupportTicketTool,
}

// searchAliases maps a query-term fragment to catalog tool names. Tool
// descriptions come from the English swagger summaries while users write
// Russian, so keyword search alone misses the most common asks. Keys of three
// runes or fewer match a term exactly; longer keys match as a substring, so
// "домен" also catches "домены" and "доменов".
//
// Keys must be word STEMS, not dictionary forms: users type "удали", not
// "удалить", so the key is "удал". A miss here is not a cosmetic ranking
// problem. The system prompt makes search_tools the mandatory gate in front of
// the word "нельзя", so an empty result set is read by the model as proof that
// the capability does not exist, and it denies a live feature.
var searchAliases = map[string][]string{
	"домен":    {"addDomainAuthorization", "verifyDomainAuthorization", "listDomainAuthorizations", "attachHostname", "listHostnames", "getManagedZone", "listManagedRecords", "upsertManagedRecord", "previewZoneImport"},
	"поддомен": {"addDomainAuthorization", "attachHostname", "listHostnames", "upsertManagedRecord"},
	"domain":   {"addDomainAuthorization", "verifyDomainAuthorization", "listDomainAuthorizations", "attachHostname", "listHostnames", "getManagedZone", "listManagedRecords", "upsertManagedRecord", "previewZoneImport"},
	"dns":      {"addDomainAuthorization", "verifyDomainAuthorization", "listDomainAuthorizations", "getManagedZone", "listManagedRecords", "upsertManagedRecord", "previewZoneImport"},

	"бэкап":     {"listDatabaseBackups", "createDatabaseBackup", "restoreDatabase", "downloadDatabaseBackup"},
	"бекап":     {"listDatabaseBackups", "createDatabaseBackup", "restoreDatabase", "downloadDatabaseBackup"},
	"backup":    {"listDatabaseBackups", "createDatabaseBackup", "restoreDatabase", "downloadDatabaseBackup"},
	"restore":   {"listDatabaseBackups", "restoreDatabase", "downloadDatabaseBackup"},
	"восстанов": {"listDatabaseBackups", "restoreDatabase", "downloadDatabaseBackup"},
	"откат":     {"rollbackApp", "rollbackDeployment", "restoreDatabase", "listDatabaseBackups"},

	"файл":    {"listAppFiles", "readAppFile", "getAppVolumeUsage", "updateAppStorage"},
	"file":    {"listAppFiles", "readAppFile", "getAppVolumeUsage", "updateAppStorage"},
	"датасет": {"listAppFiles", "readAppFile", "getAppVolumeUsage"},
	"том":     {"listAppFiles", "getAppVolumeUsage", "updateAppStorage"},
	"volume":  {"listAppFiles", "getAppVolumeUsage", "updateAppStorage"},

	"бокс":     {"listBoxes", "getBox", "getBoxState", "getBoxCatalog", "getBoxUsage", "listBoxAttachments", "listBoxCrystallizations"},
	"box":      {"listBoxes", "getBox", "getBoxState", "getBoxCatalog", "getBoxUsage", "listBoxAttachments", "listBoxCrystallizations"},
	"песочниц": {"listBoxes", "getBox", "getBoxState", "getBoxCatalog", "getBoxUsage"},
	"sandbox":  {"listBoxes", "getBox", "getBoxState", "getBoxCatalog", "getBoxUsage"},

	"гит":       {"listGitInstallations", "listAvailableInstallations", "listInstallationRepos", "getGitInstallUrl", "connectGitRepo", "listGitRepos", "detectFramework", "detectPublicFramework"},
	"git":       {"listGitInstallations", "listAvailableInstallations", "listInstallationRepos", "getGitInstallUrl", "connectGitRepo", "listGitRepos", "detectFramework", "detectPublicFramework"},
	"репозитор": {"listGitInstallations", "listAvailableInstallations", "listInstallationRepos", "getGitInstallUrl", "connectGitRepo", "listGitRepos", "detectFramework", "detectPublicFramework"},
	"github":    {"listGitInstallations", "listAvailableInstallations", "listInstallationRepos", "getGitInstallUrl", "connectGitRepo", "listGitRepos", "detectPublicFramework"},
	"repo":      {"listGitInstallations", "listInstallationRepos", "getGitInstallUrl", "connectGitRepo", "listGitRepos", "detectFramework", "detectPublicFramework"},

	"деплой":  {"createApp", "createProject", "connectGitRepo", "triggerBuild", "deployTrigger"},
	"deploy":  {"createApp", "createProject", "connectGitRepo", "triggerBuild", "deployTrigger"},
	"создать": {"createApp", "createProject", "connectGitRepo", "createDatabase", "createS3Bucket"},
	"create":  {"createApp", "createProject", "connectGitRepo", "createDatabase", "createS3Bucket"},
	"развер":  {"createApp", "createProject", "connectGitRepo", "triggerBuild", "deployTrigger"},
	"захост":  {"createApp", "createProject", "connectGitRepo", "triggerBuild", "deployTrigger"},
	"поднят":  {"createApp", "createProject", "connectGitRepo", "triggerBuild", "deployTrigger"},

	"приложен": {"createApp", "listApps", "getAppState", "updateAppProfile"},
	"аппа":     {"createApp", "listApps", "getAppState", "updateAppProfile"},
	"app":      {"createApp", "listApps", "getAppState", "updateAppProfile"},
	"сервис":   {"createApp", "listApps", "getAppState", "updateAppProfile"},
	"бот":      {"createApp", "listApps", "getAppState", "updateAppProfile"},

	"баз":      {"listDatabases", "createDatabase", "listDatabaseBackups", "restoreDatabase"},
	"бд":       {"listDatabases", "createDatabase", "listDatabaseBackups", "restoreDatabase"},
	"database": {"listDatabases", "createDatabase", "listDatabaseBackups", "restoreDatabase"},
	"postgres": {"listDatabases", "createDatabase", "listDatabaseBackups", "restoreDatabase"},
	"постгрес": {"listDatabases", "createDatabase", "listDatabaseBackups", "restoreDatabase"},
	"redis":    {"listDatabases", "createDatabase", "listInfra"},
	"редис":    {"listDatabases", "createDatabase", "listInfra"},

	"счет":    {"getBillingUsage", "getBillingPlans", "getBillingAccount", "getProjectConsumption", "getProjectQuotas", "recommendBillingPlan", "getProjectCost"},
	"счёт":    {"getBillingUsage", "getBillingPlans", "getBillingAccount", "getProjectConsumption", "getProjectQuotas", "recommendBillingPlan", "getProjectCost"},
	"деньг":   {"getBillingUsage", "getBillingPlans", "getBillingAccount", "getProjectConsumption", "getProjectCost"},
	"тариф":   {"getBillingPlans", "getBillingAccount", "recommendBillingPlan", "getProjectQuotas"},
	"план":    {"getBillingPlans", "getBillingAccount", "recommendBillingPlan", "getProjectQuotas"},
	"биллинг": {"getBillingUsage", "getBillingPlans", "getBillingAccount", "getProjectConsumption", "getProjectQuotas", "recommendBillingPlan", "getProjectCost"},
	"квот":    {"getProjectQuotas", "getBillingPlans", "recommendBillingPlan"},
	"лимит":   {"getProjectQuotas", "getBillingPlans", "recommendBillingPlan"},
	"billing": {"getBillingUsage", "getBillingPlans", "getBillingAccount", "getProjectConsumption", "getProjectQuotas", "recommendBillingPlan", "getProjectCost"},
	"plan":    {"getBillingPlans", "getBillingAccount", "recommendBillingPlan", "getProjectQuotas"},
	"quota":   {"getProjectQuotas", "getBillingPlans", "recommendBillingPlan"},
	"price":   {"getBillingPlans", "getProjectCost", "getProjectConsumption", "recommendBillingPlan"},
	"cost":    {"getProjectCost", "getProjectConsumption", "getBillingUsage", "getBillingPlans"},

	"s3":       {"listS3Buckets", "createS3Bucket", "updateAppStorage", "getAppVolumeUsage"},
	"хранилищ": {"listS3Buckets", "createS3Bucket", "updateAppStorage", "getAppVolumeUsage"},
	"bucket":   {"listS3Buckets", "createS3Bucket"},
	"storage":  {"listS3Buckets", "createS3Bucket", "updateAppStorage", "getAppVolumeUsage"},

	"vm":       {"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics"},
	"вм":       {"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics"},
	"виртуалк": {"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics"},
	"сервер":   {"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics"},
	"server":   {"listAppServers", "getAppServer", "getAppServerState", "getAppServerMetrics"},

	"ai":     {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"ллм":    {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"llm":    {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"нейрос": {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"ключ":   {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"key":    {"getAIGatewayCatalog", "listAIGatewayKeys", "getProjectAIUsage"},
	"токен":  {"listDeployHooks", "createDeployHook", "listAIGatewayKeys"},

	"env":      {"listEnvVars", "setEnvVar", "deleteEnvVar", "bulkSetEnvVars"},
	"переменн": {"listEnvVars", "setEnvVar", "deleteEnvVar", "bulkSetEnvVars"},
	"secret":   {"listEnvVars", "setEnvVar", "deleteEnvVar", "bulkSetEnvVars"},

	"хук":  {"listDeployHooks", "createDeployHook", "deployTrigger"},
	"hook": {"listDeployHooks", "createDeployHook", "deployTrigger"},
	"ci":   {"listDeployHooks", "createDeployHook", "deployTrigger"},

	"архив":    {"downloadSourceArchive"},
	"исходн":   {"downloadSourceArchive"},
	"код":      {"downloadSourceArchive"},
	"скача":    {"downloadSourceArchive", "downloadDatabaseBackup"},
	"выгруз":   {"downloadSourceArchive", "downloadDatabaseBackup"},
	"zip":      {"downloadSourceArchive"},
	"source":   {"downloadSourceArchive"},
	"archive":  {"downloadSourceArchive"},
	"download": {"downloadSourceArchive", "downloadDatabaseBackup"},

	"удал":   {"deleteAppImpact", "deleteProjectImpact", "deleteEnvVar"},
	"снест":  {"deleteAppImpact", "deleteProjectImpact"},
	"снос":   {"deleteAppImpact", "deleteProjectImpact"},
	"delete": {"deleteAppImpact", "deleteProjectImpact", "deleteEnvVar"},
	"remove": {"deleteAppImpact", "deleteProjectImpact", "deleteEnvVar"},
	"impact": {"deleteAppImpact", "deleteProjectImpact", "moveAppImpact"},

	"перенес": {"moveAppImpact"},
	"перенос": {"moveAppImpact"},
	"переезд": {"moveAppImpact"},
	"move":    {"moveAppImpact"},
}

// argDenyFields lists request-body fields the assistant must never fill in, per
// tool. connectGitRepo accepts a user OAuth token, but repository access comes
// from the installed GitHub App: the assistant has no business asking the user
// for a token in chat, so the field is stripped before the call goes out.
var argDenyFields = map[string]map[string]bool{
	"connectGitRepo": {"token": true},
}

type toolSearchEntry struct {
	name      string
	lowerName string
	lowerDesc string
}

// Toolset is the full curated catalog: every allowlisted tool, its handler and
// its read/write classification. It is built once at boot and shared across
// requests.
//
// The exported Defs field holds definitions of the ENTIRE catalog. Sending all
// of them in every prompt is exactly what NewView exists to avoid, but the
// field stays exported and complete so callers not yet migrated to ToolView
// keep working unchanged. Per-turn tool exposure belongs to ToolView.Defs().
type Toolset struct {
	Defs []llmchat.ToolDef

	handlers  map[string]internalmcp.ToolHandler
	writeSet  map[string]bool
	defByName map[string]llmchat.ToolDef
	order     []string
	index     []toolSearchEntry
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

	ts := &Toolset{
		handlers:  map[string]internalmcp.ToolHandler{},
		writeSet:  writeSet,
		defByName: map[string]llmchat.ToolDef{},
	}
	for _, t := range curated {
		if denyTools[t.Name] {
			continue
		}
		def := llmchat.ToolDef{
			Type: "function",
			Function: llmchat.ToolFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
		ts.Defs = append(ts.Defs, def)
		ts.defByName[t.Name] = def
		ts.order = append(ts.order, t.Name)
		ts.index = append(ts.index, toolSearchEntry{
			name:      t.Name,
			lowerName: strings.ToLower(t.Name),
			lowerDesc: strings.ToLower(t.Description),
		})
		ts.handlers[t.Name] = internalmcp.MakeHandler(t, backendURL, spec.BasePath)
	}

	ts.Defs = append(ts.Defs, searchToolsDef)
	ts.defByName[SearchToolsTool] = searchToolsDef

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

	argsJSON = sanitizeArgs(name, argsJSON)

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
	return truncateToolResult(sb.String()), res.IsError
}

// searchCatalog ranks catalog tool names against a free-form query. An alias
// hit outweighs a name match, which outweighs a description match. search_tools
// itself is not in the index, so the search never finds itself.
func (ts *Toolset) searchCatalog(query string) []string {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return nil
	}

	score := map[string]int{}
	for _, term := range terms {
		for key, names := range searchAliases {
			if !aliasMatches(term, key) {
				continue
			}
			for _, n := range names {
				if _, ok := ts.defByName[n]; ok {
					score[n] += 5
				}
			}
		}
		for _, e := range ts.index {
			switch {
			case strings.Contains(e.lowerName, term):
				score[e.name] += 3
			case strings.Contains(e.lowerDesc, term):
				score[e.name]++
			}
		}
	}

	out := make([]string, 0, len(score))
	for name, s := range score {
		if s > 0 {
			out = append(out, name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if score[out[i]] != score[out[j]] {
			return score[out[i]] > score[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > maxSearchResults {
		out = out[:maxSearchResults]
	}
	return out
}

// aliasMatches decides whether a query term hits an alias key. Three tiers,
// each picked to keep Russian word forms reachable without dragging in
// accidental substrings:
//
//   - keys of two runes or fewer ("бд", "вм", "ai", "s3", "ci") match exactly,
//     because anything looser turns them into noise generators;
//   - keys of exactly three runes match as a PREFIX, so "баз" reaches "базу"
//     and "базы" (an exact rule missed every inflected form, which is how
//     "откатить базу" failed to find the backup list) while "том" still does
//     not fire on "поэтому";
//   - longer keys match anywhere in the term, so "домен" catches "поддомена".
func aliasMatches(term, key string) bool {
	switch utf8.RuneCountInString(key) {
	case 0, 1, 2:
		return term == key
	case 3:
		return strings.HasPrefix(term, key)
	}
	return strings.Contains(term, key)
}

func searchTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if utf8.RuneCountInString(f) < 2 {
			continue
		}
		out = append(out, f)
		if len(out) == 8 {
			break
		}
	}
	return out
}

// ToolView is the per-turn window onto a Toolset. It starts with baseTools plus
// search_tools and grows as the model discovers or correctly guesses names, so
// the prompt carries a handful of schemas instead of the whole catalog.
type ToolView struct {
	ts          *Toolset
	active      map[string]bool
	order       []string
	searchCalls int
}

// NewView opens a fresh per-turn view. One view per HTTP request: a confirm
// resume arrives as a separate request, so use ActivateFromHistory there to
// restore what the previous request discovered.
func (ts *Toolset) NewView() *ToolView {
	v := &ToolView{
		ts:     ts,
		active: map[string]bool{SearchToolsTool: true},
		order:  []string{SearchToolsTool},
	}
	for _, name := range baseTools {
		if _, ok := ts.defByName[name]; !ok {
			continue
		}
		if v.active[name] {
			continue
		}
		v.active[name] = true
		v.order = append(v.order, name)
	}
	return v
}

// Defs returns the definitions exposed to the model for this turn.
func (v *ToolView) Defs() []llmchat.ToolDef {
	out := make([]llmchat.ToolDef, 0, len(v.order))
	for _, name := range v.order {
		if def, ok := v.ts.defByName[name]; ok {
			out = append(out, def)
		}
	}
	return out
}

func (v *ToolView) Has(name string) bool {
	return v.ts.Has(name) || name == SearchToolsTool
}

func (v *ToolView) IsWrite(name string) bool {
	return v.ts.IsWrite(name)
}

func (v *ToolView) IsActive(name string) bool {
	return v.active[name]
}

// Activate adds a catalog tool to this turn's exposed set. It reports false for
// names that do not exist in the catalog or that are already active.
func (v *ToolView) Activate(name string) bool {
	if _, ok := v.ts.defByName[name]; !ok {
		return false
	}
	if v.active[name] {
		return false
	}
	v.active[name] = true
	v.order = append(v.order, name)
	return true
}

func (v *ToolView) ActiveNames() []string {
	out := make([]string, len(v.order))
	copy(out, v.order)
	return out
}

// ActivateFromHistory re-activates every tool the conversation already called.
// Without it a confirm-card resume would drop everything search_tools found in
// the previous request and the assistant would claim the capability is gone.
func (v *ToolView) ActivateFromHistory(messages []llmchat.Message) {
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			v.Activate(call.Function.Name)
		}
	}
}

// Execute is the single tool entry point for a turn. It serves the search_tools
// meta-tool locally, auto-activates a known but not-yet-exposed tool the model
// guessed correctly, and answers an unknown name with a pointer to search_tools
// instead of a dead end.
func (v *ToolView) Execute(ctx context.Context, bearer, name, argsJSON string) (text string, isError bool) {
	if name == SearchToolsTool {
		v.searchCalls++
		if v.searchCalls > maxSearchCallsPerTurn {
			return "search_tools call limit reached for this turn; work with the tools you already have", true
		}
		var args struct {
			Query string `json:"query"`
		}
		if strings.TrimSpace(argsJSON) != "" {
			_ = json.Unmarshal([]byte(argsJSON), &args)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "search_tools requires a non-empty query argument", true
		}
		return v.searchAndActivate(args.Query), false
	}

	if !v.ts.Has(name) {
		return fmt.Sprintf("unknown tool %q; call %s with a keyword to find the right tool name, do not invent tool names", name, SearchToolsTool), true
	}

	v.Activate(name)
	return v.ts.Execute(ctx, bearer, name, argsJSON)
}

func (v *ToolView) searchAndActivate(query string) string {
	names := v.ts.searchCatalog(query)
	if len(names) == 0 {
		return fmt.Sprintf("search_tools found no tool matching %q. Do not invent a tool name. Catalog families: projects, apps, builds, deployments, env vars, databases and backups, domains and DNS, app files, S3 storage, boxes, app servers (VMs), billing and quotas, git repositories, logs, AI gateway.", query)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "search_tools matched %d tool(s) for %q; they are callable now:", len(names), query)
	for _, name := range names {
		v.Activate(name)
		sb.WriteString("\n- ")
		sb.WriteString(name)
		sb.WriteString(": ")
		sb.WriteString(shortDescription(v.ts.defByName[name].Function.Description))
	}
	if len(names) == maxSearchResults {
		sb.WriteString("\n(top matches only; refine the query for more)")
	}
	return sb.String()
}

func shortDescription(desc string) string {
	oneLine := strings.Join(strings.Fields(strings.ReplaceAll(desc, "\n", " ")), " ")
	if utf8.RuneCountInString(oneLine) <= maxSearchDescriptionChars {
		return oneLine
	}
	runes := []rune(oneLine)
	return string(runes[:maxSearchDescriptionChars]) + "..."
}

// sanitizeArgs drops argument fields the assistant is never allowed to fill in.
func sanitizeArgs(toolName, argsJSON string) string {
	denied, ok := argDenyFields[toolName]
	if !ok || strings.TrimSpace(argsJSON) == "" {
		return argsJSON
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return argsJSON
	}
	changed := false
	for field := range denied {
		if _, present := args[field]; present {
			delete(args, field)
			changed = true
		}
	}
	if !changed {
		return argsJSON
	}
	b, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(b)
}

// mintedSecretTools maps a tool to the notice that replaces its result
// everywhere the result outlives the turn. These operations hand back material
// the platform itself keeps only hashed: createDeployHook's plaintext bearer
// token is returned exactly once by the API and is a permanent deploy
// credential until revoked, so a verbatim copy in agent_chat_messages (neither
// encrypted nor pruned) outlives the reason it was minted.
var mintedSecretTools = map[string]string{
	"createDeployHook": "deploy-hook token issued; the plaintext token is shown once in this turn and is not archived. If it was lost, revoke the hook and mint a new one.",
}

// presignedResultTools are tools whose result carries a presigned URL: the
// query string is a self-contained capability that needs no session, so it must
// not be archived even though the URL's origin and path are safe and useful for
// support. The path survives, the signature does not.
//
// downloadSourceArchive stays a read tool on purpose -- handing a user back
// their own uploaded source is an explicit promise of the system prompt and the
// highest-weight eval case -- but its link is still a bearer capability and is
// scrubbed on the way to storage just like the backup one.
var presignedResultTools = map[string]bool{
	"downloadDatabaseBackup": true,
	"downloadSourceArchive":  true,
}

const redactedQueryMarker = "[signature redacted]"

// RedactToolResult returns the form of a tool result that is safe to persist or
// trace. The caller must keep passing the ORIGINAL text to the model for the
// current turn: the point is that the credential lives exactly as long as the
// turn that needed it, not that the model is kept in the dark.
//
// Every future key-minting operation belongs in mintedSecretTools; that is the
// one place to add it, and TestRedactToolResult_CoversEveryKeyMintingWriteTool
// checks such a tool is also confirmation-gated.
func RedactToolResult(name, text string) string {
	if notice, ok := mintedSecretTools[name]; ok {
		if strings.TrimSpace(text) == "" {
			return text
		}
		return RedactedMarker + " " + notice
	}
	if presignedResultTools[name] {
		return stripURLQueries(text)
	}
	return text
}

// stripURLQueries rewrites every absolute http(s) URL in text so that its query
// string is replaced by a marker. It is deliberately textual rather than
// JSON-aware: a tool result may be a JSON body, a plain error line or a
// truncated fragment of either, and the signature must die in all three.
func stripURLQueries(text string) string {
	var sb strings.Builder
	rest := text
	for {
		i := indexURLScheme(rest)
		if i < 0 {
			sb.WriteString(rest)
			return sb.String()
		}
		sb.WriteString(rest[:i])
		rest = rest[i:]
		end := strings.IndexFunc(rest, isURLTerminator)
		if end < 0 {
			end = len(rest)
		}
		url := rest[:end]
		if q := strings.IndexByte(url, '?'); q >= 0 {
			url = url[:q] + "?" + redactedQueryMarker
		}
		sb.WriteString(url)
		rest = rest[end:]
	}
}

func indexURLScheme(s string) int {
	https := strings.Index(s, "https://")
	http := strings.Index(s, "http://")
	switch {
	case https < 0:
		return http
	case http < 0:
		return https
	case http < https:
		return http
	default:
		return https
	}
}

func isURLTerminator(r rune) bool {
	switch r {
	case '"', '\'', '<', '>', '\\', '`', ',', ';':
		return true
	}
	return unicode.IsSpace(r)
}

// truncateToolResult caps a tool result so that readAppFile, listAppFiles or
// searchLogs cannot blow the model's context with tens of kilobytes.
func truncateToolResult(s string) string {
	if len(s) <= maxToolResultChars {
		return s
	}
	return s[:maxToolResultChars] + "\n... [truncated]"
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
