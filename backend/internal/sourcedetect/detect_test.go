package sourcedetect

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

type zipFile struct {
	name string
	body string
}

func buildZip(t *testing.T, files []zipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", f.name, err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatalf("zip write %s: %v", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func buildTarGz(t *testing.T, files []zipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, f := range files {
		hdr := &tar.Header{
			Name: f.name,
			Mode: 0644,
			Size: int64(len(f.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", f.name, err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("tar write %s: %v", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestDetectUnrecognizedFormat(t *testing.T) {
	_, err := Detect([]byte("not an archive"))
	if err == nil {
		t.Fatal("expected error for non-archive bytes")
	}
}

func TestDetectZipDockerfile(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"Dockerfile", "FROM node:20\nEXPOSE 4000\nCMD [\"node\", \"server.js\"]\n"},
		{"server.js", "console.log('hi')"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Format != FormatZip {
		t.Errorf("Format = %q, want zip", res.Format)
	}
	if res.Framework != "docker" {
		t.Errorf("Framework = %q, want docker", res.Framework)
	}
	if res.Port != 4000 {
		t.Errorf("Port = %d, want 4000", res.Port)
	}
}

func TestDetectZipViteRoot(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"my-lovable-app/package.json", `{"dependencies":{"react":"18.0.0","vite":"5.0.0"}}`},
		{"my-lovable-app/src/main.tsx", "export default 1"},
		{"my-lovable-app/node_modules/some-dep/package.json", `{"dependencies":{"next":"14.0.0"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "vite" {
		t.Errorf("Framework = %q, want vite (node_modules manifest must not shadow root)", res.Framework)
	}
	if res.Port != 5173 {
		t.Errorf("Port = %d, want 5173", res.Port)
	}
}

func TestDetectZipNext(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"package.json", `{"dependencies":{"next":"14.0.0","react":"18.0.0"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "next" || res.Port != 3000 {
		t.Errorf("got framework=%q port=%d, want next/3000", res.Framework, res.Port)
	}
}

func TestDetectTarGzFastAPI(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/requirements.txt", "fastapi==0.110.0\nuvicorn[standard]==0.29.0\n"},
		{"app/main.py", "app = None"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Format != FormatTarGz {
		t.Errorf("Format = %q, want tar.gz", res.Format)
	}
	if res.Framework != "fastapi" || res.Port != 8000 {
		t.Errorf("got framework=%q port=%d, want fastapi/8000", res.Framework, res.Port)
	}
}

func TestDetectTarGzDjangoPyproject(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"pyproject.toml", "[project]\nname = \"site\"\ndependencies = [\n  \"django>=5.0\",\n]\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "django" || res.Port != 8000 {
		t.Errorf("got framework=%q port=%d, want django/8000", res.Framework, res.Port)
	}
}

func TestDetectNoManifestMatch(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"readme.txt", "hello"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Errorf("Framework = %q, want empty", res.Framework)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0", res.Port)
	}
}

func TestDetectZipSlipEntriesSkipped(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"../../etc/passwd", "root:x:0:0"},
		{"package.json", `{"dependencies":{"react-scripts":"5.0.0"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "react-scripts" {
		t.Errorf("Framework = %q, want react-scripts (zip-slip entry must be skipped, not crash)", res.Framework)
	}
}
