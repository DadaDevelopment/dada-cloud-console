package mcp

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed default_overrides.yaml
var defaultOverridesYAML []byte

// Overrides is the overrides.yaml shape.
//
// Keep, when non-empty, is an allowlist: only those tool names survive, giving a
// curated agent surface where new backend operations do NOT leak in — the
// opposite of Hide, which rots as the API grows. Hide still subtracts from the
// kept set.
type Overrides struct {
	Keep     []string          `yaml:"keep"`
	Rename   map[string]string `yaml:"rename"`
	Hide     []string          `yaml:"hide"`
	Annotate map[string]string `yaml:"annotate"`
	Group    map[string]string `yaml:"group"`
}

// LoadOverrides reads an overrides yaml file. A missing file falls back to the
// embedded default_overrides.yaml (the curated agent surface), so the server
// ships a sane keep-list even when no external file is mounted. Point
// MCP_OVERRIDES_PATH at a real file to replace the default entirely.
func LoadOverrides(path string) (*Overrides, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return parseOverrides(defaultOverridesYAML, "embedded default")
		}
		return nil, fmt.Errorf("read overrides %q: %w", path, err)
	}
	return parseOverrides(b, path)
}

func parseOverrides(b []byte, src string) (*Overrides, error) {
	var c Overrides
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse overrides %q: %w", src, err)
	}
	return &c, nil
}

// ApplyOverrides drops hidden tools, renames, and overrides descriptions.
func ApplyOverrides(tools []GeneratedTool, c *Overrides) []GeneratedTool {
	if c == nil {
		return tools
	}
	hidden := make(map[string]bool, len(c.Hide))
	for _, h := range c.Hide {
		hidden[h] = true
	}
	var keep map[string]bool
	if len(c.Keep) > 0 {
		keep = make(map[string]bool, len(c.Keep))
		for _, k := range c.Keep {
			keep[k] = true
		}
	}
	out := make([]GeneratedTool, 0, len(tools))
	for _, t := range tools {
		if hidden[t.Name] {
			continue
		}
		if keep != nil && !keep[t.Name] {
			continue
		}
		orig := t.Name
		if desc, ok := c.Annotate[orig]; ok {
			t.Description = desc
		}
		if newName, ok := c.Rename[orig]; ok && newName != "" {
			t.Name = newName
		}
		out = append(out, t)
	}
	return out
}
