package sourcedetect

import "strings"

// staticWebRoots are the directory names a hand-built site keeps its entry
// page in when it is not at the archive root. They are the output directories
// of every generator a vibe-coder is likely to have run locally, plus the
// names people pick by hand for "the part you serve".
var staticWebRoots = []string{"dist/", "build/", "public/", "site/", "www/", "html/", "out/"}

// staticDisqualifiers are the manifests whose presence means the archive
// describes a stack that has to be built, not a directory that has to be
// served. Static wrapping is the last resort in Detect precisely because a
// repo that ships index.html beside package.json is a frontend project whose
// index.html is a source file, not a deployable page: serving it raw would
// publish an app that never runs its own build.
var staticDisqualifiers = []string{
	"package.json",
	"requirements.txt",
	"pyproject.toml",
	"go.mod",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"Procfile",
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yml",
	"compose.yaml",
}

// StaticSiteRoot reports which directory of an uploaded archive should become
// the web root, for an archive that carries a page to serve and no stack to
// build.
//
// It exists because such an archive had no answer at all. Detection recognizes
// manifests, and a folder of html, css and images carries none, so Detect
// returned an empty framework and the pipeline aborted with a line naming the
// framework as an empty string and telling the user to add a Dockerfile.
//
// On 2026-09-09 a user who had signed up 76 seconds earlier uploaded exactly
// that and got that message as the platform's entire answer to their first
// action. The archive was deployable; nothing about it was ever missing except
// a Dockerfile the platform is perfectly able to write itself.
//
// The returned root is archive-relative and keeps any stripped wrapper prefix,
// so a caller can hand it straight to InjectDockerfile as the prefix to strip.
// It is empty for an archive whose index.html is already at the top level.
//
// Ambiguity is answered with false rather than with a guess: two sibling
// directories that both carry an index.html describe two sites, and picking
// one would publish half an upload under the name of the whole.
func StaticSiteRoot(names []string) (string, bool) {
	wrapper := detectRootFromNames(names)

	for _, name := range names {
		rel := strings.TrimPrefix(name, wrapper)
		if rel == "" || isToolingPath(name) {
			continue
		}
		base := rel
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if strings.HasSuffix(strings.ToLower(base), ".py") {
			return "", false
		}
		if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
			return "", false
		}
		for _, bad := range staticDisqualifiers {
			if base == bad {
				return "", false
			}
		}
	}

	if hasIndexAt(names, wrapper) {
		return wrapper, true
	}

	var found string
	for _, dir := range staticWebRoots {
		if hasIndexAt(names, wrapper+dir) {
			if found != "" {
				return "", false
			}
			found = wrapper + dir
		}
	}
	if found != "" {
		return found, true
	}

	return singleContentDirIndex(names, wrapper)
}

// hasIndexAt reports whether an index page sits directly under prefix, with no
// further directory below it. The name is matched case-insensitively because
// an upload made on Windows or macOS routinely arrives as Index.html, and a
// site that differs from a working one only by the case of a letter is not a
// site the platform should refuse.
func hasIndexAt(names []string, prefix string) bool {
	for _, name := range names {
		if isToolingPath(name) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == name && prefix != "" {
			continue
		}
		if rel == "" || strings.Contains(rel, "/") {
			continue
		}
		if strings.EqualFold(rel, "index.html") || strings.EqualFold(rel, "index.htm") {
			return true
		}
	}
	return false
}

// singleContentDirIndex covers the archive whose site lives one directory down
// under a name the platform does not know in advance, which is what a user
// gets by zipping the folder that contains their project folder. Exactly one
// content directory is the whole rule, for the same reason ambiguity is
// refused above: a second one means a second project.
func singleContentDirIndex(names []string, wrapper string) (string, bool) {
	dirs := map[string]bool{}
	for _, name := range names {
		if isToolingPath(name) {
			continue
		}
		rel := strings.TrimPrefix(name, wrapper)
		idx := strings.IndexByte(rel, '/')
		if idx < 0 {
			continue
		}
		dirs[rel[:idx+1]] = true
	}
	if len(dirs) != 1 {
		return "", false
	}
	var only string
	for dir := range dirs {
		only = dir
	}
	if hasIndexAt(names, wrapper+only) {
		return wrapper + only, true
	}
	return "", false
}

// isToolingPath reports whether an archive member belongs to tooling residue
// rather than to the site: the __MACOSX sidecar Finder writes beside every zip
// it makes, and dot-directories such as .git or .vscode. Their contents must
// never decide detection, and __MACOSX in particular ships a shadow copy of
// every file including index.html.
func isToolingPath(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			continue
		}
		if seg == "__MACOSX" || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
