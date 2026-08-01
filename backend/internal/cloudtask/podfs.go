package cloudtask

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// FileEntry is one directory entry inside an app's persistent volume, as
// reported by "stat" in the app's own container. ModTime is a Unix second
// timestamp; Mode is the human-readable "drwxr-xr-x" form; Kind is the raw
// stat %F class normalised to one of the FileKind constants.
type FileEntry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modified"`
	Mode    string `json:"mode"`
}

// Entry kinds returned by PodFS. Anything stat reports that is not a regular
// file, directory or symlink (sockets, fifos, devices) collapses to
// FileKindOther, which the API exposes read-only.
const (
	FileKindFile    = "file"
	FileKindDir     = "dir"
	FileKindSymlink = "symlink"
	FileKindOther   = "other"
)

// maxListEntries caps a single directory listing. A directory with more
// entries than this is truncated rather than streamed in full: the listing is
// a browsing aid, and the tarball export exists for bulk access.
const maxListEntries = 2000

// PodFS performs file operations inside a running app container over the
// Kubernetes exec API. Every path is passed as a positional shell argument
// ("$1"), never interpolated into the script, so a path can never break out of
// its argument position regardless of the characters it contains.
//
// All operations run in the app's own container, so they observe exactly the
// same filesystem, uid and permissions as the app itself. Confining paths to
// the app's volume is the caller's job, using these two resolvers:
//
//   - ResolvePath returns the symlink-free absolute path of p. An existing
//     directory is resolved directly; anything else resolves its parent
//     directory and re-appends the base name, so a file that does not exist
//     yet (one about to be written) still resolves. The caller prefix-checks
//     the result against the volume root.
//   - ResolveLink returns the fully dereferenced target of a symlink, for the
//     same prefix check on entries that are links.
type PodFS interface {
	Enabled() bool
	FindRunningPod(ctx context.Context, namespace, appName string) (podName, containerName string, err error)
	ResolvePath(ctx context.Context, t PodTarget, p string) (string, error)
	ResolveLink(ctx context.Context, t PodTarget, p string) (string, error)
	List(ctx context.Context, t PodTarget, dir string) (entries []FileEntry, truncated bool, err error)
	Stat(ctx context.Context, t PodTarget, p string) (FileEntry, error)
	ReadFile(ctx context.Context, t PodTarget, p string, limit int64, out io.Writer) error
	WriteFile(ctx context.Context, t PodTarget, p string, in io.Reader) error
	Mkdir(ctx context.Context, t PodTarget, p string) error
	Move(ctx context.Context, t PodTarget, from, to string) error
	Remove(ctx context.Context, t PodTarget, p string, recursive bool) error
	TarDir(ctx context.Context, t PodTarget, dir string, out io.Writer) error
}

// PodTarget addresses one container of one pod. It exists so the many PodFS
// methods do not each repeat the same three string parameters.
type PodTarget struct {
	Namespace string
	Pod       string
	Container string
}

// clientsetPodFS reuses the tar exporter's in-cluster client and RBAC
// (pods/exec create); no permission beyond what the volume export already
// holds is needed.
type clientsetPodFS struct{ *clientsetPodTarExporter }

// NewPodFS builds a PodFS backed by the pod's mounted service-account
// credentials. Off-cluster it returns a PodFS whose every method fails with a
// clear "not configured" error, mirroring NewPodTarExporter.
func NewPodFS() PodFS {
	exporter := NewPodTarExporter()
	real, ok := exporter.(*clientsetPodTarExporter)
	if !ok {
		return unconfiguredPodFS{err: fmt.Errorf("in-cluster config unavailable")}
	}
	return &clientsetPodFS{clientsetPodTarExporter: real}
}

// exec runs argv in the target container, feeding it stdin and writing its
// stdout to out. A non-zero exit becomes a *PodExecError carrying the stderr
// tail, which the API layer classifies into an HTTP status.
func (p *clientsetPodFS) exec(ctx context.Context, t PodTarget, argv []string, stdin io.Reader, out io.Writer) error {
	req := p.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(t.Namespace).
		Name(t.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: t.Container,
			Command:   argv,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(p.restCfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build pod exec: %w", err)
	}
	var stderr bytes.Buffer
	if out == nil {
		out = io.Discard
	}
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: out,
		Stderr: &stderr,
	})
	if err != nil {
		tail := stderr.String()
		if len(tail) > podExecStderrLimit {
			tail = tail[len(tail)-podExecStderrLimit:]
		}
		return &PodExecError{Stderr: tail, Err: err}
	}
	return nil
}

