// Package sourcedetect inspects an uploaded source archive (zip or tar.gz)
// and guesses the app's framework and listen port from a small set of
// well-known manifest files, without ever unpacking the archive to disk.
//
// It is a simplified, archive-native cousin of build-agent's
// resolveExplicitPort (build-agent/internal/server/server.go): that function
// reads a live GitHub checkout via the Contents API; this one reads the
// table of contents of an in-memory archive, because at upload time that is
// the only place the bytes exist.
package sourcedetect

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Format identifies the archive container detected from magic bytes.
type Format string

const (
	FormatZip   Format = "zip"
	FormatTarGz Format = "tar.gz"
)

// Result is what Detect resolves from an archive's manifest files.
//
// Framework is one of "docker", "nextjs", "vite", "react", "node", "fastapi",
// "flask", "django", "streamlit", "python", "maven", "gradle", "go", or "" when
// nothing matched. The names are the vocabulary dadaBuildPipeline.renderDockerfile switches on —
// keep them in sync with build-agent's GitHub-side detection
// (build-agent/internal/server/server.go), or the pipeline finds no template
// and the build fails with no_dockerfile. Port is 0 when unresolved, which
// lets the template pick its own default.
type Result struct {
	Format    Format
	Framework string
	Port      int
}

// maxEntries caps how many table-of-contents entries Detect walks, so a
// pathological archive (millions of tiny entries) can't stall the request.
//
// The cap is generous because walking a header costs nothing: only entries
// whose base name is a known manifest have their bytes buffered (see
// isCandidate). A tight cap used to decide the answer for large repos —
// netdata carries 13k files — which made detection depend on where in the
// tree a manifest happened to sit.
const maxEntries = 40000

// candidateNames are the only files Detect ever reads. Everything else is
// walked as a header and dropped, which is what keeps a 40k-entry cap cheap.
var candidateNames = map[string]bool{
	"Dockerfile":          true,
	"Procfile":            true,
	"package.json":        true,
	"requirements.txt":    true,
	"pyproject.toml":      true,
	"docker-compose.yml":  true,
	"docker-compose.yaml": true,
	"compose.yml":         true,
	"compose.yaml":        true,
	"pom.xml":             true,
	"build.gradle":        true,
	"build.gradle.kts":    true,
	"go.mod":              true,
	"railway.json":        true,
	"railway.toml":        true,
	"nixpacks.toml":       true,
}

// isCandidate reports whether an archive member is worth buffering.
//
// Variants like Dockerfile.debian count, because a root Dockerfile symlink
// usually points at one of them (vaultwarden ships Dockerfile ->
// docker/Dockerfile.debian) and a target that was never buffered resolves to
// nothing.
func isCandidate(name string) bool {
	base := name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return candidateNames[base] || strings.HasPrefix(base, "Dockerfile.")
}

// maxManifestBytes caps how much of any single candidate manifest file is
// read into memory.
const maxManifestBytes = 1024 * 1024

// entry is a format-agnostic view of one archive member, read on demand.
//
// link is non-empty for a symbolic link, and holds the raw link target as
// stored in the archive. A repo whose root Dockerfile is a symlink into a
// subdirectory (vaultwarden, netdata) is common enough that dropping such
// entries reads to the caller as "this repo ships no Dockerfile".
type entry struct {
	name string
	size int64
	link string
	open func() (io.ReadCloser, error)
}

