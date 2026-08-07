package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

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
	// probeAppNetwork execs a fixed, non-shell-templated diagnostic sequence in
	// an ephemeral debug container, not the app's own container: it changes
	// nothing about the app and reads no state a write tool normally exposes,
	// but exec-in-pod belongs on the write path like the rest of this list, not
	// in keepTools alongside pure API reads. It stays out of riskyWriteTools:
	// nothing is destroyed, spent, exposed, or minted, so ModeEdit runs it
	// without a confirmation card, the same as restartApp.
	"probeAppNetwork",

	"createApp", "createProject", "ensureDefaultProject",
	"connectGitRepo",
	"addDomainAuthorization", "verifyDomainAuthorization", "attachHostname",
	"upsertManagedRecord",
	"createDatabaseBackup", "restoreDatabase", "downloadDatabaseBackup",
	"bulkSetEnvVars", "createDeployHook",
}

// denyTools are operations that reveal secret material. The deny-list wins over
// both allowlists: a denied tool is never registered at all, so it is neither
// callable nor listed in the catalog the model is shown.
var denyTools = map[string]bool{
	"revealEnvVar":           true,
	"getDatabaseCredentials": true,
	"getS3BucketCredentials": true,
	"revealModelApiKey":      true,
}

// riskyWriteTools are the writes that always cost the user a confirmation card,
// whatever the mode. A write lands here when undoing it is impossible, slow or
// expensive: it destroys or overwrites data, spends money, exposes something
// publicly, changes where a domain points, or mints a credential. Everything
// else in writeKeepTools is an operational action the user can simply repeat or
// reverse from the console, so in edit mode it runs without interrupting them.
var riskyWriteTools = map[string]bool{
	"restoreDatabase":        true,
	"downloadDatabaseBackup": true,
	"createDeployHook":       true,
	"createDatabase":         true,
	"createS3Bucket":         true,
	"createEndpoint":         true,
	"createApp":              true,
	"createProject":          true,
	"attachHostname":         true,
	"upsertManagedRecord":    true,
	"connectGitRepo":         true,
	"deleteEnvVar":           true,
	"updateAppStorage":       true,
	"promoteDeployment":      true,
}

// Mode is how much autonomy the user granted this session. It comes from the
// switcher in the chat input bar and is sent with every request.
//
// ModeManual confirms every write, ModeEdit (the default) confirms only
// riskyWriteTools, ModeAdmin confirms nothing.
type Mode string

const (
	ModeManual Mode = "manual"
	ModeEdit   Mode = "edit"
	ModeAdmin  Mode = "admin"
)

// ParseMode maps the wire value onto a mode, falling back to ModeEdit for an
// empty or unknown string: an unrecognised mode must never be read as "more
// autonomy than the user asked for".
func ParseMode(s string) Mode {
	switch Mode(strings.TrimSpace(strings.ToLower(s))) {
	case ModeManual:
		return ModeManual
	case ModeAdmin:
		return ModeAdmin
	default:
		return ModeEdit
	}
}

const SupportTicketTool = "create_support_ticket"

const supportTicketRoute = "agent-chat"

// LoadToolTool is how a tool from the catalog becomes callable. Loading a name
// appends that tool's real definition to this view, so from the next round on
// the model calls it natively -- same function-calling path, same schema, same
// provider-side argument decoding as a base tool. Nothing is wrapped and no
// arguments travel as free-form JSON inside another tool's payload.
//
// The catalog is not shipped whole because serialized in full its 90 schemas
// are ~12.6k tokens on every gateway call of every round, and because a
// 90-entry array measurably degrades which tool a model picks. Loading grows
// the array by the two or three tools a turn actually needs.
const LoadToolTool = "load_tool"

const maxLoadToolNames = 8

const maxLoadToolChars = 12000

// maxLoadedTools bounds how far one turn may grow its tools array. It is a
// backstop against a model that loads by the handful every round, not a budget
// a real turn is expected to reach.
const maxLoadedTools = 16

const maxToolResultChars = 24000

// OpenPageTool moves the console the user is looking at to another page. The
// product runs the whole application, so an answer that ends in "go to
// /projects/{id}/git" is a step the user has to perform by hand for no reason:
// the assistant can put them on that page and then talk about what they see.
//
// It is deliberately not a write tool. Nothing on the platform changes, the
// destination is checked against the real route table before anything is sent,
// and the client refuses a path it cannot render -- so the worst outcome is a
// page the user did not want, one browser Back away.
const OpenPageTool = "open_console_page"

