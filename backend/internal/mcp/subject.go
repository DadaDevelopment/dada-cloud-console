package mcp

import (
	"sort"
	"strings"
)

// A tool's SUBJECT is the one path parameter that is not part of the address:
// getOperation is addressed by ref="leadgen/prod" and its subject is the
// operationId, getBuild's is the buildId, every box tool's is the boxName.
//
// The subject is where a caller's spelling goes wrong, because the value it
// holds arrives from a PREVIOUS tool result under a different name. updateAppImage
// answers with {"operation":{"id":"be0bd7ba-..."}} — the field is "id", so the
// next call was written as id=be0bd7ba-..., and the server answered "missing
// required path parameter \"operationId\"" behind the TOOL CALL FAILED preamble.
// The caller had the right value, addressed the right resource, and the only
// defect was that the answer and the question disagree about a word.
//
// So the subject accepts the names a caller plausibly holds it under: the field
// name a result carries it in ("id"), its snake_case spelling, and its bare noun
// ("operation", "build", "box"). Nothing is guessed: a tool with more than one
// non-address path parameter has no unambiguous subject and gets no aliases.

// addressParams are the path parameters that describe WHERE, not WHAT. They are
// already covered by ref/project/env/app (see addressAliases).
var addressParams = map[string]bool{"projectId": true, "envId": true, "appName": true}

// subjectParam returns the tool's subject, or "" when there is not exactly one
// non-address path parameter.
func subjectParam(g GeneratedTool) string {
	subject := ""
	for _, p := range g.PathParams {
		if addressParams[p] {
			continue
		}
		if subject != "" {
			return ""
		}
		subject = p
	}
	return subject
}

// subjectAliases returns the alternative argument names accepted for a subject
// parameter, in the order they are tried. An empty result means the parameter
// is already the only name worth using.
func subjectAliases(subject string) []string {
	var out []string
	add := func(name string) {
		if name == "" || name == subject {
			return
		}
		for _, seen := range out {
			if seen == name {
				return
			}
		}
		out = append(out, name)
	}

	if snake := snakeCase(subject); snake != subject {
		add(snake)
	}
	for _, suffix := range []string{"Id", "Name"} {
		if noun := strings.TrimSuffix(subject, suffix); noun != subject && noun != "" {
			add(strings.ToLower(noun))
			if suffix == "Id" {
				add("id")
			}
		}
	}
	return out
}

// applySubjectAliases folds an alias the caller used onto the canonical path
// parameter name. An explicitly given canonical value always wins; the alias is
// then dropped so it does not travel on as a stray query or body field.
func applySubjectAliases(g GeneratedTool, args map[string]any) {
	subject := subjectParam(g)
	if subject == "" {
		return
	}
	declared := declaredParams(g)
	for _, alias := range subjectAliases(subject) {
		v, given := args[alias]
		if !given || declared[alias] {
			continue
		}
		delete(args, alias)
		if argString(args, subject) == "" {
			args[subject] = v
		}
	}
}

// snakeCase converts a camelCase parameter name to snake_case.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// missingParamMessage explains a missing path parameter in terms the caller can
// act on: what the parameter is called, what other spellings are accepted, and
// what the call actually carried. A bare "missing required path parameter" made
// the caller re-read the schema to find a word it already had the value for.
func missingParamMessage(g GeneratedTool, name string, args map[string]any) string {
	msg := "missing required path parameter " + quote(name)
	if name == subjectParam(g) {
		if aliases := subjectAliases(name); len(aliases) > 0 {
			msg += " (also accepted as: " + strings.Join(quoteAll(aliases), ", ") + ")"
		}
	}
	if given := sortedKeys(args); len(given) > 0 {
		msg += "; this call passed: " + strings.Join(given, ", ")
	}
	return msg
}

func quote(s string) string { return "\"" + s + "\"" }

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, quote(s))
	}
	return out
}

func sortedKeys(args map[string]any) []string {
	out := make([]string, 0, len(args))
	for k := range args {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// advertiseSubjectAliases writes the accepted spellings into the subject
// parameter's own schema description, so the alias is read in the tool list
// instead of discovered by a failed call.
func advertiseSubjectAliases(g *GeneratedTool) {
	subject := subjectParam(*g)
	if subject == "" {
		return
	}
	aliases := subjectAliases(subject)
	if len(aliases) == 0 {
		return
	}
	props, _ := g.InputSchema["properties"].(map[string]any)
	if props == nil {
		return
	}
	prop, _ := props[subject].(map[string]any)
	if prop == nil {
		return
	}
	desc, _ := prop["description"].(string)
	note := "Also accepted as " + strings.Join(quoteAll(aliases), " / ") + "."
	desc = strings.TrimSpace(desc)
	if desc != "" && !strings.HasSuffix(desc, ".") {
		desc += "."
	}
	prop["description"] = strings.TrimSpace(desc + " " + note)
}

// declaredParams is every argument name the tool defines in its own right. An
// alias that collides with one of them is not an alias: folding it would eat a
// real parameter and send the call out without it.
func declaredParams(g GeneratedTool) map[string]bool {
	out := map[string]bool{}
	for _, p := range g.PathParams {
		out[p] = true
	}
	for _, q := range g.QueryParams {
		out[q] = true
	}
	if props, ok := g.InputSchema["properties"].(map[string]any); ok {
		for k := range props {
			out[k] = true
		}
	}
	return out
}
