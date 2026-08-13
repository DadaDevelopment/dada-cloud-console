package archive

import (
	"bufio"
	"os"
	"path"
	"strings"
)

// rule is one parsed .gitignore line.
type rule struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// Matcher evaluates a set of gitignore rules against repo-relative,
// slash-separated paths. It supports the common subset of the gitignore
// grammar: comments, blank lines, "!" negation, a trailing "/" meaning
// "directories only", a leading "/" or an embedded "/" meaning "anchored to
// the .gitignore's directory", and glob wildcards via path.Match. It does not
// implement "**" specially (path.Match treats it as "*") and does not read
// nested .gitignore files below the project root - both are fine for a v0
// packaging filter, whose job is "don't blow the upload budget on junk", not
// byte-for-byte git parity.
type Matcher struct {
	rules []rule
}

// LoadGitignore reads root/.gitignore. A missing file yields an empty,
// always-false Matcher rather than an error, since most projects that need
// packaging still don't have one.
func LoadGitignore(root string) (*Matcher, error) {
	f, err := os.Open(path.Join(root, ".gitignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return &Matcher{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var rules []rule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := rule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.Contains(line, "/") {
			r.anchored = true
		}
		r.pattern = line
		if r.pattern != "" {
			rules = append(rules, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &Matcher{rules: rules}, nil
}

// Match reports whether relPath (slash-separated, relative to the project
// root, no leading slash) is ignored. Later rules override earlier ones, and
// a negated rule un-ignores a path matched by an earlier rule - the same
// precedence git itself uses.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if m == nil {
		return false
	}
	ignored := false
	base := path.Base(relPath)
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		var hit bool
		if r.anchored {
			hit, _ = path.Match(r.pattern, relPath)
		} else {
			hit, _ = path.Match(r.pattern, base)
			if !hit {
				hit, _ = path.Match(r.pattern, relPath)
			}
		}
		if hit {
			ignored = !r.negate
		}
	}
	return ignored
}