// IsMetaTool reports whether a tool name is a client-side meta-tool that does
// not touch the backend. The loop must not charge those against the per-turn
// tool-call budget, otherwise reaching for a tool costs the user an answer.
func IsMetaTool(name string) bool {
	return name == LoadToolTool || name == OpenPageTool
}

var loadToolDef = llmchat.ToolDef{
	Type: "function",
	Function: llmchat.ToolFunctionDef{
		Name:        LoadToolTool,
		Description: "Make one or more tools from the catalog available to you. Pass the catalog names; from your next message on you can call those tools directly, like the ones you already have. Load a tool before you need it, not after.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"names": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Catalog tool names to read, exactly as spelled in the catalog.",
				},
			},
			"required": []string{"names"},
		},
	},
}

var openPageDef = llmchat.ToolDef{
	Type: "function",
	Function: llmchat.ToolFunctionDef{
		Name:        OpenPageTool,
		Description: "Open a console page for the user right now: their current tab moves to that page as you answer. Call this instead of asking the user to navigate somewhere themselves, whenever the page is where the rest of your answer happens. Pass a path from the console page list with the placeholders filled in with real ids, for example /projects/<projectId>/git/import. Only ever move a user somewhere they asked to go or somewhere your answer sends them, once per turn, and say in one short sentence what they are now looking at.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Console path to open, starting with /, no scheme and no host. Every placeholder must already be replaced with a real id.",
				},
			},
			"required": []string{"path"},
		},
	},
}

// baseTools ship their full schema in every prompt of every session. The set is
// the grounding path -- who the user is, what they own, what state it is in --
// and stays small on purpose: everything else costs nothing until the model
// reads it with load_tool.
var baseTools = []string{
	"listProjects", "getProject",
	"listApps", "getAppState",
	"listDatabases",
	"getCurrentUser",
	"searchLogs",
}

// argDenyFields lists request-body fields the assistant must never fill in, per
// tool. connectGitRepo accepts a user OAuth token, but repository access comes
// from the installed GitHub App: the assistant has no business asking the user
// for a token in chat, so the field is stripped before the call goes out.
var argDenyFields = map[string]map[string]bool{
	"connectGitRepo": {"token": true},
}

