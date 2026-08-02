package agentchat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxTraceTextLen is the cap applied to free-form turn text (input message,
// assistant answer) before it is persisted or shipped to a tracing backend.
const MaxTraceTextLen = 4000

// MaxToolErrorLen is the cap applied to a failing tool's result text when it is
// stored as the tool span's error.
const MaxToolErrorLen = 1000

// TurnKind distinguishes the three entry points that produce a turn: a plain
// chat message, a resumed loop, and a confirmation of a pending write.
type TurnKind string

const (
	TurnKindTurn    TurnKind = "turn"
	TurnKindResume  TurnKind = "resume"
	TurnKindConfirm TurnKind = "confirm"
)

// TurnOutcome is how a turn ended. It is the primary grouping key of the eval
// harness, so the set is closed and matches the outcome column of
// agent_chat_turns.
type TurnOutcome string

const (
	OutcomeAnswered       TurnOutcome = "answered"
	OutcomePendingConfirm TurnOutcome = "pending_confirm"
	OutcomeError          TurnOutcome = "error"
	OutcomeDailyCap       TurnOutcome = "daily_cap"
	OutcomeNotConfigured  TurnOutcome = "not_configured"
)

// ToolSpan is one tool invocation inside a turn, in call order. Args are
// already redacted; Error is only set when the tool reported a failure.
// Preflight marks a call the engine made on its own before the first LLM call,
// which is the difference between "the agent looked" and "the model asked".
type ToolSpan struct {
	Name       string         `json:"name"`
	Args       map[string]any `json:"args,omitempty"`
	OK         bool           `json:"ok"`
	Error      string         `json:"error,omitempty"`
	DurationMs int            `json:"duration_ms"`
	ResultLen  int            `json:"result_len"`
	Preflight  bool           `json:"preflight,omitempty"`
}

// TurnTrace accumulates everything worth knowing about a single agent turn. It
// deliberately knows nothing about the database or about any tracing vendor:
// the HTTP layer fills it, then hands it to a persister and to a trace
// exporter. A nil *TurnTrace is safe to call every method on, so the caller
// never needs a guard around instrumentation.
type TurnTrace struct {
	TraceID uuid.UUID
	Kind    TurnKind

	UserSub   string
	OrgID     string
	ProjectID *uuid.UUID
	EnvID     *uuid.UUID

	InputMessage string
	OutputText   string

	ToolSpans      []ToolSpan
	WriteCallCount int
	PreflightCalls int

	Usage Usage

	Outcome   TurnOutcome
	ErrorCode string

	PendingToolName string
	PendingArgs     map[string]any

	ContextProjectPresent bool
	ContextAppPresent     bool
	InventoryApps         *int
	InventoryProjects     *int

	StartedAt  time.Time
	FinishedAt time.Time

	absorbed int
}

// NewTurnTrace starts a trace for the given kind, minting the trace id that
// also identifies the trace in the external tracing backend.
func NewTurnTrace(kind TurnKind) *TurnTrace {
	return &TurnTrace{
		TraceID:   uuid.New(),
		Kind:      kind,
		StartedAt: time.Now(),
	}
}

// RecordTool appends one already-built tool span. Used by the confirm path,
// which executes a tool outside the ReAct loop and therefore has no tool log.
func (t *TurnTrace) RecordTool(span ToolSpan) {
	if t == nil {
		return
	}
	t.ToolSpans = append(t.ToolSpans, span)
}

// AbsorbToolLog converts the loop's tool log into spans. It only reads entries
// it has not seen before, so calling it repeatedly with the same growing slice
// never duplicates. A log shorter than what was already absorbed is treated as
// a different log and taken whole: losing tool calls silently would be worse
// than recording a few twice, since the eval counts calls per turn.
func (t *TurnTrace) AbsorbToolLog(log []ToolLogEntry) {
	if t == nil {
		return
	}
	if t.absorbed > len(log) {
		t.absorbed = 0
	}
	for _, e := range log[t.absorbed:] {
		t.ToolSpans = append(t.ToolSpans, toolSpanFromLog(e))
		t.absorbInventory(e)
	}
	t.absorbed = len(log)
}

// SetUsage records the LLM usage of this turn. It assigns rather than adds:
// RunTurn and ResumeTurn are separate turns and get separate rows.
func (t *TurnTrace) SetUsage(u Usage) {
	if t == nil {
		return
	}
	t.Usage = u
}

// EnsureModel names the model the turn was sent to when the gateway never got
// far enough to report one back. Without it a turn that failed upstream is
// stored with an empty model, which is exactly the row where knowing the model
// matters most: a provider that refuses one model group and serves another is
// indistinguishable from a dead gateway.
func (t *TurnTrace) EnsureModel(model string) {
	if t == nil || t.Usage.Model != "" {
		return
	}
	t.Usage.Model = model
}

// AbsorbResult folds a whole TurnResult into the trace: usage, tool log, budget
// counters and the inventory the engine established for itself. It is the one
// call the HTTP layer needs after RunTurn or ResumeTurn, and it is safe on a
// turn that ended in an error, because the loop returns a populated result
// alongside the error.
//
// Inventory counts come from the result rather than from parsing tool output:
// InventoryAppsLookedUp is the engine's own answer to "did listApps actually
// run", so a nil InventoryApps in the trace means the lookup never happened,
// never that it happened and found nothing.
func (t *TurnTrace) AbsorbResult(res TurnResult) {
	if t == nil {
		return
	}
	t.SetUsage(res.Usage)
	t.AbsorbToolLog(res.ToolLog)
	t.WriteCallCount = res.WriteCallCount
	t.PreflightCalls = res.PreflightCalls
	if res.InventoryAppsLookedUp {
		apps := res.InventoryApps
		t.InventoryApps = &apps
	}
	if res.InventoryProjects > 0 || res.InventoryAppsLookedUp {
		projects := res.InventoryProjects
		t.InventoryProjects = &projects
	}
	if res.AssistantText != "" {
		t.OutputText = res.AssistantText
	}
}

// Finish stamps the outcome and end time. The first call wins, so a path that
// finishes and then falls into a shared tail does not overwrite its own verdict
// or inflate its latency.
func (t *TurnTrace) Finish(outcome TurnOutcome, errorCode string) {
	if t == nil || !t.FinishedAt.IsZero() {
		return
	}
	t.FinishedAt = time.Now()
	t.Outcome = outcome
	t.ErrorCode = errorCode
}

// LatencyMs is the wall time of the turn, measured to now if the trace has not
// been finished yet. Never negative.
func (t *TurnTrace) LatencyMs() int {
	if t == nil || t.StartedAt.IsZero() {
		return 0
	}
	end := t.FinishedAt
	if end.IsZero() {
		end = time.Now()
	}
	ms := int(end.Sub(t.StartedAt).Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

// ToolCallCount is the number of tool invocations recorded for this turn.
func (t *TurnTrace) ToolCallCount() int {
	if t == nil {
		return 0
	}
	return len(t.ToolSpans)
}

// ToolCallsJSON renders the tool spans for a JSONB column. It never returns
// null: an empty turn serialises to an empty array.
func (t *TurnTrace) ToolCallsJSON() []byte {
	if t == nil || len(t.ToolSpans) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(t.ToolSpans)
	if err != nil || len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func toolSpanFromLog(e ToolLogEntry) ToolSpan {
	span := ToolSpan{
		Name:       e.Name,
		Args:       RedactArgs(e.ArgsJSON),
		OK:         !e.IsError,
		DurationMs: int(e.DurationMs),
		ResultLen:  len(e.Result),
		Preflight:  e.Preflight,
	}
	if e.IsError {
		span.Error = TruncateForTrace(RedactToolResult(e.Name, e.Result), MaxToolErrorLen)
	}
	return span
}

func (t *TurnTrace) absorbInventory(e ToolLogEntry) {
	if e.IsError {
		return
	}
	switch e.Name {
	case "listApps":
		if n, ok := countJSONArrayField(e.Result, "apps"); ok {
			t.InventoryApps = &n
		}
	case "listProjects":
		if n, ok := countJSONArrayField(e.Result, "projects"); ok {
			t.InventoryProjects = &n
		}
	}
}

func countJSONArrayField(result, field string) (int, bool) {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return 0, false
	}
	if strings.HasPrefix(trimmed, "[") {
		var bare []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &bare); err != nil {
			return 0, false
		}
		return len(bare), true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return 0, false
	}
	raw, ok := obj[field]
	if !ok {
		return 0, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return 0, false
	}
	return len(items), true
}

// TruncateForTrace shortens s to at most max bytes, marking that it was cut.
// Used so a 200 KB tool result never reaches the database or the network.
//
// The cut lands on a rune boundary. Slicing bytes would split a multi-byte rune
// -- on Cyrillic text that happens roughly half the time -- and Postgres then
// rejects the whole INSERT with 22021 invalid byte sequence for encoding
// "UTF8", losing the entire turn trace rather than a few characters of it.
func TruncateForTrace(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return RuneSafeCut(s, max) + "... [truncated]"
}

// RuneSafeCut returns the longest prefix of s that fits in maxBytes bytes
// without splitting a rune. A budget too small for the first rune yields an
// empty string, never a partial one.
func RuneSafeCut(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

var secretArgKeys = map[string]bool{
	"value":           true,
	"env":             true,
	"password":        true,
	"secret":          true,
	"token":           true,
	"api_key":         true,
	"apikey":          true,
	"private_key":     true,
	"privatekey":      true,
	"ssh_private_key": true,
	"sshprivatekey":   true,
	"credentials":     true,
}

// RedactedMarker replaces every secret-bearing value in a redacted argument
// map. Callers assert on it instead of hardcoding the literal.
const RedactedMarker = "[redacted]"

// RedactArgs parses a tool's raw JSON arguments and replaces the values of
// known secret-bearing keys at any depth. Returns nil for empty or unparseable
// input, so a malformed argument blob is dropped rather than stored verbatim.
func RedactArgs(argsJSON string) map[string]any {
	if strings.TrimSpace(argsJSON) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil
	}
	return RedactArgsMap(args)
}

// RedactArgsJSON redacts a raw JSON argument blob and returns JSON text, for
// callers that ship arguments onwards as a string (an SSE frame, a pending_args
// column) instead of as a decoded map. Blank input is returned unchanged.
// Input that is not valid JSON becomes "{}": an unparseable blob may still hold
// a secret, so it is dropped rather than passed through. Unlike RedactArgs this
// accepts any JSON value, not just an object.
func RedactArgsJSON(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		return argsJSON
	}
	var v any
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return "{}"
	}
	b, err := json.Marshal(RedactValue(v))
	if err != nil {
		return "{}"
	}
	return string(b)
}

