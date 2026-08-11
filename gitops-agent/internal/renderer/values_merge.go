package renderer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ownedCommonKeys is the set of keys under `common` that RenderAppValues is
// authoritative for: every key the appValuesFile struct can emit.
//
// It is the ownership contract of the merge below. A key in this list is the
// console's to write and to remove -- dropping a volume or an env var in the UI
// has to delete it from git, or the change does not happen. A key outside it was
// put in the file by a human or another pipeline, and the console has no opinion
// about it, so a render must leave it exactly as it found it.
//
// Keep this in sync with commonValues. A field added to the struct but not
// listed here would be written on the first deploy and then never removed.
var ownedCommonKeys = []string{
	"image",
	"service",
	"servicePort",
	"replicas",
	"useDotEnv",
	"resources",
	"extraEnv",
	"pvc",
	"workloadType",
	"podSecurityContext",
}

// MergeAppValues applies a freshly rendered values.yaml onto the one already in
// git, replacing only the keys the renderer owns and preserving everything else
// verbatim -- including comments, key order, and whole subtrees the console has
// never heard of.
//
// The console renders values.yaml from its database, and until this existed it
// wrote that render over the file wholesale. Any key only the git file knew
// about disappeared on the next deploy of an unrelated field. That is how
// dada-development-site lost its `common.ingress` block during an image bump:
// the app stayed healthy on its default domain while its custom domain served
// nginx 404 for sixteen hours, because a host with no Ingress rule reaches the
// default backend and is attributed to no Ingress at all.
//
// The jenkins-pipelines shared library has never had this failure mode for the
// same file, because it edits one path -- `yq eval '.common.image.tag = ...'`
// -- and cannot express "and delete whatever else is in there".
//
// A values.yaml that does not parse is returned to the caller as an error rather
// than being overwritten: an unreadable file is not permission to discard it,
// and Argo would render chart defaults over the live release if a broken one
// were committed.
func MergeAppValues(existingYAML, renderedYAML string) (string, error) {
	var rendered yaml.Node
	if err := yaml.Unmarshal([]byte(renderedYAML), &rendered); err != nil {
		return "", fmt.Errorf("parsing rendered values: %w", err)
	}
	if existingYAML == "" {
		return renderedYAML, nil
	}
	var existing yaml.Node
	if err := yaml.Unmarshal([]byte(existingYAML), &existing); err != nil {
		return "", fmt.Errorf("parsing existing values: %w", err)
	}

	existingRoot := documentRoot(&existing)
	renderedRoot := documentRoot(&rendered)
	if existingRoot == nil || existingRoot.Kind != yaml.MappingNode ||
		renderedRoot == nil || renderedRoot.Kind != yaml.MappingNode {
		return renderedYAML, nil
	}

	for i := 0; i+1 < len(renderedRoot.Content); i += 2 {
		key := renderedRoot.Content[i].Value
		if key != "common" {
			setMapValue(existingRoot, key, renderedRoot.Content[i+1])
			continue
		}
		existingCommon := mapValue(existingRoot, "common")
		renderedCommon := renderedRoot.Content[i+1]
		if existingCommon == nil || existingCommon.Kind != yaml.MappingNode ||
			renderedCommon.Kind != yaml.MappingNode {
			setMapValue(existingRoot, key, renderedCommon)
			continue
		}
		for _, owned := range ownedCommonKeys {
			if v := mapValue(renderedCommon, owned); v != nil {
				setMapValue(existingCommon, owned, v)
			} else {
				deleteMapKey(existingCommon, owned)
			}
		}
	}

	out, err := marshalDocument(&existing)
	if err != nil {
		return "", fmt.Errorf("re-marshalling merged values: %w", err)
	}
	return out, nil
}

// documentRoot unwraps the document node yaml.Unmarshal produces for a whole
// file, so callers can treat a parsed file as its top-level mapping.
func documentRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	if n.Kind == 0 {
		return nil
	}
	return n
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces the value for key in place, keeping the key's position in
// the file, and appends it at the end when it is new.
func setMapValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// deleteMapKey removes key and its value from a mapping node.
func deleteMapKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// marshalDocument serialises a parsed document back to YAML with the indentation
// the rest of the renderer emits.
func marshalDocument(doc *yaml.Node) (string, error) {
	root := documentRoot(doc)
	if root == nil {
		return "", fmt.Errorf("empty document")
	}
	b, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