// Toolset is the full curated catalog: every allowlisted tool, its handler and
// its read/write classification. It is built once at boot and shared across
// requests.
//
// Defs holds the definitions of the entire catalog. What a turn actually sends
// to the model is ToolView.Defs(), a fixed set of base tools plus the two
// meta-tools; the full catalog is reachable through them by name.
type Toolset struct {
	Defs []llmchat.ToolDef

	handlers  map[string]internalmcp.ToolHandler
	writeSet  map[string]bool
	defByName map[string]llmchat.ToolDef
	order     []string
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

// CatalogNames lists the catalog tools the model is told about by name only.
// Base tools are excluded because their full definitions are already in the
// prompt. When allowWrite is false the mutating half of the catalog is not
// listed at all: a caller without write access must not be shown capabilities
// it cannot exercise, and ToolView refuses to dispatch them anyway.
func (ts *Toolset) CatalogNames(allowWrite bool) []string {
	base := make(map[string]bool, len(baseTools))
	for _, name := range baseTools {
		base[name] = true
	}
	out := make([]string, 0, len(ts.order))
	for _, name := range ts.order {
		if base[name] {
			continue
		}
		if !allowWrite && ts.writeSet[name] {
			continue
		}
		out = append(out, name)
	}
	return out
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

// ToolView is the per-turn window onto a Toolset: the base tools, plus
// load_tool, plus whatever the model has loaded so far in this turn. Loaded
// tools are real definitions in the tools array, so the model calls them the
// normal way; the view only decides which ones are in front of it.
//
// AllowWrite is the mode gate. With it false the view dispatches read tools
// only, and the catalog the model is shown lists nothing else.
type ToolView struct {
	ts          *Toolset
	AllowWrite  bool
	Mode        Mode
	loaded      map[string]bool
	loadedOrder []string
	navigate    func(path string) bool
	navigated   bool
}

// SetNavigator hands the view the client's own navigation, used by
// OpenPageTool. The callback owns both halves of the decision: it reports
// whether the path is a page the console really serves, and it is what actually
// moves the user. A view without one still advertises the tool and answers the
// model that this client cannot move it, which is the honest result for a
// caller that has no live stream to the browser.
func (v *ToolView) SetNavigator(fn func(path string) bool) {
	v.navigate = fn
}

// Navigated reports whether this turn already moved the user's tab. The caller
// needs it to tell a turn that used OpenPageTool from one that only wrote about
// a page: only the second kind may still be finished by the server.
func (v *ToolView) Navigated() bool {
	return v.navigated
}

// NewView opens a per-turn view with write dispatch allowed, in the given mode.
// Mode decides only which writes stop for a confirmation card; it never widens
// what the caller's own bearer is allowed to do.
func (ts *Toolset) NewView(mode Mode) *ToolView {
	return &ToolView{ts: ts, AllowWrite: true, Mode: mode, loaded: map[string]bool{}}
}

// NewReadOnlyView opens a view for a caller that holds no write scope.
func (ts *Toolset) NewReadOnlyView() *ToolView {
	return &ToolView{ts: ts, AllowWrite: false, Mode: ModeManual, loaded: map[string]bool{}}
}

// NeedsConfirmation reports whether a call must stop for the user's explicit
// approval before it runs.
func (v *ToolView) NeedsConfirmation(name string) bool {
	if !v.ts.IsWrite(name) {
		return false
	}
	switch v.Mode {
	case ModeAdmin:
		return false
	case ModeEdit:
		return riskyWriteTools[name]
	default:
		return true
	}
}

// Defs returns the definitions exposed to the model: the base tools, load_tool,
// and every tool loaded so far, each with its own real schema.
func (v *ToolView) Defs() []llmchat.ToolDef {
	out := make([]llmchat.ToolDef, 0, len(baseTools)+len(v.loadedOrder)+1)
	for _, name := range baseTools {
		if def, ok := v.ts.defByName[name]; ok {
			out = append(out, def)
		}
	}
	for _, name := range v.loadedOrder {
		if def, ok := v.ts.defByName[name]; ok {
			out = append(out, def)
		}
	}
	out = append(out, loadToolDef, openPageDef)
	return out
}

// Load makes catalog tools callable without going through the model, used to
// restore what a paused turn had already loaded before it stopped for a
// confirmation. Names the view may not dispatch are ignored.
func (v *ToolView) Load(names ...string) {
	for _, name := range names {
		v.load(name)
	}
}

func (v *ToolView) load(name string) bool {
	if v.loaded[name] || !v.ts.Has(name) {
		return false
	}
	if v.ts.IsWrite(name) && !v.AllowWrite {
		return false
	}
	if len(v.loadedOrder) >= maxLoadedTools {
		return false
	}
	v.loaded[name] = true
	v.loadedOrder = append(v.loadedOrder, name)
	return true
}

// LoadedNames returns the tools this view has loaded, in load order.
func (v *ToolView) LoadedNames() []string {
	return append([]string{}, v.loadedOrder...)
}

func (v *ToolView) Has(name string) bool {
	return v.ts.Has(name) || name == LoadToolTool || name == OpenPageTool
}

func (v *ToolView) IsWrite(name string) bool {
	return v.ts.IsWrite(name)
}

// CatalogNames is the name-only catalog for this view's permissions.
func (v *ToolView) CatalogNames() []string {
	return v.ts.CatalogNames(v.AllowWrite)
}

// Execute is the single tool entry point for a turn. It serves load_tool
// locally and dispatches everything else onto the catalog, answering an unknown
// name by naming the catalog rather than leaving the model at a dead end.
func (v *ToolView) Execute(ctx context.Context, bearer, name, argsJSON string) (text string, isError bool) {
	if name == LoadToolTool {
		return v.loadTools(argsJSON)
	}
	if name == OpenPageTool {
		return v.openPage(argsJSON)
	}
	return v.dispatch(ctx, bearer, name, argsJSON)
}

// openPage moves the user's console to path.
//
// One move per turn: a model that keeps calling this would drag the page around
// under the user's hands while they are still reading the previous answer, and
// the second destination is never the one they asked for. The refusal names the
// page they are already on so the model can fall back to writing the path out.
func (v *ToolView) openPage(argsJSON string) (string, bool) {
	var args struct {
		Path string `json:"path"`
	}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("%s arguments are not valid JSON: %v", OpenPageTool, err), true
		}
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return fmt.Sprintf("%s requires a path, for example /projects/<projectId>/apps", OpenPageTool), true
	}
	if v.navigate == nil {
		return "this client cannot open pages for the user; write the path out in your answer instead", true
	}
	if v.navigated {
		return "the user was already moved to another page this turn; leave them there and write any further path out in your answer instead", true
	}
	if !v.navigate(path) {
		return fmt.Sprintf("%q is not a page this console serves, so the user was not moved. Use a path from the console page list with every placeholder replaced by a real id.", path), true
	}
	v.navigated = true
	return fmt.Sprintf("The user's console is now showing %s. Say in one short sentence what they are looking at; do not ask them to navigate there.", path), false
}

