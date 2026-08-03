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

const SupportTicketTool = "create_support_ticket"

const supportTicketRoute = "agent-chat"

// LoadToolTool is the meta-tool the model calls to read the real JSON schema of
// catalog tools it wants to use. The schema lands in the conversation as a tool
// result, never in the tools array: tool definitions are serialized into the
// head of the prompt (tools, then system, then messages), so mutating the array
// mid-session would invalidate the whole prefix cache on every discovery.
const LoadToolTool = "load_tool"

// CallToolTool invokes any catalog tool by name. Together with LoadToolTool it
// replaces the previous keyword search: the model picks a tool by reading the
// honest catalog and the honest schema, not by matching word fragments.
const CallToolTool = "call_tool"

const maxLoadToolNames = 8

const maxLoadToolChars = 12000

const maxToolResultChars = 24000

// IsMetaTool reports whether a tool name is a client-side meta-tool that does
// not touch the backend. The loop must not charge those against the per-turn
// tool-call budget, otherwise reading a schema costs the user an answer.
// CallToolTool is deliberately absent: it performs a real backend call and is
// charged as the tool it dispatches to.
func IsMetaTool(name string) bool {
	return name == LoadToolTool
}

var loadToolDef = llmchat.ToolDef{
	Type: "function",
	Function: llmchat.ToolFunctionDef{
		Name:        LoadToolTool,
		Description: "Read the full JSON schema and documentation of one or more platform tools listed in the catalog. Use it before calling a tool you have not used yet in this conversation, so that you send correct arguments.",
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

var callToolDef = llmchat.ToolDef{
	Type: "function",
	Function: llmchat.ToolFunctionDef{
		Name:        CallToolTool,
		Description: "Call any platform tool from the catalog by name. Arguments are validated against the tool's real schema; on a mismatch the schema is returned so the call can be corrected and retried.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Catalog tool name.",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments for that tool, matching its schema.",
				},
			},
			"required": []string{"name"},
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

// ToolView is the per-turn window onto a Toolset. Unlike its predecessor it
// never grows: Defs() returns the same fixed list for every round of every
// turn, which is the whole point -- the tools block sits in front of the system
// prompt in the serialized request, so a set that changes mid-conversation
// invalidates the prompt cache for everything behind it.
//
// AllowWrite is the mode gate. With it false the view dispatches read tools
// only, and the catalog the model is shown lists nothing else.
type ToolView struct {
	ts         *Toolset
	AllowWrite bool
}

// NewView opens a per-turn view with write dispatch allowed; the loop still
// pauses every write for a confirmation card, so this is not an approval.
func (ts *Toolset) NewView() *ToolView {
	return &ToolView{ts: ts, AllowWrite: true}
}

// NewReadOnlyView opens a view for a caller that holds no write scope.
func (ts *Toolset) NewReadOnlyView() *ToolView {
	return &ToolView{ts: ts, AllowWrite: false}
}

// Defs returns the definitions exposed to the model. Fixed for the lifetime of
// the session: base tools plus load_tool and call_tool.
func (v *ToolView) Defs() []llmchat.ToolDef {
	out := make([]llmchat.ToolDef, 0, len(baseTools)+2)
	for _, name := range baseTools {
		if def, ok := v.ts.defByName[name]; ok {
			out = append(out, def)
		}
	}
	out = append(out, loadToolDef, callToolDef)
	return out
}

func (v *ToolView) Has(name string) bool {
	return v.ts.Has(name) || name == LoadToolTool || name == CallToolTool
}

func (v *ToolView) IsWrite(name string) bool {
	return v.ts.IsWrite(name)
}

// CatalogNames is the name-only catalog for this view's permissions.
func (v *ToolView) CatalogNames() []string {
	return v.ts.CatalogNames(v.AllowWrite)
}

// Resolve maps a model tool call onto the backend tool it actually performs.
// A direct call resolves to itself; call_tool resolves to its wrapped name and
// arguments. ok is false when the call carries no dispatchable tool, in which
// case Execute produces the honest error for the model.
//
// The loop resolves before classifying a call as a write, otherwise every
// mutation wrapped in call_tool would slip past the confirmation gate.
func (v *ToolView) Resolve(name, argsJSON string) (string, string, bool) {
	if name != CallToolTool {
		return name, argsJSON, v.ts.Has(name)
	}
	inner, innerArgs, err := parseCallToolArgs(argsJSON)
	if err != nil || !v.ts.Has(inner) {
		return name, argsJSON, false
	}
	return inner, innerArgs, true
}

// Execute is the single tool entry point for a turn. It serves load_tool
// locally, dispatches call_tool onto the catalog after validating arguments
// against the tool's real schema, and answers an unknown name by naming the
// catalog rather than leaving the model at a dead end.
func (v *ToolView) Execute(ctx context.Context, bearer, name, argsJSON string) (text string, isError bool) {
	switch name {
	case LoadToolTool:
		return v.loadTools(argsJSON)
	case CallToolTool:
		inner, innerArgs, err := parseCallToolArgs(argsJSON)
		if err != nil {
			return fmt.Sprintf("call_tool arguments are not valid: %v. Send {\"name\": \"<catalog tool>\", \"arguments\": {...}}.", err), true
		}
		return v.dispatch(ctx, bearer, inner, innerArgs)
	}

	return v.dispatch(ctx, bearer, name, argsJSON)
}

func (v *ToolView) dispatch(ctx context.Context, bearer, name, argsJSON string) (string, bool) {
	if !v.ts.Has(name) {
		return fmt.Sprintf("unknown tool %q. Use only names from the catalog and read the schema with %s first.", name, LoadToolTool), true
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
			fmt.Fprintf(&sb, "%s: not in the catalog. Use a name exactly as listed there.\n\n", n)
			continue
		}
		if v.ts.IsWrite(n) && !v.AllowWrite {
			fmt.Fprintf(&sb, "%s: not available, this session has read-only access.\n\n", n)
			continue
		}
		fmt.Fprintf(&sb, "%s%s\n%s\nschema:\n%s\n\n", n, writeMarker(v.ts.IsWrite(n)), oneLine(def.Function.Description), schemaJSON(def.Function.Parameters))
	}
	out := strings.TrimRight(sb.String(), "\n")
	if len(out) > maxLoadToolChars {
		out = out[:maxLoadToolChars] + "\n... [truncated, load fewer tools at a time]"
	}
	return out, false
}

func writeMarker(isWrite bool) string {
	if isWrite {
		return " (changes state; the user confirms it before it runs)"
	}
	return ""
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

// parseCallToolArgs unpacks a call_tool payload. The arguments field is
// accepted both as an object and as a JSON string holding one: models emit the
// stringified form often enough that rejecting it would burn a round for
// nothing.
func parseCallToolArgs(argsJSON string) (string, string, error) {
	if strings.TrimSpace(argsJSON) == "" {
		return "", "", fmt.Errorf("no arguments given")
	}
	var payload struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &payload); err != nil {
		return "", "", err
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return "", "", fmt.Errorf("name is required")
	}

	inner := strings.TrimSpace(string(payload.Arguments))
	if inner == "" || inner == "null" {
		return name, "{}", nil
	}
	if strings.HasPrefix(inner, `"`) {
		var unquoted string
		if err := json.Unmarshal(payload.Arguments, &unquoted); err == nil {
			inner = strings.TrimSpace(unquoted)
			if inner == "" {
				return name, "{}", nil
			}
		}
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(inner), &probe); err != nil {
		return name, "", fmt.Errorf("arguments must be an object: %w", err)
	}
	return name, inner, nil
}

// validateAgainstSchema checks a tool call against the tool's own swagger
// schema before it reaches the backend. It exists because call_tool arguments
// do not go through the provider's constrained decoding the way a native tool
// definition does, so the schema has to be enforced on this side. It checks
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
