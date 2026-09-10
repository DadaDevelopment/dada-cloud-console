package sourcedetect

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

// zipOf builds an in-memory zip whose members are the given name to content
// pairs, which is the shape a browser upload arrives in.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// tarGzOf is zipOf for the other container the upload endpoint accepts.
func tarGzOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
		}); err != nil {
			t.Fatalf("header %q: %v", name, err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func zipNames(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %q: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %q: %v", f.Name, err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func tarGzNames(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read body %q: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(body)
	}
	return out
}

// TestStaticSiteRoot covers the shapes an uploaded site arrives in and the
// shapes that must never be mistaken for one. The disqualifier cases matter
// most: a repo that ships index.html beside package.json is a frontend project
// whose index.html is a source file, and serving it raw would publish an app
// that never ran its own build.
func TestStaticSiteRoot(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		wantRoot string
		wantOK   bool
	}{
		{"root index", []string{"index.html", "style.css"}, "", true},
		{"wrapped in one dir", []string{"site-main/index.html", "site-main/app.js"}, "site-main/", true},
		{"dist subdir", []string{"index.md", "dist/index.html", "dist/main.css"}, "dist/", true},
		{"public subdir", []string{"readme.txt", "public/index.html"}, "public/", true},
		{"wrapped dist", []string{"proj/README.md", "proj/dist/index.html"}, "proj/dist/", true},
		{"uppercase index", []string{"Index.HTML"}, "", true},
		{"package.json wins", []string{"index.html", "package.json"}, "", false},
		{"python source wins", []string{"index.html", "app.py"}, "", false},
		{"dockerfile wins", []string{"index.html", "Dockerfile"}, "", false},
		{"go module wins", []string{"index.html", "go.mod"}, "", false},
		{"two candidate dirs", []string{"dist/index.html", "public/index.html"}, "", false},
		{"no index at all", []string{"main.css", "logo.png"}, "", false},
		{"index too deep", []string{"a/b/c/index.html", "z/other.txt"}, "", false},
		{"macosx sidecar ignored", []string{"__MACOSX/._index.html", "index.html"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, ok := StaticSiteRoot(tc.names)
			if ok != tc.wantOK || root != tc.wantRoot {
				t.Fatalf("StaticSiteRoot(%v) = (%q, %v), want (%q, %v)", tc.names, root, ok, tc.wantRoot, tc.wantOK)
			}
		})
	}
}

// TestDetectStaticArchive is the guarantee the upload path depends on: a
// folder of html that carries no manifest at all resolves to a framework and a
// port instead of to the empty verdict that ended in framework_undetected.
func TestDetectStaticArchive(t *testing.T) {
	res, err := Detect(zipOf(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"style.css":  "body{}",
	}))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "static" || res.Port != 80 || res.StaticRoot != "" {
		t.Fatalf("Detect = %+v, want framework static, port 80, root \"\"", res)
	}
}

// TestDetectStaticDoesNotOverrideNode guards the ordering: static wrapping is
// the last resort, so an archive that also carries package.json must still be
// built as the Node project it is.
func TestDetectStaticDoesNotOverrideNode(t *testing.T) {
	res, err := Detect(zipOf(t, map[string]string{
		"index.html":   "<h1>hi</h1>",
		"package.json": `{"name":"x","dependencies":{"next":"14.0.0"},"scripts":{"start":"next start"}}`,
	}))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework == "static" {
		t.Fatalf("Detect = %+v, static must not win over a package.json repo", res)
	}
}

func TestInjectDockerfileZip(t *testing.T) {
	in := zipOf(t, map[string]string{"index.html": "<h1>hi</h1>", "style.css": "body{}"})
	out, err := InjectDockerfile(in, FormatZip, "", StaticDockerfile())
	if err != nil {
		t.Fatalf("InjectDockerfile: %v", err)
	}
	got := zipNames(t, out)
	if got["Dockerfile"] != StaticDockerfile() {
		t.Fatalf("Dockerfile = %q, want the generated one", got["Dockerfile"])
	}
	if got["index.html"] != "<h1>hi</h1>" {
		t.Fatalf("index.html = %q, want the original bytes", got["index.html"])
	}
}

// TestInjectDockerfileZipStripsRoot proves the web root becomes the build
// context root, which is what makes COPY . serve the right directory.
func TestInjectDockerfileZipStripsRoot(t *testing.T) {
	in := zipOf(t, map[string]string{
		"proj/README.md":       "notes",
		"proj/dist/index.html": "<h1>hi</h1>",
		"proj/dist/app.js":     "console.log(1)",
	})
	out, err := InjectDockerfile(in, FormatZip, "proj/dist/", StaticDockerfile())
	if err != nil {
		t.Fatalf("InjectDockerfile: %v", err)
	}
	got := zipNames(t, out)
	if _, ok := got["index.html"]; !ok {
		t.Fatalf("members = %v, want index.html at top level", got)
	}
	if _, ok := got["Dockerfile"]; !ok {
		t.Fatalf("members = %v, want a Dockerfile", got)
	}
	for name := range got {
		if len(name) > 5 && name[:5] == "proj/" {
			t.Fatalf("member %q kept the stripped prefix", name)
		}
	}
	if _, ok := got["README.md"]; ok {
		t.Fatalf("members = %v, README.md lives outside the web root", got)
	}
}

func TestInjectDockerfileTarGz(t *testing.T) {
	in := tarGzOf(t, map[string]string{"site/index.html": "<h1>hi</h1>"})
	out, err := InjectDockerfile(in, FormatTarGz, "site/", StaticDockerfile())
	if err != nil {
		t.Fatalf("InjectDockerfile: %v", err)
	}
	got := tarGzNames(t, out)
	if got["index.html"] != "<h1>hi</h1>" || got["Dockerfile"] != StaticDockerfile() {
		t.Fatalf("members = %v, want index.html plus the generated Dockerfile", got)
	}
}

// TestInjectDockerfileKeepsUserDockerfile is the promise that the platform
// never overrides what the user packaged.
func TestInjectDockerfileKeepsUserDockerfile(t *testing.T) {
	in := zipOf(t, map[string]string{"Dockerfile": "FROM scratch\n", "index.html": "<h1>hi</h1>"})
	out, err := InjectDockerfile(in, FormatZip, "", StaticDockerfile())
	if err != nil {
		t.Fatalf("InjectDockerfile: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("archive was rewritten even though it ships its own Dockerfile")
	}
}

// TestInjectDockerfileDropsEscapingPaths keeps an unpacking step from writing
// outside the build context.
func TestInjectDockerfileDropsEscapingPaths(t *testing.T) {
	in := zipOf(t, map[string]string{
		"index.html":   "<h1>hi</h1>",
		"../evil.txt":  "pwn",
		"/etc/passwd":  "root",
		"a/../ok.html": "fine",
	})
	out, err := InjectDockerfile(in, FormatZip, "", StaticDockerfile())
	if err != nil {
		t.Fatalf("InjectDockerfile: %v", err)
	}
	for name := range zipNames(t, out) {
		if name == "../evil.txt" || name == "/etc/passwd" || name == "a/../ok.html" {
			t.Fatalf("escaping member %q survived the rewrite", name)
		}
	}
}
