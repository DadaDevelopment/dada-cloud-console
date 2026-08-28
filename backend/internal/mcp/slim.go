package mcp

import (
	"bytes"
	"encoding/json"
)

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
//   - Ids nothing can be addressed with. project_id, environment_id, actor_id
//     and git_repo_id are stamped on every record the console's grid returns.
//     No tool on this surface takes them — a project, an environment and an app
//     are addressed by name — and no tool turns an actor uuid into a person. On
//     getOperation they were most of the answer (2026-08-28).
//
// Nothing is invented here and no field is renamed away from its meaning: the
// UUIDs are dropped because every tool on this surface accepts the same ref
// the caller already used (see addressAliases), not because they are secret.
// The REST shape is left alone so the console grid keeps working; this trims
// only what goes to MCP.

// slimmers maps a tool name onto the transform applied to its successful
// response body. A tool with no entry is passed through untouched.
var slimmers = map[string]func(map[string]any) any{
	"listApps":     slimListApps,
	"searchLogs":   slimSearchLogs,
	"getProject":   slimGetProject,
	"getOperation": slimGetOperation,
	"getBuild":     slimGetBuild,
}

// echoIDKeys are dropped from every response this package touches, at any
// depth. They are the ids of things the caller addressed by name, and of the
// actor who is usually the caller.
var echoIDKeys = []string{"project_id", "environment_id", "actor_id", "git_repo_id"}

// slimResponse trims a 2xx body for the MCP reader.
//
// Any body that is not a JSON object, or that does not carry the keys a
// slimmer expects, keeps its own shape: a shape this code does not recognize
// is a shape it must not silently truncate. Numbers are decoded as
// json.Number, so a re-marshalled body cannot drift an integer into 1e+06.
func slimResponse(tool string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return body
	}

	var out any = doc
	if fn := slimmers[tool]; fn != nil {
		if trimmed := fn(doc); trimmed != nil {
			out = trimmed
		}
	}
	dropKeys(out, echoIDKeys)

	marshalled, err := json.Marshal(out)
	if err != nil {
		return body
	}
	return marshalled
}

// dropKeys deletes keys wherever they appear, however deep. An id stamped on
// every row of a nested list is exactly the shape being trimmed, so walking
// only the top level would leave the storm untouched.
func dropKeys(v any, keys []string) {
	if len(keys) == 0 {
		return
	}
	switch node := v.(type) {
	case map[string]any:
		for _, k := range keys {
			delete(node, k)
		}
		for _, child := range node {
			dropKeys(child, keys)
		}
	case []any:
		for _, child := range node {
			dropKeys(child, keys)
		}
	case []map[string]any:
		for _, child := range node {
			dropKeys(child, keys)
		}
	}
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

// slimGetOperation drops what the caller wrote and what only the console can
// use: the operation id it just passed as operationId, and git_path, the
// values.yaml path inside argo-infra. git_commit stays — it is the evidence
// that the write actually landed in git, and it is not addressable by any
// other means.
func slimGetOperation(doc map[string]any) any {
	return withoutRecordKeys(doc, "operation", "id", "git_path")
}

// slimGetBuild drops the build id the caller passed as buildId.
func slimGetBuild(doc map[string]any) any {
	return withoutRecordKeys(doc, "build", "id")
}

// withoutRecordKeys deletes keys from the single record an envelope wraps,
// and only from that record: an id nested deeper belongs to something the
// caller did not ask about and may still need to address.
func withoutRecordKeys(doc map[string]any, envelope string, keys ...string) any {
	record, ok := doc[envelope].(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range keys {
		delete(record, k)
	}
	return doc
}

// projectKeepKeys is what a project header is for: the address, the org it
// belongs to and which environment it means by default. Ownership ids, quota
// blobs and timestamps are the console's own grid; a caller reading getProject
// is on its way to an app, a database or an agent, and the details belong to
// that subresource.
var projectKeepKeys = []string{"id", "name", "display_name", "org_id", "default_environment"}

// slimGetProject turns the project envelope into a header plus a list of
// environment names.
//
// Measured on agent-sandbox (2026-08-28): seven environments came back as full
// records — project_id repeated on every one, namespace, type, empty
// limit_range and resource_quota objects, is_ephemeral, created_at and
// updated_at — for a call whose only purpose was to step into one of them. The
// ids are dropped because every tool on this surface takes the name.
func slimGetProject(doc map[string]any) any {
	project, ok := doc["project"].(map[string]any)
	if !ok {
		return nil
	}
	envs, ok := doc["environments"].([]any)
	if !ok {
		return nil
	}

	header := map[string]any{}
	for _, k := range projectKeepKeys {
		if v, present := project[k]; present && v != "" {
			header[k] = v
		}
	}
	if header["display_name"] == header["name"] {
		delete(header, "display_name")
	}

	out := map[string]any{"project": header}
	if role, present := doc["role"]; present {
		out["role"] = role
	}
	out["environments"] = slimEnvironments(envs)
	return out
}

// slimEnvironments keeps a name and only what distinguishes one environment
// from the ordinary case: a runtime that is not Kubernetes decides which tools
// apply at all, and an environment that expires is one a caller must not
// settle into.
func slimEnvironments(envs []any) []any {
	out := make([]any, 0, len(envs))
	for _, raw := range envs {
		env, ok := raw.(map[string]any)
		if !ok {
			return envs
		}
		name, ok := env["name"].(string)
		if !ok {
			return envs
		}
		row := map[string]any{"name": name}
		if runtime, _ := env["runtime"].(string); runtime != "" && runtime != "k8s" {
			row["runtime"] = runtime
		}
		if ephemeral, _ := env["is_ephemeral"].(bool); ephemeral {
			row["is_ephemeral"] = true
			if v, present := env["expires_at"]; present {
				row["expires_at"] = v
			}
		}
		out = append(out, row)
	}
	return out
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
