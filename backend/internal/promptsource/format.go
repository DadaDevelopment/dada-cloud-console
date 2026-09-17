// Package promptsource reads an agent's prompt and skills from a directory in
// the client's git repository and checks the layout the console documents in
// docs/agent-prompt-source.md: agents/<name>/core.md carries the system prompt
// with a "# <title> U+00B7 <version>" header, agents/<name>/domains/<skill>.md
// carries one skill each. The package has no database and no handler; it
// turns files into a Bundle or into one error the UI can show.
package promptsource

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dada-tuda/console/backend/internal/agentruntime"
	"github.com/dada-tuda/console/backend/internal/kagent"
)

// CoreFile is the prompt file inside the agent directory.
const CoreFile = "core.md"

// DomainsDir holds one skill per .md file inside the agent directory.
const DomainsDir = "domains"

// HeaderSeparator splits the title from the version on the first line of core.md.
const HeaderSeparator = "\u00b7"

var skillName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// ErrMissingCore reports an agent directory without core.md.
var ErrMissingCore = errors.New("core.md not found in the agent directory")

// Skill is one domains/<name>.md file.
type Skill struct {
	Name    string
	Content string
}

// Bundle is what one sync produced from the repository.
type Bundle struct {
	Title   string
	Version string
	Prompt  string
	Skills  []Skill
	SHA     string
}

// Bytes is the size of the prompt plus every skill, for the sync log.
func (b Bundle) Bytes() int {
	n := len(b.Prompt)
	for _, s := range b.Skills {
		n += len(s.Content)
	}
	return n
}

// SkillMap keys the skills by name for the JSONB column.
func (b Bundle) SkillMap() map[string]string {
	out := make(map[string]string, len(b.Skills))
	for _, s := range b.Skills {
		out[s.Name] = s.Content
	}
	return out
}

// ParseHeader splits "# <title> U+00B7 <version>" into its two parts. The version
// is what the ManagedAgent claim carries as promptVersion, so a header that
// does not name one is refused rather than defaulted.
func ParseHeader(firstLine string) (title, version string, err error) {
	line := strings.TrimSpace(firstLine)
	if !strings.HasPrefix(line, "#") {
		return "", "", fmt.Errorf("core.md must start with a \"# <title> %s <version>\" header, got %q", HeaderSeparator, truncate(line, 60))
	}
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	before, after, found := strings.Cut(line, HeaderSeparator)
	if !found {
		return "", "", fmt.Errorf("core.md header %q has no %s separator: the text after it is the prompt version", truncate(line, 60), HeaderSeparator)
	}
	title = strings.TrimSpace(before)
	version = strings.TrimSpace(after)
	if version == "" {
		return "", "", fmt.Errorf("core.md header %q has an empty version after %s", truncate(line, 60), HeaderSeparator)
	}
	return title, version, nil
}

// Build validates the files fetched from <dir> in the repository and assembles
// the bundle. files maps a path relative to the agent directory to its bytes;
// anything outside core.md and domains/*.md is ignored so experiments/ and
// README files in the same directory do not fail the sync.
func Build(files map[string][]byte, sha string) (Bundle, error) {
	core, ok := files[CoreFile]
	if !ok {
		return Bundle{}, ErrMissingCore
	}
	if !utf8.Valid(core) {
		return Bundle{}, fmt.Errorf("core.md is not valid UTF-8")
	}
	prompt := string(core)
	if err := kagent.ValidatePrompt(prompt); err != nil {
		return Bundle{}, fmt.Errorf("core.md: %w", err)
	}
	firstLine, _, _ := strings.Cut(prompt, "\n")
	title, version, err := ParseHeader(firstLine)
	if err != nil {
		return Bundle{}, err
	}

	var skills []Skill
	for rel, content := range files {
		dir, base := path.Split(rel)
		if path.Clean(dir) != DomainsDir || !strings.HasSuffix(base, ".md") {
			continue
		}
		name := strings.TrimSuffix(base, ".md")
		if !skillName.MatchString(name) {
			return Bundle{}, fmt.Errorf("skill file %s: name must match %s", rel, skillName.String())
		}
		if !utf8.Valid(content) {
			return Bundle{}, fmt.Errorf("skill file %s is not valid UTF-8", rel)
		}
		if len(content) == 0 {
			return Bundle{}, fmt.Errorf("skill file %s is empty", rel)
		}
		if len(content) > agentruntime.MaxSkillContentBytes {
			return Bundle{}, fmt.Errorf("skill file %s is %d bytes, the runtime loads at most %d", rel, len(content), agentruntime.MaxSkillContentBytes)
		}
		skills = append(skills, Skill{Name: name, Content: string(content)})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return Bundle{Title: title, Version: version, Prompt: prompt, Skills: skills, SHA: sha}, nil
}

// WantedPath reports whether a repository path under the agent directory is
// one the sync downloads.
func WantedPath(rel string) bool {
	if rel == CoreFile {
		return true
	}
	dir, base := path.Split(rel)
	return path.Clean(dir) == DomainsDir && strings.HasSuffix(base, ".md")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