func (v *ToolView) dispatch(ctx context.Context, bearer, name, argsJSON string) (string, bool) {
	if !v.ts.Has(name) {
		return fmt.Sprintf("unknown tool %q. Use only names from the catalog, and make one callable with %s first.", name, LoadToolTool), true
	}
	if v.ts.IsWrite(name) && !v.AllowWrite {
		return fmt.Sprintf("%s changes state and this session has read-only access; tell the user what would need to change and let them do it.", name), true
	}
	if err := validateAgainstSchema(v.ts.defByName[name].Function.Parameters, argsJSON); err != nil {
		return fmt.Sprintf("arguments for %s are invalid: %v\nschema:\n%s", name, err, schemaJSON(v.ts.defByName[name].Function.Parameters)), true
	}
	return v.ts.Execute(ctx, bearer, name, argsJSON)
}

func (v *ToolView) loadTools(argsJSON string) (string, bool) {
	var args struct {
		Names []string `json:"names"`
	}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("load_tool arguments are not valid JSON: %v", err), true
		}
	}
	names := make([]string, 0, len(args.Names))
	for _, n := range args.Names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "load_tool requires a non-empty names array holding catalog tool names", true
	}
	if len(names) > maxLoadToolNames {
		names = names[:maxLoadToolNames]
	}

	var sb strings.Builder
	for _, n := range names {
		def, ok := v.ts.defByName[n]
		if !ok {
			fmt.Fprintf(&sb, "%s: not in the catalog. Use a name exactly as listed there.\n", n)
			continue
		}
		if v.ts.IsWrite(n) && !v.AllowWrite {
			fmt.Fprintf(&sb, "%s: not available, this session has read-only access.\n", n)
			continue
		}
		if !v.loaded[n] && !v.load(n) {
			fmt.Fprintf(&sb, "%s: not loaded, this turn already holds %d tools. Finish with those or answer with what you have.\n", n, maxLoadedTools)
			continue
		}
		fmt.Fprintf(&sb, "%s%s is now available: %s\n", n, v.writeMarker(n), oneLine(def.Function.Description))
	}
	out := strings.TrimRight(sb.String(), "\n")
	if len(out) > maxLoadToolChars {
		out = out[:maxLoadToolChars] + "\n... [truncated, load fewer tools at a time]"
	}
	return out, false
}

// writeMarker tells the model what calling this tool actually does under the
// current mode, so it neither promises a confirmation card that will not appear
// nor treats an autonomous write as harmless.
func (v *ToolView) writeMarker(name string) string {
	if !v.ts.IsWrite(name) {
		return ""
	}
	if v.NeedsConfirmation(name) {
		return " (changes state; the user confirms it before it runs)"
	}
	return " (changes state immediately)"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func schemaJSON(schema map[string]any) string {
	if len(schema) == 0 {
		return "{}"
	}
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// validateAgainstSchema checks a tool call against the tool's own swagger
// schema before it reaches the backend. Loaded tools are native definitions, so
// the provider already constrains them, but not every gateway route enforces
// that, and the backend's own 400 says nothing the model can act on. It checks
// what a model actually gets wrong -- a missing required field or a value of
// the wrong JSON type -- and deliberately does not attempt full JSON Schema:
// a false rejection here would block a legal call outright.
func validateAgainstSchema(schema map[string]any, argsJSON string) error {
	if len(schema) == 0 {
		return nil
	}
	args := map[string]any{}
	if trimmed := strings.TrimSpace(argsJSON); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return fmt.Errorf("arguments are not a JSON object: %w", err)
		}
	}

	var missing []string
	for _, req := range schemaRequired(schema) {
		if v, ok := args[req]; !ok || v == nil {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}

	props, _ := schema["properties"].(map[string]any)
	for name, raw := range args {
		propSchema, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		if !valueMatchesSchemaType(propSchema["type"], raw) {
			return fmt.Errorf("field %q must be of type %v", name, propSchema["type"])
		}
	}
	return nil
}

func schemaRequired(schema map[string]any) []string {
	switch req := schema["required"].(type) {
	case []string:
		return req
	case []any:
		out := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func valueMatchesSchemaType(declared any, value any) bool {
	switch t := declared.(type) {
	case string:
		return jsonTypeMatches(t, value)
	case []any:
		for _, alt := range t {
			if s, ok := alt.(string); ok && jsonTypeMatches(s, value) {
				return true
			}
		}
		return false
	}
	return true
}

func jsonTypeMatches(declared string, value any) bool {
	switch declared {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "null":
		return value == nil
	}
	return true
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
// credential until revoked, so a verbatim copy in the chat archive (neither
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
