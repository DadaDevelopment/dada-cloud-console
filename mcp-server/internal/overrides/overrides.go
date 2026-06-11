// Package overrides applies a thin curation layer over the reflected tool set:
// rename, hide, and re-annotate tools without touching the backend spec.
package overrides

import (
	"fmt"
	"os"

	"github.com/dada-tuda/console/mcp-server/internal/reflect"
	"gopkg.in/yaml.v3"
)

// Config is the overrides.yaml shape.
type Config struct {
	Rename   map[string]string `yaml:"rename"`   // oldName -> newName
	Hide     []string          `yaml:"hide"`     // tool names to drop
	Annotate map[string]string `yaml:"annotate"` // name -> replacement description
	Group    map[string]string `yaml:"group"`    // name -> group label (optional, metadata only)
}

// Load reads overrides.yaml. A missing file is a no-op (empty config, nil error).
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read overrides %q: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse overrides %q: %w", path, err)
	}
	return &c, nil
}

// Apply drops hidden tools, renames, and overrides descriptions. Names are
// matched against the original (pre-rename) tool name.
func Apply(tools []reflect.GeneratedTool, c *Config) []reflect.GeneratedTool {
	if c == nil {
		return tools
	}
	hidden := make(map[string]bool, len(c.Hide))
	for _, h := range c.Hide {
		hidden[h] = true
	}

	out := make([]reflect.GeneratedTool, 0, len(tools))
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
