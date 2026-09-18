// Package agentjudge scores one delivered agent turn against the judge spec
// shipped with the agent package (agents/<agent>/judge/turn.yaml + turn.md).
// Deterministic criteria run as code, the rest go to a single LLM call, and
// every criterion lands in Langfuse as its own score on the turn's trace.
package agentjudge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	CheckCode = "code"
	CheckLLM  = "llm"

	TypeBool  = "bool"
	TypeScore = "score"

	SeverityHard  = "hard"
	SeverityMajor = "major"
	SeverityMinor = "minor"
)

// Criterion is one scored rule. Score is the suffix of the Langfuse score
// name (<judge>.<score>). A bool criterion is a violation flag, a score
// criterion is 0-100 where 100 is the desired behaviour.
type Criterion struct {
	ID       string   `yaml:"id"`
	Score    string   `yaml:"score"`
	Check    string   `yaml:"check"`
	Type     string   `yaml:"type"`
	Severity string   `yaml:"severity"`
	Applies  string   `yaml:"applies"`
	Title    string   `yaml:"title"`
	Text     string   `yaml:"text"`
	Kind     string   `yaml:"kind"`
	Scope    string   `yaml:"scope"`
	Words    []string `yaml:"words"`
	Max      int      `yaml:"max"`
	Message  int      `yaml:"message"`
	Turn     int      `yaml:"turn"`
}

// Signal is a situation label the LLM marks on the client side of the turn.
type Signal struct {
	ID   string `yaml:"id"`
	When string `yaml:"when"`
}

// Total describes the aggregate score derived from the criteria.
type Total struct {
	Score     string         `yaml:"score"`
	HardCap   float64        `yaml:"hard_cap"`
	Weights   map[string]int `yaml:"weights"`
	FailBelow float64        `yaml:"fail_below"`
}

// Spec is the parsed turn.yaml plus the prompt template next to it.
type Spec struct {
	Version  int         `yaml:"version"`
	Name     string      `yaml:"name"`
	Prompt   string      `yaml:"prompt"`
	Total    Total       `yaml:"total"`
	Signals  []Signal    `yaml:"signals"`
	Criteria []Criterion `yaml:"criteria"`
	Template string      `yaml:"-"`
}

// LoadSpec reads <dir>/turn.yaml and the prompt file it names.
func LoadSpec(dir string) (*Spec, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "turn.yaml"))
	if err != nil {
		return nil, err
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "turn.yaml"), err)
	}
	prompt := spec.Prompt
	if prompt == "" {
		prompt = "turn.md"
	}
	tpl, err := os.ReadFile(filepath.Join(dir, prompt))
	if err != nil {
		return nil, err
	}
	spec.Template = string(tpl)
	return spec, nil
}

// ParseSpec decodes and validates a turn.yaml body.
func ParseSpec(raw []byte) (*Spec, error) {
	var spec Spec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	if spec.Version != 2 {
		return nil, fmt.Errorf("unsupported judge spec version %d", spec.Version)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("judge name is required")
	}
	if spec.Total.Score == "" {
		spec.Total.Score = "total"
	}
	seen := map[string]bool{}
	for i, c := range spec.Criteria {
		if c.ID == "" || c.Score == "" {
			return nil, fmt.Errorf("criterion %d: id and score are required", i)
		}
		if seen[c.Score] {
			return nil, fmt.Errorf("criterion %s: duplicate score %s", c.ID, c.Score)
		}
		seen[c.Score] = true
		if c.Check != CheckCode && c.Check != CheckLLM {
			return nil, fmt.Errorf("criterion %s: check must be code or llm", c.ID)
		}
		if c.Type != TypeBool && c.Type != TypeScore {
			return nil, fmt.Errorf("criterion %s: type must be bool or score", c.ID)
		}
		switch c.Severity {
		case SeverityHard, SeverityMajor, SeverityMinor, "":
		default:
			return nil, fmt.Errorf("criterion %s: unknown severity %s", c.ID, c.Severity)
		}
		if c.Check == CheckCode {
			if _, ok := codeChecks[c.Kind]; !ok {
				return nil, fmt.Errorf("criterion %s: unknown code check %q", c.ID, c.Kind)
			}
			if c.Type != TypeBool {
				return nil, fmt.Errorf("criterion %s: code checks are bool", c.ID)
			}
		} else if strings.TrimSpace(c.Text) == "" {
			return nil, fmt.Errorf("criterion %s: llm criterion needs text", c.ID)
		}
	}
	for _, s := range spec.Signals {
		if s.ID == "" {
			return nil, fmt.Errorf("signal without id")
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("signal %s collides with a criterion score", s.ID)
		}
		seen[s.ID] = true
	}
	return &spec, nil
}

// ScoreName is the full Langfuse score name of one criterion or signal.
func (s *Spec) ScoreName(suffix string) string {
	return s.Name + "." + suffix
}

func (s *Spec) llmCriteria() []Criterion {
	var out []Criterion
	for _, c := range s.Criteria {
		if c.Check == CheckLLM {
			out = append(out, c)
		}
	}
	return out
}

func (s *Spec) codeCriteria() []Criterion {
	var out []Criterion
	for _, c := range s.Criteria {
		if c.Check == CheckCode {
			out = append(out, c)
		}
	}
	return out
}
