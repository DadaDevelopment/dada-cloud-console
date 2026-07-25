package renderer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ResourcesValues models an app's resources.values.yaml under ADR 0005: a single
// top-level "manifests:" list, each entry a full platform CR object. The shared
// helm/app-resources chart renders this file (ignoreMissingValueFiles: true).
//
// Each manifest is kept as a yaml.Node so nested key order is preserved verbatim
// between read and write — this is what keeps git diffs minimal (a plain
// map[string]interface{} round-trip reorders keys on every marshal and churns
// the file on every commit).
type ResourcesValues struct {
	Manifests []yaml.Node `yaml:"manifests"`
}

// ParseResourcesValues parses resources.values.yaml content. Empty/whitespace
// content yields an empty (non-nil) Manifests list, matching the "missing file
// => empty list" contract.
func ParseResourcesValues(content string) (*ResourcesValues, error) {
	rv := &ResourcesValues{Manifests: []yaml.Node{}}
	if len(content) == 0 {
		return rv, nil
	}
	if err := yaml.Unmarshal([]byte(content), rv); err != nil {
		return nil, fmt.Errorf("parsing resources.values.yaml: %w", err)
	}
	if rv.Manifests == nil {
		rv.Manifests = []yaml.Node{}
	}
	return rv, nil
}

// manifestKey returns the (kind, name) identity of a manifest node, used to
// match existing entries for upsert/remove. Missing fields yield empty strings.
func manifestKey(n *yaml.Node) (kind, name string) {
	doc := n
	if doc != nil && doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc == nil || doc.Kind != yaml.MappingNode {
		return "", ""
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k := doc.Content[i]
		v := doc.Content[i+1]
		switch k.Value {
		case "kind":
			kind = v.Value
		case "metadata":
			if v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					if v.Content[j].Value == "name" {
						name = v.Content[j+1].Value
					}
				}
			}
		}
	}
	return kind, name
}

// manifestNodeFromYAML parses a single rendered CR YAML string into a mapping
// node suitable for the manifests: list (unwrapping the document node).
func manifestNodeFromYAML(crYAML string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(crYAML), &doc); err != nil {
		return nil, fmt.Errorf("parsing CR manifest: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0], nil
	}
	return &doc, nil
}

// Upsert inserts or replaces a manifest (matched by kind+name). Existing order is
// preserved: a replaced entry keeps its slot, a new entry is appended. crYAML is
// the rendered CR (the exact string a Render*() function produces).
func (rv *ResourcesValues) Upsert(crYAML string) error {
	node, err := manifestNodeFromYAML(crYAML)
	if err != nil {
		return err
	}
	kind, name := manifestKey(node)
	if kind == "" || name == "" {
		return fmt.Errorf("manifest missing kind/name, cannot upsert")
	}
	for i := range rv.Manifests {
		ek, en := manifestKey(&rv.Manifests[i])
		if ek == kind && en == name {
			rv.Manifests[i] = *node
			return nil
		}
	}
	rv.Manifests = append(rv.Manifests, *node)
	return nil
}

// Remove drops the manifest matching (kind, name). No-op if absent. Returns true
// when something was removed.
func (rv *ResourcesValues) Remove(kind, name string) bool {
	out := rv.Manifests[:0]
	removed := false
	for i := range rv.Manifests {
		ek, en := manifestKey(&rv.Manifests[i])
		if ek == kind && en == name {
			removed = true
			continue
		}
		out = append(out, rv.Manifests[i])
	}
	rv.Manifests = out
	return removed
}

// RemoveKind drops every manifest of the given kind, returning how many were
// removed. Used by MoveApp's stateful guard to defensively strip any
// ServiceDatabaseV2 entry before copying an app's resources.values.yaml to
// another project — Phase 1 never carries a stateful child across namespaces.
func (rv *ResourcesValues) RemoveKind(kind string) int {
	out := rv.Manifests[:0]
	removed := 0
	for i := range rv.Manifests {
		ek, _ := manifestKey(&rv.Manifests[i])
		if ek == kind {
			removed++
			continue
		}
		out = append(out, rv.Manifests[i])
	}
	rv.Manifests = out
	return removed
}

// NamesOfKind returns the metadata.name of every manifest of the given kind, in
// document order (empty when none match). MoveApp uses it to enumerate the
// cluster-scoped resources an app carries — the custom-domain PublicApi — so it
// can re-adopt each onto the target Argo app before the source folder is pruned,
// keeping the single shared object from being deleted mid-move.
func (rv *ResourcesValues) NamesOfKind(kind string) []string {
	var names []string
	for i := range rv.Manifests {
		k, n := manifestKey(&rv.Manifests[i])
		if k == kind && n != "" {
			names = append(names, n)
		}
	}
	return names
}

// ManifestOfKind returns the YAML of the first manifest of the given kind and
// whether one was present. The bytes round-trip through the same node encoding
// Upsert consumes, so a caller can unmarshal a specific CR's fields back out and
// re-render it. MoveApp reads the app's ServiceDatabaseV2 entry this way to
// re-home it under the target namespace without hand-parsing the whole file.
func (rv *ResourcesValues) ManifestOfKind(kind string) (string, bool, error) {
	for i := range rv.Manifests {
		k, _ := manifestKey(&rv.Manifests[i])
		if k != kind {
			continue
		}
		b, err := yaml.Marshal(&rv.Manifests[i])
		if err != nil {
			return "", false, fmt.Errorf("marshalling %s manifest: %w", kind, err)
		}
		return string(b), true, nil
	}
	return "", false, nil
}

// Marshal renders the file back to YAML with a top-level "manifests:" key. When
// the list is empty it emits "manifests: []" so the file is still valid.
func (rv *ResourcesValues) Marshal() (string, error) {
	if rv.Manifests == nil {
		rv.Manifests = []yaml.Node{}
	}
	b, err := yaml.Marshal(rv)
	if err != nil {
		return "", fmt.Errorf("marshalling resources.values.yaml: %w", err)
	}
	return string(b), nil
}
