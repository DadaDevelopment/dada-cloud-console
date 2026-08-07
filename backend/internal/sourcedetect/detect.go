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
// "flask", "django", "streamlit", "python", or "" when nothing matched. The
// names are the vocabulary dadaBuildPipeline.renderDockerfile switches on —
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
const maxEntries = 500

// maxManifestBytes caps how much of any single candidate manifest file is
// read into memory.
const maxManifestBytes = 1024 * 1024

// entry is a format-agnostic view of one archive member, read on demand.
type entry struct {
	name string
	size int64
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

	if e, ok := findManifest(entries, root, "Dockerfile"); ok {
		raw, err := readEntry(e)
		if err == nil {
			result.Framework = "docker"
			if p, ok := parseDockerfileExpose(raw); ok {
				result.Port = p
			}
			return result, nil
		}
	}

	if e, ok := findManifest(entries, root, "package.json"); ok {
		raw, err := readEntry(e)
		if err == nil {
			if fw, port, ok := parsePackageJSON(raw); ok {
				result.Framework = fw
				result.Port = port
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
			result.Port = port
			return result, nil
		}
	}

	return result, nil
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
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") {
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
		if hdr.Typeflag != tar.TypeReg || strings.Contains(hdr.Name, "..") {
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
			return e, true
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

var exposeRe = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)

func parseDockerfileExpose(raw []byte) (int, bool) {
	m := exposeRe.FindSubmatch(raw)
	if m == nil {
		return 0, false
	}
	p, err := strconv.Atoi(string(m[1]))
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
