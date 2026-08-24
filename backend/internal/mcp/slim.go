package mcp

import "encoding/json"

// Response slimming: a tool result is read by a model whose context is the
// scarce resource, and the REST bodies these tools proxy were shaped for the
// console's own web UI, which needs identifiers the model already holds.
//
// Two kinds of waste were measured on real calls (leadgen/prod, 2026-08-24):
//
//   - Echo. listApps repeated the project and environment names and UUIDs in
//     an envelope AND again on every row, for a call whose own argument was
//     ref="leadgen/prod". searchLogs stamped "app" on every entry for a search
//     already scoped to that app.
//   - Constants. Every log entry carried the same "stream" value, once per
//     line.
//
// Nothing is invented here and no field is renamed away from its meaning: the
// UUIDs are dropped because every tool on this surface accepts the same ref
// the caller already used (see addressAliases), not because they are secret.
// The REST shape is left alone so the console grid keeps working; this trims
// only what goes to MCP.

// slimmers maps a tool name onto the transform applied to its successful
// response body. A tool with no entry is passed through untouched.
var slimmers = map[string]func(map[string]any) any{
	"listApps":   slimListApps,
	"searchLogs": slimSearchLogs,
}

// slimResponse applies the tool's slimmer to a 2xx body. Any body that is not
// a JSON object, or that does not carry the keys the slimmer expects, is
// returned byte-for-byte: a shape this code does not recognize is a shape it
// must not silently truncate.
func slimResponse(tool string, body []byte) []byte {
	fn := slimmers[tool]
	if fn == nil || len(body) == 0 {
		return body
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}
	out := fn(doc)
	if out == nil {
		return body
	}
	trimmed, err := json.Marshal(out)
	if err != nil {
		return body
	}
	return trimmed
}

// appEchoKeys are the listApps row fields the caller supplied as the call's own
// address. project and env are kept: they are names, they are what the next
// call wants, and they are already inside ref.
var appEchoKeys = []string{"project_id", "environment_id"}

// slimListApps drops the envelope and the per-row UUIDs, leaving the apps
// array as the whole response.
func slimListApps(doc map[string]any) any {
	apps, ok := doc["apps"].([]any)
	if !ok {
		return nil
	}
	for _, raw := range apps {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range appEchoKeys {
			delete(row, k)
		}
	}
	return map[string]any{"apps": apps}
}

// logEntryConstantKeys are the log-entry fields worth hoisting out of the array
// when every entry agrees on them. "app" is nearly always constant (the search
// is scoped to one app) and "stream" usually is; when they are not, they stay
// on the entries where they differ and nothing is lost.
var logEntryConstantKeys = []string{"app", "stream", "vm_name"}

// logInstanceKey renames vm_name in the MCP view. The backend field carries the
// vm_name label filebeat stamps, which for a Kubernetes app is the POD name —
// so a container app's log entries came back tagged "vm_name", inviting the
// reader to look for a virtual machine that does not exist.
const logInstanceKey = "instance"

// slimSearchLogs hoists the fields every entry shares and renames vm_name to
// instance.
func slimSearchLogs(doc map[string]any) any {
	raw, ok := doc["entries"].([]any)
	if !ok {
		return nil
	}
	entries := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		row, ok := e.(map[string]any)
		if !ok {
			return nil
		}
		entries = append(entries, row)
	}

	out := map[string]any{}
	if total, ok := doc["total"]; ok {
		out["total"] = total
	}
	for _, key := range logEntryConstantKeys {
		if v, ok := constantAcross(entries, key); ok {
			out[outKey(key)] = v
			for _, row := range entries {
				delete(row, key)
			}
		}
	}
	for _, row := range entries {
		if v, ok := row["vm_name"]; ok {
			delete(row, "vm_name")
			row[logInstanceKey] = v
		}
	}
	out["entries"] = entries
	return out
}

// outKey is the name a hoisted field takes at the top level.
func outKey(key string) string {
	if key == "vm_name" {
		return logInstanceKey
	}
	return key
}

// constantAcross reports the value of key when every entry carries it and they
// all agree. An empty list has no constant: hoisting from nothing would invent
// a claim about entries that do not exist.
func constantAcross(entries []map[string]any, key string) (any, bool) {
	if len(entries) == 0 {
		return nil, false
	}
	first, ok := entries[0][key]
	if !ok {
		return nil, false
	}
	s, ok := first.(string)
	if !ok || s == "" {
		return nil, false
	}
	for _, row := range entries[1:] {
		if row[key] != first {
			return nil, false
		}
	}
	return first, true
}