// Detect identifies the archive format and, on a best-effort basis, the
// framework and port implied by its manifest files. An unrecognized
// container (bad magic bytes) is the only hard error; a container with no
// matching manifest still returns successfully with an empty Result.Framework.
func Detect(data []byte) (Result, error) {
	format, err := detectFormat(data)
	if err != nil {
		return Result{}, err
	}

	entries, err := listEntries(data, format)
	if err != nil {
		return Result{}, fmt.Errorf("read %s table of contents: %w", format, err)
	}

	result := Result{Format: format}
	root := detectRoot(entries)

	for _, pick := range []func() (entry, bool){
		func() (entry, bool) { return findManifest(entries, root, "Dockerfile") },
		func() (entry, bool) { return rootProductionDockerfile(entries, root) },
		func() (entry, bool) { return singleNestedDockerfile(entries, root) },
	} {
		e, ok := pick()
		if !ok {
			continue
		}
		raw, err := readEntry(e)
		if err != nil {
			continue
		}
		result.Framework = "docker"
		if p, ok := parseDockerfileExpose(raw); ok {
			result.Port = p
		} else if p, ok := composePort(entries, root); ok {
			result.Port = p
		}
		return result, nil
	}

	platform := platformAssignsPort(entries, root)
	compose, hasCompose := composePort(entries, root)

	if e, ok := findManifest(entries, root, "package.json"); ok {
		raw, err := readEntry(e)
		if err == nil {
			if fw, port, ok := parsePackageJSON(raw); ok {
				result.Framework = fw
				result.Port = resolvePort(port, platform, compose, hasCompose)
				return result, nil
			}
		}
	}

	for _, name := range []string{"requirements.txt", "pyproject.toml"} {
		e, ok := findManifest(entries, root, name)
		if !ok {
			continue
		}
		raw, err := readEntry(e)
		if err != nil {
			continue
		}
		if fw, port, ok := parsePythonManifest(raw); ok {
			result.Framework = fw
			result.Port = resolvePort(port, platform, compose, hasCompose)
			return result, nil
		}
	}

	if fw, port, ok := detectCompiled(entries, root); ok {
		result.Framework = fw
		result.Port = resolvePort(port, platform, compose, hasCompose)
		return result, nil
	}

	return result, nil
}

// compiledManifests maps a build manifest to the framework name and default
// port the git-import path already uses (build-agent's frameworkDefaultPort).
// Without them the upload path answered "no manifest" for every Go and JVM
// repo, even though the pipeline knows how to build both.
var compiledManifests = []struct {
	file      string
	framework string
	port      int
}{
	{"pom.xml", "maven", 8080},
	{"build.gradle", "gradle", 8080},
	{"build.gradle.kts", "gradle", 8080},
	{"go.mod", "go", 8080},
}

// platformFiles declare that the port is assigned by the host platform and read
// from $PORT: a Procfile, or a Railway/Nixpacks config. Naming a number for such
// a repo publishes a port nothing listens on.
var platformFiles = []string{"Procfile", "railway.json", "railway.toml", "nixpacks.toml"}

func platformAssignsPort(entries []entry, root string) bool {
	for _, name := range platformFiles {
		if _, ok := findManifest(entries, root, name); ok {
			return true
		}
	}
	return false
}

// resolvePort ranks the three answers a non-Dockerfile repo can give, from
// evidence to convention: a compose mapping states the port, a platform config
// states that there is no fixed port, and only then does the per-framework
// default apply.
func resolvePort(fallback int, platform bool, compose int, hasCompose bool) int {
	if hasCompose {
		return compose
	}
	if platform {
		return 0
	}
	return fallback
}

func detectCompiled(entries []entry, root string) (string, int, bool) {
	for _, m := range compiledManifests {
		if _, ok := findManifest(entries, root, m.file); ok {
			return m.framework, m.port, true
		}
	}
	return "", 0, false
}

func detectFormat(data []byte) (Format, error) {
	if len(data) >= 4 && bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return FormatZip, nil
	}
	if len(data) >= 4 && bytes.HasPrefix(data, []byte("PK\x05\x06")) {
		return FormatZip, nil
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return FormatTarGz, nil
	}
	return "", fmt.Errorf("unrecognized archive magic bytes (want zip or tar.gz)")
}

