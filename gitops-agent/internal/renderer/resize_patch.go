package renderer

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNoResourcesTarget reports a values.yaml with no common: block to patch.
var ErrNoResourcesTarget = errors.New("values.yaml has no common: mapping to patch resources into")

// PatchValuesResources rewrites ONLY common.resources in an existing
// values.yaml and returns the file with everything else byte-identical.
//
// This is the difference between resizing an app and redeploying it. The normal
// deploy path regenerates values.yaml from the database, which is correct for an
// app the console owns and destructive for one whose manifests are maintained by
// hand: the render carries no env, no volumes, no serviceDatabase for those, and
// the clobber guard has to refuse the deploy to avoid deleting them. The
// autoscaler then cannot touch a single hand-maintained app, which on this
// cluster was most of the ones that starve.
//
// Patching sidesteps the whole question. Nothing outside resources is read,
// written or re-derived, so there is nothing to drop and no guard to trip.
//
// Keys the caller leaves empty are not written, and no key is ever deleted: an
// ephemeral-storage limit the platform has no opinion about survives untouched.
func PatchValuesResources(existingYAML string, r AppResources) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(existingYAML), &doc); err != nil {
		return "", fmt.Errorf("parse values.yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", ErrNoResourcesTarget
	}
	root := doc.Content[0]
	common := mappingChild(root, "common")
	if common == nil {
		return "", ErrNoResourcesTarget
	}
	resources := mappingChild(common, "resources")
	if resources == nil {
		resources = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendPair(common, "resources", resources)
	}

	requests := map[string]string{"cpu": r.CPURequest, "memory": r.MemoryRequest, "ephemeral-storage": r.EphemeralRequest}
	limits := map[string]string{"cpu": r.CPULimit, "memory": r.MemoryLimit, "ephemeral-storage": r.EphemeralLimit}
	for section, values := range map[string]map[string]string{"requests": requests, "limits": limits} {
		target := mappingChild(resources, section)
		if target == nil {
			target = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendPair(resources, section, target)
		}
		for _, key := range []string{"cpu", "memory", "ephemeral-storage"} {
			if values[key] == "" {
				continue
			}
			setScalar(target, key, values[key])
		}
	}

	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode values.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode values.yaml: %w", err)
	}
	return out.String(), nil
}

// mappingChild returns the value node for key in a mapping, or nil.
func mappingChild(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func appendPair(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
}

// setScalar writes a string value, replacing an existing one in place so the
// key keeps its position and any comment attached to it.
//
// The tag is pinned to !!str because Kubernetes quantities like "8" or "2" are
// strings: emitting them bare would make the chart read an int and the API
// server reject the manifest.
func setScalar(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind = yaml.ScalarNode
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Value = value
			m.Content[i+1].Content = nil
			return
		}
	}
	appendPair(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
