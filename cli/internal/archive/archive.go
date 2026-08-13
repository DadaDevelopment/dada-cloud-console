// Package archive packages a project directory into a tar.gz suitable for
// the console's source-archive upload endpoint, respecting the project's
// .gitignore and a hardcoded set of directories that are never worth
// uploading.
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// MaxBytes mirrors uploadSourceMaxBytes in
// backend/internal/api/uploadsource.go. Checked client-side before any
// network call so an oversized project fails fast with a plain-language
// message instead of a 413 after minutes of upload.
const MaxBytes = 100 * 1024 * 1024

// alwaysExcludedDirs are pruned unconditionally, regardless of .gitignore,
// because every one of them regularly blows the 100MB budget on projects
// that never bothered to gitignore their own build output.
var alwaysExcludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"venv":         true,
	"__pycache__":  true,
}

// Entry describes one file that will be packaged, used by Plan to report a
// size estimate before any bytes are written.
type Entry struct {
	RelPath string
	Size    int64
}

// Plan walks root and returns every file that packaging would include, sorted
// by RelPath for deterministic output, without touching the network or
// writing an archive. Callers use it to enforce MaxBytes before spending time
// on gzip.
func Plan(root string) ([]Entry, int64, error) {
	gi, err := LoadGitignore(root)
	if err != nil {
		return nil, 0, fmt.Errorf("reading .gitignore: %w", err)
	}

	var entries []Entry
	var total int64

	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if alwaysExcludedDirs[path.Base(rel)] {
				return filepath.SkipDir
			}
			if gi.Match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if gi.Match(rel, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		entries = append(entries, Entry{RelPath: rel, Size: info.Size()})
		total += info.Size()
		return nil
	})
	if walkErr != nil {
		return nil, 0, walkErr
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })
	return entries, total, nil
}

// Build tars and gzips every entry from Plan into an in-memory buffer. It
// does not itself enforce MaxBytes on the uncompressed total - callers should
// check Plan's total first, since gzip can occasionally make a highly
// compressible tree land under the limit even when the raw bytes exceed it,
// and rejecting on the raw sum is the honest, unsurprising rule to explain to
// a user.
func Build(root string, entries []Entry) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		full := filepath.Join(root, filepath.FromSlash(e.RelPath))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", e.RelPath, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, fmt.Errorf("header for %s: %w", e.RelPath, err)
		}
		hdr.Name = e.RelPath
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write header for %s: %w", e.RelPath, err)
		}
		f, err := os.Open(full)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", e.RelPath, err)
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("write %s: %w", e.RelPath, copyErr)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("closing tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("closing gzip: %w", err)
	}
	return buf.Bytes(), nil
}