func listEntries(data []byte, format Format) ([]entry, error) {
	switch format {
	case FormatZip:
		return listZipEntries(data)
	case FormatTarGz:
		return listTarGzEntries(data)
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func listZipEntries(data []byte) ([]entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out []entry
	for i, f := range zr.File {
		if i >= maxEntries {
			break
		}
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") || !isCandidate(f.Name) {
			continue
		}
		f := f
		out = append(out, entry{
			name: f.Name,
			size: int64(f.UncompressedSize64),
			open: func() (io.ReadCloser, error) { return f.Open() },
		})
	}
	return out, nil
}

// listTarGzEntries buffers each member's bytes eagerly (rather than keeping a
// lazy handle into the tar stream), since a tar.Reader can only be walked
// forward once and Detect may need to open more than one candidate entry.
func listTarGzEntries(data []byte) ([]entry, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var out []entry
	for i := 0; i < maxEntries; i++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			if isCandidate(hdr.Name) && !strings.Contains(hdr.Linkname, "..") {
				out = append(out, entry{name: hdr.Name, link: hdr.Linkname})
			}
			continue
		}
		if hdr.Typeflag != tar.TypeReg || !isCandidate(hdr.Name) {
			continue
		}
		limit := hdr.Size
		if limit > maxManifestBytes {
			limit = maxManifestBytes
		}
		buf := make([]byte, limit)
		if _, err := io.ReadFull(tr, buf); err != nil && err != io.ErrUnexpectedEOF {
			continue
		}
		name := hdr.Name
		content := buf
		out = append(out, entry{
			name: name,
			size: hdr.Size,
			open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(content)), nil },
		})
	}
	return out, nil
}

// detectRoot returns the single top-level directory shared by every entry
// ("myproject/src/index.js" -> "myproject/"), matching the strip-components
// Jenkins applies on extract. Lovable/Bolt/v0 exports are single-root zips;
// an archive with mixed top-level entries (or none) has no root to strip.
func detectRoot(entries []entry) string {
	var root string
	for i, e := range entries {
		idx := strings.IndexByte(e.name, '/')
		if idx < 0 {
			return ""
		}
		top := e.name[:idx+1]
		if i == 0 {
			root = top
			continue
		}
		if top != root {
			return ""
		}
	}
	return root
}

// findManifest looks for name at the archive root (after stripping the
// detected single top-level directory, if any) — never inside nested
// directories like node_modules, so a dependency's own package.json can't
// shadow the app's.
func findManifest(entries []entry, root, name string) (entry, bool) {
	for _, e := range entries {
		rel := strings.TrimPrefix(e.name, root)
		if rel == name {
			return resolveLink(entries, e, 0)
		}
	}
	return entry{}, false
}

// singleNestedDockerfile returns the archive's only Dockerfile when it lives
// outside the root, which is how a large share of real repos ship one
// (gotify keeps docker/Dockerfile, mealie docker/Dockerfile).
//
// "Only" is the whole rule: a repo with several Dockerfiles is describing
// several images, and choosing among them would be a guess whose cost lands on
// the user as an app that builds the wrong thing. Test and example images are
// excluded by path, since a repo that ships one app Dockerfile plus a test
// fixture is still unambiguous to a human.
func singleNestedDockerfile(entries []entry, root string) (entry, bool) {
	if e, ok := nestedDockerfile(entries, root, true); ok {
		return e, true
	}
	return nestedDockerfile(entries, root, false)
}

// nestedDockerfile scans the archive's non-root Dockerfiles and returns the one
// candidate, if there is exactly one.
//
// With productionOnly set it looks only inside a production/ directory. Repos
// that lay their Dockerfiles out by environment (wger keeps base, development,
// demo and production side by side) are ambiguous only until that convention is
// read; the environment named production is the one that ships.
func nestedDockerfile(entries []entry, root string, productionOnly bool) (entry, bool) {
	var found entry
	count := 0
	for _, e := range entries {
		rel := strings.TrimPrefix(e.name, root)
		base := rel
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if base != "Dockerfile" || !strings.Contains(rel, "/") || isExcludedPath(rel) {
			continue
		}
		if productionOnly && !strings.Contains(rel, "production/") {
			continue
		}
		resolved, ok := resolveLink(entries, e, 0)
		if !ok {
			continue
		}
		count++
		if count > 1 {
			return entry{}, false
		}
		found = resolved
	}
	return found, count == 1
}