// execOut runs argv and returns its stdout as a string.
func (p *clientsetPodFS) execOut(ctx context.Context, t PodTarget, argv []string) (string, error) {
	var buf bytes.Buffer
	if err := p.exec(ctx, t, argv, nil, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// sh wraps a POSIX script so every caller-supplied path arrives as "$1", "$2",
// ... The "_" placeholder occupies $0.
func sh(script string, args ...string) []string {
	return append([]string{"sh", "-c", script, "_"}, args...)
}

// statFormat is understood by both GNU coreutils stat and busybox stat. %n is
// last so a name containing the separator cannot shift the numeric fields.
const statFormat = "%s|%Y|%A|%F|%n"

func (p *clientsetPodFS) List(ctx context.Context, t PodTarget, dir string) ([]FileEntry, bool, error) {
	script := `cd -- "$1" || exit 66
ls -A -- . 2>/dev/null | head -n "$2" | while IFS= read -r f; do
  stat -c '` + statFormat + `' -- "$f" 2>/dev/null || true
done`
	out, err := p.execOut(ctx, t, sh(script, dir, strconv.Itoa(maxListEntries+1)))
	if err != nil {
		return nil, false, err
	}
	entries := parseStatLines(out)
	truncated := false
	if len(entries) > maxListEntries {
		entries = entries[:maxListEntries]
		truncated = true
	}
	return entries, truncated, nil
}

func (p *clientsetPodFS) Stat(ctx context.Context, t PodTarget, path string) (FileEntry, error) {
	out, err := p.execOut(ctx, t, sh(`stat -c '`+statFormat+`' -- "$1"`, path))
	if err != nil {
		return FileEntry{}, err
	}
	entries := parseStatLines(out)
	if len(entries) == 0 {
		return FileEntry{}, fmt.Errorf("stat returned no output for %q", path)
	}
	return entries[0], nil
}

func (p *clientsetPodFS) ResolvePath(ctx context.Context, t PodTarget, path string) (string, error) {
	script := `p="$1"
if [ -d "$p" ]; then
  cd -- "$p" && pwd -P
else
  d=$(dirname -- "$p"); b=$(basename -- "$p")
  cd -- "$d" || exit 66
  dir=$(pwd -P)
  case "$dir" in
    */) printf '%s%s\n' "$dir" "$b" ;;
    *) printf '%s/%s\n' "$dir" "$b" ;;
  esac
fi`
	out, err := p.execOut(ctx, t, sh(script, path))
	if err != nil {
		return "", err
	}
	resolved := strings.TrimRight(out, "\r\n")
	if resolved == "" {
		return "", fmt.Errorf("could not resolve path %q", path)
	}
	return resolved, nil
}

func (p *clientsetPodFS) ResolveLink(ctx context.Context, t PodTarget, path string) (string, error) {
	out, err := p.execOut(ctx, t, sh(`readlink -f -- "$1"`, path))
	if err != nil {
		return "", err
	}
	resolved := strings.TrimRight(out, "\r\n")
	if resolved == "" {
		return "", fmt.Errorf("could not resolve symlink %q", path)
	}
	return resolved, nil
}

func (p *clientsetPodFS) ReadFile(ctx context.Context, t PodTarget, path string, limit int64, out io.Writer) error {
	if limit > 0 {
		return p.exec(ctx, t, sh(`head -c "$2" -- "$1"`, path, strconv.FormatInt(limit, 10)), nil, out)
	}
	return p.exec(ctx, t, sh(`cat -- "$1"`, path), nil, out)
}

// WriteFile streams in to a sibling temp file and renames it over the target,
// so a connection that dies mid-upload leaves the previous content intact
// rather than a half-written file.
func (p *clientsetPodFS) WriteFile(ctx context.Context, t PodTarget, path string, in io.Reader) error {
	script := `tmp="$1.dada-upload.$$"
if cat > "$tmp"; then
  mv -- "$tmp" "$1"
else
  rm -f -- "$tmp"
  exit 1
fi`
	return p.exec(ctx, t, sh(script, path), in, nil)
}

func (p *clientsetPodFS) Mkdir(ctx context.Context, t PodTarget, path string) error {
	return p.exec(ctx, t, sh(`mkdir -p -- "$1"`, path), nil, nil)
}

// Move refuses to clobber an existing destination: "mv -n" is not portable to
// every busybox build, so the check is explicit.
func (p *clientsetPodFS) Move(ctx context.Context, t PodTarget, from, to string) error {
	script := `if [ -e "$2" ]; then echo "destination exists" >&2; exit 67; fi
mv -- "$1" "$2"`
	return p.exec(ctx, t, sh(script, from, to), nil, nil)
}

func (p *clientsetPodFS) Remove(ctx context.Context, t PodTarget, path string, recursive bool) error {
	if recursive {
		return p.exec(ctx, t, sh(`rm -rf -- "$1"`, path), nil, nil)
	}
	script := `if [ -d "$1" ]; then rmdir -- "$1"; else rm -f -- "$1"; fi`
	return p.exec(ctx, t, sh(script, path), nil, nil)
}

func (p *clientsetPodFS) TarDir(ctx context.Context, t PodTarget, dir string, out io.Writer) error {
	return p.StreamTarball(ctx, t.Namespace, t.Pod, t.Container, dir, out)
}

// parseStatLines turns "size|mtime|mode|class|name" lines into entries,
// skipping anything malformed (a file name containing a newline produces such
// a line and is simply not listed).
func parseStatLines(out string) []FileEntry {
	var entries []FileEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			continue
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		mtime, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		name := parts[4]
		if name == "" || name == "." || name == ".." {
			continue
		}
		entries = append(entries, FileEntry{
			Name:    name,
			Kind:    fileKindFromStatClass(parts[3]),
			Size:    size,
			ModTime: mtime,
			Mode:    parts[2],
		})
	}
	return entries
}

// fileKindFromStatClass maps the stat %F description to a stable API value.
// GNU and busybox agree on "directory", "regular file", "regular empty file"
// and "symbolic link"; everything else is exposed as "other".
func fileKindFromStatClass(class string) string {
	switch {
	case strings.Contains(class, "directory"):
		return FileKindDir
	case strings.Contains(class, "symbolic link"):
		return FileKindSymlink
	case strings.Contains(class, "regular"):
		return FileKindFile
	default:
		return FileKindOther
	}
}

// unconfiguredPodFS is returned off-cluster: every method fails identically
// with the wrapped configuration error.
type unconfiguredPodFS struct{ err error }

func (u unconfiguredPodFS) Enabled() bool { return false }

func (u unconfiguredPodFS) FindRunningPod(context.Context, string, string) (string, string, error) {
	return "", "", u.notConfigured()
}

func (u unconfiguredPodFS) ResolvePath(context.Context, PodTarget, string) (string, error) {
	return "", u.notConfigured()
}

func (u unconfiguredPodFS) ResolveLink(context.Context, PodTarget, string) (string, error) {
	return "", u.notConfigured()
}

func (u unconfiguredPodFS) List(context.Context, PodTarget, string) ([]FileEntry, bool, error) {
	return nil, false, u.notConfigured()
}

func (u unconfiguredPodFS) Stat(context.Context, PodTarget, string) (FileEntry, error) {
	return FileEntry{}, u.notConfigured()
}

func (u unconfiguredPodFS) ReadFile(context.Context, PodTarget, string, int64, io.Writer) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) WriteFile(context.Context, PodTarget, string, io.Reader) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) Mkdir(context.Context, PodTarget, string) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) Move(context.Context, PodTarget, string, string) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) Remove(context.Context, PodTarget, string, bool) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) TarDir(context.Context, PodTarget, string, io.Writer) error {
	return u.notConfigured()
}

func (u unconfiguredPodFS) notConfigured() error {
	return fmt.Errorf("file access not configured: %w", u.err)
}