// RedactValue redacts an arbitrary decoded JSON value, walking objects and
// arrays to any depth and returning a copy. Scalars pass through untouched.
func RedactValue(v any) any {
	return redactArgValue(v)
}

// RedactArgsMap redacts an already-decoded argument map, returning a copy. It
// walks nested objects and arrays at any depth, so a bulkSetEnvVars payload
// (vars[].value) is redacted just like a flat setEnvVar one: the value of an
// env var must never leave the encrypted column it lives in, whatever nesting
// the tool schema wraps it in.
//
// An object carrying both "key" and "value" is the canonical env var pair, so
// its "value" is redacted on shape alone, independently of secretArgKeys.
func RedactArgsMap(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	_, isPair := args["key"]
	if isPair {
		_, isPair = args["value"]
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		lower := strings.ToLower(k)
		if secretArgKeys[lower] || (isPair && lower == "value") {
			out[k] = RedactedMarker
			continue
		}
		out[k] = redactArgValue(v)
	}
	return out
}

func redactArgValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return RedactArgsMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactArgValue(item)
		}
		return out
	default:
		return v
	}
}

type traceIDCtxKey struct{}

// WithTraceID carries the turn's trace id down the request context, so message
// persistence can stamp it without every helper growing an extra parameter.
func WithTraceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, traceIDCtxKey{}, id.String())
}

// TraceIDFrom returns the trace id carried by ctx, or an empty string.
func TraceIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(traceIDCtxKey{}).(string); ok {
		return s
	}
	return ""
}