// excludedDirs are path segments whose Dockerfiles never describe the app
// itself, so their presence must not turn an unambiguous repo into an
// ambiguous one.
var excludedDirs = []string{
	"test/", "tests/", "e2e/", "example/", "examples/", "docs/",
	"playwright/", "fixtures/", "contrib/", ".devcontainer/", ".github/",
}

// productionDockerfileNames are the root Dockerfile variants that name the
// shipped image. A repo like Ghost carries only Dockerfile.production in its
// root; reading nothing there meant falling through to package.json and
// answering with the dev server's port instead of the image's.
var productionDockerfileNames = []string{"Dockerfile.production", "Dockerfile.prod"}

// rootProductionDockerfile returns the root production Dockerfile when exactly
// one of the known names is present. Two candidates mean the repo, not the
// detector, decides which image ships, so it refuses.
func rootProductionDockerfile(entries []entry, root string) (entry, bool) {
	var found entry
	count := 0
	for _, name := range productionDockerfileNames {
		e, ok := findManifest(entries, root, name)
		if !ok {
			continue
		}
		count++
		found = e
	}
	return found, count == 1
}

func isExcludedPath(rel string) bool {
	for _, dir := range excludedDirs {
		if strings.HasPrefix(rel, dir) || strings.Contains(rel, "/"+dir) {
			return true
		}
	}
	return false
}

// maxLinkHops bounds symlink chasing; a cycle inside a hostile archive must
// not turn into an infinite loop.
const maxLinkHops = 4

// resolveLink follows a symlink entry to the regular file it names. The link
// target is tried both as an archive-absolute path (how hard links store it)
// and as a path relative to the link's own directory (how symlinks store it).
// A link that resolves to nothing is reported as "not found" rather than as an
// empty manifest, since an empty manifest would silently answer "no framework".
func resolveLink(entries []entry, e entry, hop int) (entry, bool) {
	if e.link == "" {
		return e, true
	}
	if hop >= maxLinkHops {
		return entry{}, false
	}
	dir := ""
	if i := strings.LastIndexByte(e.name, '/'); i >= 0 {
		dir = e.name[:i+1]
	}
	for _, candidate := range []string{e.link, path.Clean(dir + e.link)} {
		for _, other := range entries {
			if other.name != candidate || other.name == e.name {
				continue
			}
			return resolveLink(entries, other, hop+1)
		}
	}
	return entry{}, false
}

func readEntry(e entry) ([]byte, error) {
	r, err := e.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	limit := e.size
	if limit <= 0 || limit > maxManifestBytes {
		limit = maxManifestBytes
	}
	return io.ReadAll(io.LimitReader(r, limit))
}

var (
	composeMappedRe  = regexp.MustCompile(`(?m)^\s*-\s*"?[^"\n]*?:(\d+)(?:/(?:tcp|udp))?"?\s*$`)
	composeDefaultRe = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*:-(\d+)\}`)
	composeTargetRe  = regexp.MustCompile(`(?m)^\s*target:\s*"?(\d+)`)
)

// composePort recovers the container port from a root compose file, for repos
// that ship a Dockerfile without EXPOSE.
//
// It looks at the root compose file first and, only if that yields nothing,
// at compose files anywhere else in the archive: repos that keep the app's
// compose next to a nested Dockerfile (mealie, memos) are common, and the root
// file, when present, is the more authoritative of the two.
//
// It answers only when every published mapping agrees on one container port.
// A compose file with several distinct targets describes several services, and
// picking one of them would be guessing — and a guessed port is exactly what
// turns a healthy app into a failing readiness probe. Silence (port 0) leaves
// the decision to the build template, which is the safe default.
func composePort(entries []entry, root string) (int, bool) {
	rootNames := map[string]bool{
		"docker-compose.yml":  true,
		"docker-compose.yaml": true,
		"compose.yml":         true,
		"compose.yaml":        true,
	}
	for _, scope := range []bool{true, false} {
		found := map[int]bool{}
		for _, e := range entries {
			rel := strings.TrimPrefix(e.name, root)
			base := rel
			if i := strings.LastIndexByte(base, '/'); i >= 0 {
				base = base[i+1:]
			}
			if !rootNames[base] || isExcludedPath(rel) {
				continue
			}
			if scope && strings.Contains(rel, "/") {
				continue
			}
			raw, err := readEntry(e)
			if err != nil {
				continue
			}
			raw = composeDefaultRe.ReplaceAll(raw, []byte("$1"))
			for _, re := range []*regexp.Regexp{composeMappedRe, composeTargetRe} {
				for _, m := range re.FindAllSubmatch(raw, -1) {
					p, err := strconv.Atoi(string(m[1]))
					if err == nil && p > 0 && p <= 65535 {
						found[p] = true
					}
				}
			}
		}
		if len(found) == 1 {
			for p := range found {
				return p, true
			}
		}
	}
	return 0, false
}

