package sourcedetect

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// staticDockerfile serves the build context as a static site.
//
// The base image is pinned and alpine-based so the build needs one pull and no
// package installs: an upload path whose whole promise is "a folder deploys"
// cannot afford a build that fetches a package index.
const staticDockerfile = `FROM nginx:1.27-alpine
COPY . /usr/share/nginx/html
EXPOSE 80
`

// StaticDockerfile returns the Dockerfile the platform writes on the user's
// behalf for an archive that is a site rather than a project.
func StaticDockerfile() string { return staticDockerfile }

// maxInjectBytes caps the uncompressed bytes InjectDockerfile will write. The
// input is already capped at 100MB compressed by the upload handler, but a
// crafted archive expands far past that, and this function holds the whole
// result in memory.
const maxInjectBytes = 200 * 1024 * 1024

// InjectDockerfile rewrites an uploaded archive so that its web root becomes
// the archive root and a generated Dockerfile sits beside it.
//
// It exists because labelling the archive is not enough to change what gets
// built. The control plane hands Jenkins only a coarse build family
// (web, android or auto — see build-agent/internal/detect and the params map in
// build-agent/internal/worker/runner.go), and the pipeline re-detects the stack
// itself after unpacking the archive. A framework name invented at upload time
// never reaches the step that decides how to build, so the only signal that
// survives the trip is a real Dockerfile inside the build context.
//
// A Dockerfile the user wrote is never replaced: if one is already at the root
// after stripping, the input is returned untouched, because a generated wrapper
// that overrode it would silently deploy something other than what the user
// packaged.
func InjectDockerfile(data []byte, format Format, root string, dockerfile string) ([]byte, error) {
	switch format {
	case FormatZip:
		return injectZip(data, root, dockerfile)
	case FormatTarGz:
		return injectTarGz(data, root, dockerfile)
	default:
		return nil, fmt.Errorf("inject dockerfile: unsupported format %q", format)
	}
}

// injectName maps an archive member onto its name in the rewritten archive,
// reporting false for members that must be dropped: anything outside the web
// root, tooling residue, and any path that escapes the context via an absolute
// path or a ".." segment, which an unpacking step would otherwise write outside
// the build directory.
func injectName(name, root string) (string, bool) {
	if isToolingPath(name) {
		return "", false
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return "", false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", false
		}
	}
	rel := strings.TrimPrefix(name, root)
	if rel == name && root != "" {
		return "", false
	}
	if rel == "" || strings.HasSuffix(rel, "/") {
		return "", false
	}
	return rel, true
}

func injectZip(data []byte, root, dockerfile string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("inject dockerfile: read zip: %w", err)
	}

	for _, f := range zr.File {
		if rel, ok := injectName(f.Name, root); ok && rel == "Dockerfile" {
			return data, nil
		}
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	written := int64(0)

	for _, f := range zr.File {
		rel, ok := injectName(f.Name, root)
		if !ok {
			continue
		}
		written += int64(f.UncompressedSize64)
		if written > maxInjectBytes {
			return nil, fmt.Errorf("inject dockerfile: archive expands past %d bytes", int64(maxInjectBytes))
		}
		hdr := f.FileHeader
		hdr.Name = rel
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, fmt.Errorf("inject dockerfile: write %q: %w", rel, err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("inject dockerfile: open %q: %w", f.Name, err)
		}
		if _, err := io.Copy(w, io.LimitReader(rc, maxInjectBytes)); err != nil {
			rc.Close()
			return nil, fmt.Errorf("inject dockerfile: copy %q: %w", f.Name, err)
		}
		rc.Close()
	}

	w, err := zw.Create("Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("inject dockerfile: create Dockerfile: %w", err)
	}
	if _, err := io.WriteString(w, dockerfile); err != nil {
		return nil, fmt.Errorf("inject dockerfile: write Dockerfile: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("inject dockerfile: finish zip: %w", err)
	}
	return out.Bytes(), nil
}

func injectTarGz(data []byte, root, dockerfile string) ([]byte, error) {
	has, err := tarHasRootDockerfile(data, root)
	if err != nil {
		return nil, err
	}
	if has {
		return data, nil
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("inject dockerfile: read gzip: %w", err)
	}
	defer gr.Close()

	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gw)
	tr := tar.NewReader(gr)
	written := int64(0)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("inject dockerfile: read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := injectName(hdr.Name, root)
		if !ok {
			continue
		}
		written += hdr.Size
		if written > maxInjectBytes {
			return nil, fmt.Errorf("inject dockerfile: archive expands past %d bytes", int64(maxInjectBytes))
		}
		nh := *hdr
		nh.Name = rel
		if err := tw.WriteHeader(&nh); err != nil {
			return nil, fmt.Errorf("inject dockerfile: write header %q: %w", rel, err)
		}
		if _, err := io.Copy(tw, io.LimitReader(tr, maxInjectBytes)); err != nil {
			return nil, fmt.Errorf("inject dockerfile: copy %q: %w", hdr.Name, err)
		}
	}

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "Dockerfile",
		Mode:     0o644,
		Size:     int64(len(dockerfile)),
	}); err != nil {
		return nil, fmt.Errorf("inject dockerfile: write Dockerfile header: %w", err)
	}
	if _, err := io.WriteString(tw, dockerfile); err != nil {
		return nil, fmt.Errorf("inject dockerfile: write Dockerfile: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("inject dockerfile: finish tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("inject dockerfile: finish gzip: %w", err)
	}
	return out.Bytes(), nil
}

func tarHasRootDockerfile(data []byte, root string) (bool, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return false, fmt.Errorf("inject dockerfile: read gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inject dockerfile: read tar: %w", err)
		}
		if rel, ok := injectName(hdr.Name, root); ok && rel == "Dockerfile" {
			return true, nil
		}
	}
}
