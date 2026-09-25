// Package agentjudge scores one delivered agent turn against the judge spec
// shipped with the agent package (agents/<agent>/judge/turn.yaml + turn.md).
// Deterministic criteria run as code, the rest go to a single LLM call, and
// every criterion lands in Langfuse as its own score on the turn's trace.
package agentjudge

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
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

	// BlockRewrite and BlockHandoff mark a criterion the runtime checks
	// before delivery (Judge.Check): a rewrite violation asks the model for
	// another draft with Ask, a handoff violation also passes the dialog to
	// an operator with Code and sends Line to the client.
	BlockRewrite = "rewrite"
	BlockHandoff = "handoff"
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
	Allow    []string `yaml:"allow"`
	Block    string   `yaml:"block"`
	Ask      string   `yaml:"ask"`
	Code     string   `yaml:"code"`
	Line     string   `yaml:"line"`
	Source   string   `yaml:"source"`
}

// Signal is a situation label the LLM marks on the client side of the turn.
// Handoff and StopFollowups say what the runtime does when a pre-delivery
// check marks it: Handoff is the escalation code the conversation is handed
// off with (after the reply of that turn went out, with a pause),
// StopFollowups ends the idle follow-up ladder until the client writes
// again. The mapping lives in the agent's spec, the runtime only acts on it.
type Signal struct {
	ID            string `yaml:"id"`
	When          string `yaml:"when"`
	Handoff       string `yaml:"handoff"`
	StopFollowups bool   `yaml:"stop_followups"`
}

// Total describes the aggregate score derived from the criteria.
type Total struct {
	Score     string         `yaml:"score"`
	HardCap   float64        `yaml:"hard_cap"`
	Weights   map[string]int `yaml:"weights"`
	FailBelow float64        `yaml:"fail_below"`
}

// Skip names traffic the judge leaves unscored, such as synthetic probes; a username may be a glob (qa_*, *_probe), matched ignoring case and @.
type Skip struct {
	Usernames []string `yaml:"usernames"`
}

// Spec is the parsed turn.yaml plus the prompt template next to it.
// HandoffLine is the client line of a check-decided hand-off whose criterion
// names no line of its own.
type Spec struct {
	Version     int         `yaml:"version"`
	Name        string      `yaml:"name"`
	Prompt      string      `yaml:"prompt"`
	Skip        Skip        `yaml:"skip"`
	Total       Total       `yaml:"total"`
	HandoffLine string      `yaml:"handoff_line"`
	Signals     []Signal    `yaml:"signals"`
	Criteria    []Criterion `yaml:"criteria"`
	Template    string      `yaml:"-"`
}

// LoadSpec reads <dir>/turn.yaml and the prompt file it names.
func LoadSpec(dir string) (*Spec, error) {
	return loadSpecFile(filepath.Join(dir, "turn.yaml"))
}

// LoadSpecs reads every <dir>/*.yaml as a judge spec; each one runs on
// every turn and writes scores under its own name. A dir without specs is
// os.ErrNotExist so the caller treats it like a missing judge.
func LoadSpecs(dir string) ([]*Spec, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(files)
	names := map[string]string{}
	specs := make([]*Spec, 0, len(files))
	for _, f := range files {
		spec, err := loadSpecFile(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := names[spec.Name]; dup {
			return nil, fmt.Errorf("%s: judge name %s already used by %s", f, spec.Name, prev)
		}
		names[spec.Name] = f
		specs = append(specs, spec)
	}
	return specs, nil
}

func loadSpecFile(file string) (*Spec, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	prompt := spec.Prompt
	if prompt == "" {
		prompt = strings.TrimSuffix(filepath.Base(file), ".yaml") + ".md"
	}
	tpl, err := os.ReadFile(filepath.Join(filepath.Dir(file), prompt))
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
	signals := map[string]bool{}
	for _, s := range spec.Signals {
		signals[s.ID] = true
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
		switch c.Block {
		case "":
		case BlockRewrite, BlockHandoff:
			if strings.TrimSpace(c.Ask) == "" {
				return nil, fmt.Errorf("criterion %s: block %s needs ask", c.ID, c.Block)
			}
			if c.Block == BlockHandoff && strings.TrimSpace(c.Code) == "" {
				return nil, fmt.Errorf("criterion %s: block handoff needs code", c.ID)
			}
		default:
			return nil, fmt.Errorf("criterion %s: block must be rewrite or handoff", c.ID)
		}
		// A block criterion gated by an unknown signal would never fire in
		// Check, silently turning a safety rule off.
		if c.Block != "" && c.Applies != "" && !signals[c.Applies] {
			return nil, fmt.Errorf("criterion %s: applies %s is not a signal", c.ID, c.Applies)
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
// Skips reports whether turns of this username stay unjudged.
func (s *Spec) Skips(username string) bool {
	name := strings.ToLower(strings.TrimPrefix(username, "@"))
	for _, u := range s.Skip.Usernames {
		if ok, _ := path.Match(strings.ToLower(strings.TrimPrefix(u, "@")), name); ok {
			return true
		}
	}
	return false
}

func (s *Spec) ScoreName(suffix string) string {
	return s.Name + "." + suffix
}

// subset is a copy of the spec with only the criteria keep accepts; signals,
// totals and the template are shared, so the prompt keeps its shape.
func (s *Spec) subset(keep func(Criterion) bool) *Spec {
	sub := *s
	sub.Criteria = nil
	for _, c := range s.Criteria {
		if keep(c) {
			sub.Criteria = append(sub.Criteria, c)
		}
	}
	return &sub
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
