package renderer

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DroppedPaths reports the parts of an existing values.yaml that a freshly
// rendered one would delete: keys, and named list entries, that the render
// simply does not carry.
//
// The console renders values.yaml from the database on every deploy, so an app
// whose manifests were hand-maintained in git loses those hand edits the moment
// anything triggers a re-render. That has always been true, but a human clicking
// Deploy sees the result. An unattended deploy does not, which is how the
// autoscaler's first production tick silently stripped nine environment
// variables, two volumes and a managed-database declaration from a live app --
// ArgoCD then pruned the database.
//
// Only deletions count. A changed value is what a deploy is FOR; a key the
// render never learned about is content that only exists in git, and losing it
// is data loss.
//
// A parse failure on either side returns no paths: this guard exists to block
// a specific, provable loss, and a values.yaml this agent cannot read is not
// evidence of one. The rendered side is machine-written and always parses.
func DroppedPaths(existingYAML, renderedYAML string) []string {
	var existing, rendered any
	if err := yaml.Unmarshal([]byte(existingYAML), &existing); err != nil {
		return nil
	}
	if err := yaml.Unmarshal([]byte(renderedYAML), &rendered); err != nil {
		return nil
	}
	var out []string
	collectDropped("", existing, rendered, &out)
	sort.Strings(out)
	return out
}

// collectDropped walks two decoded YAML trees in parallel and appends the path
// of everything present in the existing tree but absent in the rendered one.
//
// Lists whose entries are maps carrying a "name" are matched by that name, not
// by index: Kubernetes writes env vars, volumes and mounts that way, and index
// matching would report every entry after an insertion as both dropped and
// added.
func collectDropped(path string, before, after any, out *[]string) {
	switch beforeVal := before.(type) {
	case map[string]any:
		afterMap, ok := after.(map[string]any)
		if !ok {
			*out = append(*out, pathOr(path))
			return
		}
		for k, v := range beforeVal {
			child, exists := afterMap[k]
			if !exists {
				*out = append(*out, joinPath(path, k))
				continue
			}
			collectDropped(joinPath(path, k), v, child, out)
		}
	case []any:
		afterList, ok := after.([]any)
		if !ok {
			*out = append(*out, pathOr(path))
			return
		}
		beforeNamed, beforeRest := splitNamed(beforeVal)
		afterNamed, _ := splitNamed(afterList)
		for name, v := range beforeNamed {
			child, exists := afterNamed[name]
			if !exists {
				*out = append(*out, joinPath(path, name))
				continue
			}
			collectDropped(joinPath(path, name), v, child, out)
		}
		if beforeRest > len(afterList)-len(afterNamed) {
			*out = append(*out, pathOr(path))
		}
	}
}

// splitNamed indexes the entries of a list that are maps with a string "name",
// and returns the count of entries that are not.
func splitNamed(list []any) (map[string]any, int) {
	named := map[string]any{}
	rest := 0
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			rest++
			continue
		}
		name, ok := m["name"].(string)
		if !ok {
			rest++
			continue
		}
		named[name] = m
	}
	return named, rest
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func pathOr(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

// DescribeDropped renders DroppedPaths for a log line or an operation's error
// message, capped so one badly drifted app cannot produce an unreadable error.
func DescribeDropped(paths []string) string {
	const max = 12
	if len(paths) > max {
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:max], ", "), len(paths)-max)
	}
	return strings.Join(paths, ", ")
}
