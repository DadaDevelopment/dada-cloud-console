package renderer

import (
	"fmt"
	"strings"

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
	"startCommand",
	"podSecurityContext",
	"hostAliases",
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
// MergeOptions narrows what a render is allowed to say about the file in git.
//
// Two of the keys the renderer emits are not console state at all -- they are
// platform guesses that get emitted on every deploy whether or not anyone chose
// them. Writing a guess over a value a human put in git is the same data loss as
// deleting it, and the clobber guard cannot see it, because the guard only
// reports keys that DISAPPEAR: on 2026-08-21 an env-var save on
// internal/prod/telemost-bot moved servicePort 8000 -> 8080 and useDotEnv
// true -> false, both as in-place CHANGES, and the bot came up with a Service
// pointing at a port nothing listened on.
//
// Advisory names those keys. An advisory key is written when git does not have
// it -- a console-created app still gets its defaults -- and left exactly as it
// is when git does, in either direction: a render that omits an advisory key
// does not delete it either.
//
// ExpectedDrops are the values.yaml paths the operation MEANS to remove, in the
// same notation the clobber guard reports (common.extraEnv.FOO). They are what
// makes removing an environment variable possible now that a render's silence
// about an entry no longer removes it.
type MergeOptions struct {
	Advisory      []string
	ExpectedDrops []string
}

// alwaysAdvisory are the owned keys no caller can make authoritative, because
// nothing in the console's database backs them. useDotEnv is a hardcoded
// constant in RenderAppValues: the console has never had a field for it, so
// every render asserts "false" about an app that may well have been mounting a
// .env for a year.
var alwaysAdvisory = []string{"useDotEnv"}

// MergeAppValues merges with the console treated as authoritative for every key
// it owns except the intrinsically advisory ones. Production goes through
// MergeAppValuesWith so a deploy can declare which of its values are guesses.
func MergeAppValues(existingYAML, renderedYAML string) (string, error) {
	return MergeAppValuesWith(existingYAML, renderedYAML, MergeOptions{})
}

func MergeAppValuesWith(existingYAML, renderedYAML string, opts MergeOptions) (string, error) {
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
			v := mapValue(renderedCommon, owned)
			switch {
			case owned == extraEnvKey:
				mergeExtraEnv(existingCommon, v, opts.ExpectedDrops)
			case isAdvisory(owned, opts.Advisory):
				if mapValue(existingCommon, owned) == nil && v != nil {
					setMapValue(existingCommon, owned, v)
				}
			case v != nil:
				setMapValue(existingCommon, owned, v)
			default:
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

// extraEnvKey is the one owned key whose entries are merged by name instead of
// being replaced as a block. See mergeExtraEnv.
const extraEnvKey = "extraEnv"

// isAdvisory reports whether an owned key carries a platform guess rather than
// console state on this render.
func isAdvisory(key string, advisory []string) bool {
	for _, a := range alwaysAdvisory {
		if a == key {
			return true
		}
	}
	for _, a := range advisory {
		if a == key {
			return true
		}
	}
	return false
}

// mergeExtraEnv folds the rendered environment into the one already in git,
// matched by variable name, and removes only the entries the operation declared.
//
// Replacing the list wholesale was the loudest half of the 2026-08-21 loss:
// internal/prod/telemost-bot carried eight extraEnv entries -- its Postgres
// credentials among them -- that live only in git, the console had rows for
// none of them, and saving one new variable rendered a one-entry list that
// replaced all eight. The console cannot delete what it has never been told
// about; silence about an entry is not an instruction to remove it.
//
// A caller that means to remove one says so through MergeOptions.ExpectedDrops,
// which is exactly what DeleteEnvVar declares, so removing a variable in the UI
// still removes it from git.
func mergeExtraEnv(existingCommon *yaml.Node, rendered *yaml.Node, expectedDrops []string) {
	existing := mapValue(existingCommon, extraEnvKey)
	if existing == nil || existing.Kind != yaml.SequenceNode {
		if rendered != nil {
			setMapValue(existingCommon, extraEnvKey, rendered)
		}
		return
	}

	if rendered != nil && rendered.Kind == yaml.SequenceNode {
		for _, item := range rendered.Content {
			name := envEntryName(item)
			if name == "" {
				continue
			}
			if at := envEntryIndex(existing, name); at >= 0 {
				carryComments(existing.Content[at], item)
				existing.Content[at] = item
				continue
			}
			existing.Content = append(existing.Content, item)
		}
	}

	for _, drop := range expectedDrops {
		name := strings.TrimPrefix(drop, "common."+extraEnvKey+".")
		if name == drop || name == "" {
			continue
		}
		if at := envEntryIndex(existing, name); at >= 0 {
			existing.Content = append(existing.Content[:at], existing.Content[at+1:]...)
		}
	}

	if len(existing.Content) == 0 {
		deleteMapKey(existingCommon, extraEnvKey)
	}
}

// envEntryName returns the "name" of one extraEnv entry, or "" when the entry
// is not a named mapping -- a raw scalar or a Helm template the console must
// leave alone.
func envEntryName(item *yaml.Node) string {
	if item.Kind != yaml.MappingNode {
		return ""
	}
	if n := mapValue(item, "name"); n != nil {
		return n.Value
	}
	return ""
}

// envEntryIndex finds an extraEnv entry by variable name.
func envEntryIndex(list *yaml.Node, name string) int {
	for i, item := range list.Content {
		if envEntryName(item) == name {
			return i
		}
	}
	return -1
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
			carryComments(m.Content[i+1], value)
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// carryComments moves the prose a human left in git onto the node that replaces
// it. A rendered node never carries comments, so replacing an owned key would
// otherwise silently delete the warning someone wrote next to it. Comments are
// matched by key so a note on a nested field survives the subtree swap; the
// rendered node wins whenever it already carries text of its own.
func carryComments(old, fresh *yaml.Node) {
	if old == nil || fresh == nil {
		return
	}
	if fresh.HeadComment == "" {
		fresh.HeadComment = old.HeadComment
	}
	if fresh.LineComment == "" {
		fresh.LineComment = old.LineComment
	}
	if fresh.FootComment == "" {
		fresh.FootComment = old.FootComment
	}
	if old.Kind == yaml.SequenceNode && fresh.Kind == yaml.SequenceNode {
		for i, item := range fresh.Content {
			if match := matchingSeqItem(old, item, i); match != nil {
				carryComments(match, item)
			}
		}
		return
	}
	if old.Kind != yaml.MappingNode || fresh.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(old.Content); i += 2 {
		for j := 0; j+1 < len(fresh.Content); j += 2 {
			if old.Content[i].Value != fresh.Content[j].Value {
				continue
			}
			carryComments(old.Content[i], fresh.Content[j])
			carryComments(old.Content[i+1], fresh.Content[j+1])
		}
	}
}

// matchingSeqItem finds the list entry the fresh one replaces. List entries the
// renderer emits (extraEnv, hostAliases) are identified by their name field, so
// a note next to one variable follows that variable even when the list is
// reordered; position is the fallback for entries without a name.
func matchingSeqItem(old, fresh *yaml.Node, idx int) *yaml.Node {
	if name := scalarField(fresh, "name"); name != "" {
		for _, cand := range old.Content {
			if scalarField(cand, "name") == name {
				return cand
			}
		}
		return nil
	}
	if idx < len(old.Content) {
		return old.Content[idx]
	}
	return nil
}

// scalarField reads a scalar value out of a mapping node, or "" when absent.
func scalarField(n *yaml.Node, key string) string {
	if n == nil || n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value
		}
	}
	return ""
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
	if doc.Kind == yaml.DocumentNode {
		if root.HeadComment == "" {
			root.HeadComment = doc.HeadComment
		}
		if root.FootComment == "" {
			root.FootComment = doc.FootComment
		}
	}
	b, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
