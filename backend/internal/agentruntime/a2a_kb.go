package agentruntime

import (
	"encoding/json"
	"strings"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// kbSearchTool is the knowledge-base tool whose results the check needs.
const kbSearchTool = "kb_search"

// escalateTool is the model's hand-off tool; the runtime reads its calls so a
// check verdict does not hand off again with the code the model already used.
const escalateTool = "escalate_to_operator"

// kagentTypeKey is the part metadata kagent stamps on the ADK events it
// forwards into the task history: "function_call" for a tool call (data
// {id, name, args}) and "function_response" for its result (data {id, name,
// response}).
const kagentTypeKey = "kagent_type"

// kbResults collects the kb_search results of a task from
// result.history[].parts[]: a part is a result when its metadata says
// kagent_type=function_response and its data names kb_search. The query is
// taken from the function_call part with the same id. None of this reaches
// the customer: extractText stays the only reader of customer-visible text.
// An empty list means "none found", never an error.
func kbResults(raw json.RawMessage) []agentjudge.KBResult {
	var task struct {
		History []struct {
			Parts []a2aPart `json:"parts"`
		} `json:"history"`
	}
	if json.Unmarshal(raw, &task) != nil {
		return nil
	}
	queries := map[string]string{}
	var responses []map[string]any
	for _, m := range task.History {
		for _, part := range m.Parts {
			if part.Kind != "data" || part.Data == nil {
				continue
			}
			if name, _ := part.Data["name"].(string); name != kbSearchTool {
				continue
			}
			id, _ := part.Data["id"].(string)
			switch part.Metadata[kagentTypeKey] {
			case "function_call":
				queries[id] = kbQuery(part.Data["args"])
			case "function_response":
				responses = append(responses, part.Data)
			}
		}
	}
	var out []agentjudge.KBResult
	for _, data := range responses {
		id, _ := data["id"].(string)
		out = append(out, agentjudge.KBResult{Query: queries[id], Text: kbText(data["response"])})
	}
	return out
}

// escalationCalls lists the reason_code of every escalate_to_operator
// function_call in the task history whose function_response (same id) says
// the call went through, upper-cased as the tool handler reads it. The tool
// (http_call_v1 with pass_error_response) answers {status_code, body}; a
// rejection carries body.error / body.error_code (rejectControl), a tool
// failure is an MCP isError result or an ADK {"error": ...} response. A call
// without a response, or with a failed one, reached no operator and is not
// listed.
func escalationCalls(raw json.RawMessage) []string {
	var task struct {
		History []struct {
			Parts []a2aPart `json:"parts"`
		} `json:"history"`
	}
	if json.Unmarshal(raw, &task) != nil {
		return nil
	}
	type call struct{ id, code string }
	var calls []call
	succeeded := map[string]bool{}
	for _, m := range task.History {
		for _, part := range m.Parts {
			if part.Kind != "data" || part.Data == nil {
				continue
			}
			if name, _ := part.Data["name"].(string); name != escalateTool {
				continue
			}
			id, _ := part.Data["id"].(string)
			switch part.Metadata[kagentTypeKey] {
			case "function_call":
				args, _ := part.Data["args"].(map[string]any)
				if code, _ := args["reason_code"].(string); strings.TrimSpace(code) != "" {
					calls = append(calls, call{id: id, code: strings.ToUpper(strings.TrimSpace(code))})
				}
			case "function_response":
				if response, ok := part.Data["response"]; ok && !toolResponseFailed(response, 0) {
					succeeded[id] = true
				}
			}
		}
	}
	var out []string
	for _, c := range calls {
		if succeeded[c.id] {
			out = append(out, c.code)
		}
	}
	return out
}

// toolResponseFailed walks a tool response for a failure mark: isError true,
// a non-empty error or error_code, at any depth, also inside a text block
// that holds JSON (how an MCP result carries the tool's dict).
func toolResponseFailed(v any, depth int) bool {
	if depth > 8 {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case map[string]any:
		if isErr, _ := x["isError"].(bool); isErr {
			return true
		}
		for _, key := range []string{"error", "error_code"} {
			if mark, ok := x[key]; ok && mark != nil && mark != "" && mark != false {
				return true
			}
		}
		for _, child := range x {
			if toolResponseFailed(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if toolResponseFailed(child, depth+1) {
				return true
			}
		}
	case string:
		if text := strings.TrimSpace(x); strings.HasPrefix(text, "{") {
			var inner any
			if json.Unmarshal([]byte(text), &inner) == nil {
				return toolResponseFailed(inner, depth+1)
			}
		}
	}
	return false
}

// withEarlierAttempts carries what the earlier attempts of one turn saw into
// this attempt's reply: the kb_search results (each result once), so a fact
// the first attempt looked up still backs the rewrite, and the escalation
// calls. Fresh slices: prev is not aliased.
func withEarlierAttempts(prev, cur A2AReply) A2AReply {
	var kb []agentjudge.KBResult
	seen := map[agentjudge.KBResult]bool{}
	for _, res := range append(append([]agentjudge.KBResult{}, prev.KB...), cur.KB...) {
		if !seen[res] {
			seen[res] = true
			kb = append(kb, res)
		}
	}
	cur.KB = kb
	cur.Escalations = append(append([]string{}, prev.Escalations...), cur.Escalations...)
	if len(cur.Escalations) == 0 {
		cur.Escalations = nil
	}
	return cur
}

// kbQuery is the call's "query" argument, or the whole arguments object when
// the tool was called with another shape.
func kbQuery(args any) string {
	if m, ok := args.(map[string]any); ok {
		if q, ok := m["query"].(string); ok {
			return strings.TrimSpace(q)
		}
	}
	return compactJSON(args)
}

// kbText is the text the tool returned: the MCP text content blocks joined,
// or the raw response as JSON when it carries none.
func kbText(response any) string {
	var texts []string
	collectTextBlocks(response, 0, &texts)
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	if s, ok := response.(string); ok {
		return strings.TrimSpace(s)
	}
	return compactJSON(response)
}

// collectTextBlocks walks the response for MCP content blocks
// {"type": "text", "text": ...}.
func collectTextBlocks(v any, depth int, texts *[]string) {
	if depth > 8 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		if kind, _ := x["type"].(string); kind == "text" {
			if text, ok := x["text"].(string); ok && strings.TrimSpace(text) != "" {
				*texts = append(*texts, strings.TrimSpace(text))
				return
			}
		}
		for _, key := range sortedKeys(x) {
			collectTextBlocks(x[key], depth+1, texts)
		}
	case []any:
		for _, child := range x {
			collectTextBlocks(child, depth+1, texts)
		}
	}
}

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}
