package mcp

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Overrides is the overrides.yaml shape.
type Overrides struct {
	Rename   map[string]string `yaml:"rename"`
	Hide     []string          `yaml:"hide"`
	Annotate map[string]string `yaml:"annotate"`
	Group    map[string]string `yaml:"group"`
}

// LoadOverrides reads an overrides yaml file. Missing file is a no-op.
func LoadOverrides(path string) (*Overrides, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Overrides{}, nil
		}
		return nil, fmt.Errorf("read overrides %q: %w", path, err)
	}
	var c Overrides
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse overrides %q: %w", path, err)
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
	out := make([]GeneratedTool, 0, len(tools))
	for _, t := range tools {
		if hidden[t.Name] {
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