var (
	exposeRe    = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\S+)`)
	dockerVarRe = regexp.MustCompile(`(?im)^\s*(?:ENV|ARG)\s+([A-Za-z_][A-Za-z0-9_]*)\s*[= ]\s*"?(\d+)"?\s*$`)
	varRefRe    = regexp.MustCompile(`^\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$`)
)

// parseDockerfileExpose reads the first EXPOSE, resolving a variable reference
// against ENV and ARG defaults declared in the same Dockerfile.
//
// "EXPOSE $PORT" above "ENV PORT=3000" is how homepage, shiori and gotify
// declare their port; reading only literal digits treated all three as if they
// declared nothing, and the port then had to be guessed downstream.
func parseDockerfileExpose(raw []byte) (int, bool) {
	m := exposeRe.FindSubmatch(raw)
	if m == nil {
		return 0, false
	}
	token := strings.TrimSpace(string(m[1]))
	if i := strings.IndexByte(token, '/'); i >= 0 {
		token = token[:i]
	}
	if ref := varRefRe.FindStringSubmatch(token); ref != nil {
		token = ""
		for _, v := range dockerVarRe.FindAllSubmatch(raw, -1) {
			if string(v[1]) == ref[1] {
				token = string(v[2])
			}
		}
		if token == "" {
			return 0, false
		}
	}
	p, err := strconv.Atoi(token)
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// parsePackageJSON maps the handful of frameworks the upload flow cares
// about to their conventional dev-server port. Next is checked first since a
// Next app's package.json can carry "react" (and even "vite", for a mixed
// tool-chain) transitively too.
func parsePackageJSON(raw []byte) (framework string, port int, ok bool) {
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", 0, false
	}
	has := func(name string) bool {
		_, a := pkg.Dependencies[name]
		_, b := pkg.DevDependencies[name]
		return a || b
	}
	switch {
	case has("next"):
		return "nextjs", 3000, true
	case has("vite"):
		return "vite", 5173, true
	case has("react-scripts"):
		return "react", 3000, true
	default:
		return "node", 3000, true
	}
}

// parsePythonManifest scans requirements.txt / pyproject.toml text for one
// of a handful of well-known Python web framework package names. Both
// formats list the package name at the start of a dependency token
// (requirements.txt: "fastapi==0.110.0"; pyproject.toml dependency arrays:
// "fastapi>=0.110").
//
// A python manifest with no recognized web framework still resolves to the
// generic "python" framework with port 0: a long-polling worker (a Telegram
// bot, a queue consumer) is a first-class upload target and must not fall
// through to the empty framework, which the build pipeline cannot template.
func parsePythonManifest(raw []byte) (framework string, port int, ok bool) {
	text := strings.ToLower(string(raw))
	for _, fw := range []struct {
		name string
		port int
	}{
		{"fastapi", 8000},
		{"streamlit", 8501},
		{"django", 8000},
		{"flask", 5000},
	} {
		if pythonPackageRe(fw.name).MatchString(text) {
			return fw.name, fw.port, true
		}
	}
	return "python", 0, true
}

func pythonPackageRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)(^|["'\s])` + regexp.QuoteMeta(name) + `([=><\[;"'\s]|$)`)
}
