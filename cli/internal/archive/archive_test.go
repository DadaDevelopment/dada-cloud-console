package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanExcludesAlwaysExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main")
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	writeFile(t, dir, "node_modules/pkg/index.js", "module.exports = {}")
	writeFile(t, dir, ".next/cache/x", "cache")
	writeFile(t, dir, "dist/bundle.js", "bundled")
	writeFile(t, dir, "build/out.o", "obj")
	writeFile(t, dir, "venv/lib/site.py", "x=1")
	writeFile(t, dir, "__pycache__/x.pyc", "compiled")

	entries, _, err := Plan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].RelPath != "main.go" {
		t.Fatalf("expected only main.go, got %+v", entries)
	}
}

// TestPlanHonorsGitignore checks a "*.log" glob exclude plus a directory
// exclude with a negated file inside it. Per git's own documented behavior,
// a directory-level ignore ("secrets/") prunes the whole subtree before any
// negation rule inside it gets a chance to run, so "!secrets/keep.txt" does
// not resurrect that file - Plan must match that behavior, not fight it.
func TestPlanHonorsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.log\nsecrets/\n!secrets/keep.txt\n")
	writeFile(t, dir, "app.py", "print(1)")
	writeFile(t, dir, "debug.log", "boom")
	writeFile(t, dir, "secrets/token.txt", "shh")
	writeFile(t, dir, "secrets/keep.txt", "public")

	entries, _, err := Plan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.RelPath)
	}
	sort.Strings(got)

	want := []string{".gitignore", "app.py"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBuildProducesReadableArchive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello")
	writeFile(t, dir, "sub/b.txt", "world")

	entries, total, err := Plan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len("hello")+len("world")) {
		t.Fatalf("unexpected total size %d", total)
	}

	data, err := Build(dir, entries)
	if err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		found[hdr.Name] = string(b)
	}
	if found["a.txt"] != "hello" || found["sub/b.txt"] != "world" {
		t.Fatalf("unexpected archive contents: %+v", found)
	}
}

func TestPlanSizeExceedsMaxBytesIsDetectable(t *testing.T) {
	dir := t.TempDir()
	big := bytes.Repeat([]byte("x"), 1024)
	writeFile(t, dir, "big.bin", string(big))

	_, total, err := Plan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if total > MaxBytes {
		t.Fatalf("test fixture unexpectedly exceeds MaxBytes")
	}
	if total != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", total)
	}
}
