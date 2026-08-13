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
	if res.Framework != "nextjs" || res.Port != 3000 {
		t.Errorf("got framework=%q port=%d, want nextjs/3000", res.Framework, res.Port)
	}
}

// TestDetectZipTelegramBot pins the upload flow's headline case: a plain
// python worker with no web framework. It must resolve to "python", never to
// the empty framework — an empty framework reaches dadaBuildPipeline with no
// template and fails the build with no_dockerfile.
func TestDetectZipTelegramBot(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"demo-bot/requirements.txt", "aiogram==3.15.0\n"},
		{"demo-bot/bot.py", "import asyncio"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "python" {
		t.Errorf("Framework = %q, want python", res.Framework)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0 (a long-polling bot listens on nothing)", res.Port)
	}
}

// TestDetectZipPlainNode covers a node app with no recognized framework: it
// still has to name a framework the pipeline can template.
func TestDetectZipPlainNode(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"package.json", `{"dependencies":{"telegraf":"4.16.0"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "node" {
		t.Errorf("Framework = %q, want node", res.Framework)
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
	if res.Framework != "react" {
		t.Errorf("Framework = %q, want react (zip-slip entry must be skipped, not crash)", res.Framework)
	}
}

type tarLink struct {
	name   string
	target string
}

func buildTarGzWithLinks(t *testing.T, files []zipFile, links []tarLink) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, l := range links {
		hdr := &tar.Header{
			Name:     l.name,
			Linkname: l.target,
			Typeflag: tar.TypeSymlink,
			Mode:     0777,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar symlink header %s: %v", l.name, err)
		}
	}
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0644, Size: int64(len(f.body))}
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

func TestDetectFollowsRootDockerfileSymlink(t *testing.T) {
	data := buildTarGzWithLinks(t,
		[]zipFile{{"app/docker/Dockerfile.debian", "FROM alpine\nEXPOSE 8080\n"}},
		[]tarLink{{"app/Dockerfile", "docker/Dockerfile.debian"}},
	)
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "docker" {
		t.Errorf("Framework = %q, want docker (root Dockerfile is a symlink, as in vaultwarden)", res.Framework)
	}
	if res.Port != 8080 {
		t.Errorf("Port = %d, want 8080 from the link target's EXPOSE", res.Port)
	}
}

func TestDetectDanglingSymlinkIsNotAManifest(t *testing.T) {
	data := buildTarGzWithLinks(t,
		[]zipFile{{"app/package.json", `{"dependencies":{"react-scripts":"5.0.0"}}`}},
		[]tarLink{{"app/Dockerfile", "docker/Dockerfile.missing"}},
	)
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "react" {
		t.Errorf("Framework = %q, want react: a symlink pointing at nothing must not answer for the Dockerfile", res.Framework)
	}
}

func TestDetectSymlinkCycleTerminates(t *testing.T) {
	data := buildTarGzWithLinks(t, nil, []tarLink{
		{"app/Dockerfile", "Dockerfile.a"},
		{"app/Dockerfile.a", "Dockerfile"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Errorf("Framework = %q, want empty for a symlink cycle", res.Framework)
	}
}

func TestDetectComposePortWhenDockerfileHasNoExpose(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\nCMD [\"sh\"]\n"},
		{"app/docker-compose.yml", "services:\n  web:\n    build: .\n    ports:\n      - \"3000:80\"\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "docker" || res.Port != 80 {
		t.Errorf("got %q:%d, want docker:80 (container side of the compose mapping)", res.Framework, res.Port)
	}
}

func TestDetectComposeWithSeveralPortsStaysSilent(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\n"},
		{"app/compose.yaml", "services:\n  web:\n    ports:\n      - \"3000:80\"\n  api:\n    ports:\n      - \"9000:9000\"\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0: two services disagree, and a guessed port is worse than none", res.Port)
	}
}

func TestDetectExposeWinsOverCompose(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\nEXPOSE 5000\n"},
		{"app/docker-compose.yml", "services:\n  web:\n    ports:\n      - \"8080:8080\"\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 5000 {
		t.Errorf("Port = %d, want 5000: EXPOSE is the deploy contract, compose is only the fallback", res.Port)
	}
}

func TestDetectExposeResolvesEnvVar(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\nENV PORT=3000\nEXPOSE $PORT\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 3000 {
		t.Errorf("Port = %d, want 3000: EXPOSE $PORT with ENV PORT=3000 above states the port", res.Port)
	}
}

func TestDetectExposeResolvesBracedArg(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\nARG APP_PORT 8080\nEXPOSE ${APP_PORT}/tcp\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 8080 {
		t.Errorf("Port = %d, want 8080", res.Port)
	}
}

func TestDetectExposeUnresolvedVarStaysSilent(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile", "FROM alpine\nEXPOSE $MYSTERY_PORT\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0: an unresolvable variable is not a port, and guessing one has already caused an outage", res.Port)
	}
}

func TestDetectIgnoresDevcontainerDockerfile(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/.devcontainer/Dockerfile", "FROM alpine\nEXPOSE 1234\n"},
		{"app/docker/Dockerfile", "FROM alpine\nENV APP_PORT=9000\nEXPOSE ${APP_PORT}\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "docker" || res.Port != 9000 {
		t.Errorf("got %s:%d, want docker:9000: a devcontainer describes the dev box, not the app", res.Framework, res.Port)
	}
}

func TestDetectRootProductionDockerfile(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Dockerfile.production", "FROM alpine\nEXPOSE 2368\n"},
		{"app/package.json", `{"dependencies":{"express":"4"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "docker" || res.Port != 2368 {
		t.Errorf("got %s:%d, want docker:2368", res.Framework, res.Port)
	}
}

func TestDetectGoModule(t *testing.T) {
	data := buildTarGz(t, []zipFile{{"app/go.mod", "module example.com/x\n\ngo 1.23\n"}})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "go" || res.Port != 8080 {
		t.Errorf("got %s:%d, want go:8080", res.Framework, res.Port)
	}
}

func TestDetectMavenProject(t *testing.T) {
	data := buildTarGz(t, []zipFile{{"app/pom.xml", "<project></project>\n"}})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "maven" || res.Port != 8080 {
		t.Errorf("got %s:%d, want maven:8080", res.Framework, res.Port)
	}
}

func TestDetectProcfileLeavesPortToPlatform(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Procfile", "web: npm start\n"},
		{"app/package.json", `{"dependencies":{"express":"4"}}`},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "node" {
		t.Errorf("Framework = %q, want node", res.Framework)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0: a Procfile app listens on the $PORT the platform assigns", res.Port)
	}
}

func TestDetectProcfileKeepsDockerfileEvidence(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/Procfile", "web: ./server\n"},
		{"app/Dockerfile", "FROM alpine\nEXPOSE 4567\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Port != 4567 {
		t.Errorf("Port = %d, want 4567: EXPOSE is evidence, not a per-framework default", res.Port)
	}
}

func TestDetectComposeBeatsFrameworkDefault(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/package.json", `{"dependencies":{"express":"4"}}`},
		{"app/docker-compose.yml", "services:\n  app:\n    ports:\n      - \"${LD_HOST_PORT:-9090}:9090\"\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "node" || res.Port != 9090 {
		t.Errorf("got %s:%d, want node:9090: a compose mapping is evidence, the framework default is only a convention", res.Framework, res.Port)
	}
}

func TestDetectRailwayConfigLeavesPortToPlatform(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"app/railway.json", `{"build":{"builder":"NIXPACKS"}}`},
		{"app/requirements.txt", "Django==5.0\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "django" || res.Port != 0 {
		t.Errorf("got %s:%d, want django:0", res.Framework, res.Port)
	}
}

// TestDetectManifestlessPythonScripts pins the shape that failed a real
// upload three times on 2026-08-13: a script-style python project — modules
// and an entrypoint at the root, no requirements.txt, no pyproject.toml, no
// Dockerfile. It must resolve to "python", because dadaBuildPipeline's python
// branch builds exactly this (install step skips when no manifest is present,
// start step falls back to any *.py) while an empty framework aborts the
// build with no_dockerfile.
func TestDetectManifestlessPythonScripts(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{"agent.py", "import sys"},
		{"serve.py", "import http.server"},
		{"connectors/base.py", "class Base: pass"},
		{"README.md", "# genagent"},
		{"ui/index.html", "<html></html>"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "python" {
		t.Errorf("Framework = %q, want python (script-style project ships no manifest)", res.Framework)
	}
	if res.Port != 0 {
		t.Errorf("Port = %d, want 0 (nothing declares a listen port)", res.Port)
	}
}

// TestDetectNestedPythonScriptDoesNotHijackNodeRepo guards the other side of
// the fallback: a helper script buried in a JS repo must not turn that repo
// into a python build.
func TestDetectNestedPythonScriptDoesNotHijackNodeRepo(t *testing.T) {
	data := buildZip(t, []zipFile{
		{"app/index.js", "console.log(1)"},
		{"app/scripts/gen.py", "print(1)"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Errorf("Framework = %q, want \"\" (a nested helper script is not the app)", res.Framework)
	}
}

// TestDetectDotSlashPrefixedTarPythonScripts is the shape `tar czf x.tar.gz -C
// dir .` writes and a live sandbox upload proved on 2026-08-13: every member
// carries a "./" prefix and macOS adds AppleDouble sidecars ("._foo") beside
// them. Before normalization the top-level "._." member made root detection
// answer "", every remaining name then looked nested, and the manifest-less
// python fallback returned no framework at all.
func TestDetectDotSlashPrefixedTarPythonScripts(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: "._.", body: "appledouble"},
		{name: "./._agent.py", body: "appledouble"},
		{name: "./agent.py", body: "print('hi')\n"},
		{name: "./main.py", body: "print('hi')\n"},
		{name: "./connectors/base.py", body: "print('hi')\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "python" {
		t.Fatalf("framework = %q, want python", res.Framework)
	}
}

// TestDetectDotSlashPrefixedTarSingleRoot pins the other half of the same bug:
// with "./" stripped, a single-root export still has its root detected and its
// manifest found, instead of every path reading as one level deeper.
func TestDetectDotSlashPrefixedTarSingleRoot(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: "./myapp/package.json", body: `{"dependencies":{"next":"14.0.0"}}`},
		{name: "./myapp/src/index.js", body: "console.log(1)\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "nextjs" {
		t.Fatalf("framework = %q, want nextjs", res.Framework)
	}
}

// TestDetectSingleRootBesideToolingDirs is the "tree" upload of 2026-08-13: a
// user zipped the folder their editor works in, so ".claude/" sat beside the
// project directory. Counting that sidecar as content made the archive look
// multi-rooted, nothing was stripped, and the answer was "no framework" for a
// project whose python sources sat one directory down.
func TestDetectSingleRootBesideToolingDirs(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: ".claude/scheduled_tasks.lock", body: "lock\n"},
		{name: "genagent/README.md", body: "hi\n"},
		{name: "genagent/agent.py", body: "print('hi')\n"},
		{name: "genagent/connectors/base.py", body: "print('hi')\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "python" {
		t.Fatalf("framework = %q, want python", res.Framework)
	}
}

// TestDetectToolingDirsDoNotInventARoot pins the other side: two real project
// directories are still no single root, so nothing is stripped and a manifest
// buried in one of them does not decide the whole archive.
func TestDetectToolingDirsDoNotInventARoot(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: ".git/config", body: "\n"},
		{name: "frontend/package.json", body: `{"dependencies":{"next":"14.0.0"}}`},
		{name: "backend/pyproject.toml", body: "[project]\nname = \"x\"\n"},
		{name: "backend/main.py", body: "print('hi')\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Fatalf("framework = %q, want empty (two real roots must not be stripped)", res.Framework)
	}
}

// TestDetectPythonSourcesBesideRootDataFile is the exact shape of the "tree"
// upload that stayed red for three hours on 2026-08-13: ".claude/" and
// "genagent/" beside a loose "input.txt" the agent reads at runtime. The loose
// file forbids stripping a root — that would delete the app's own data — so the
// archive must still resolve to python, with the whole archive as the context.
func TestDetectPythonSourcesBesideRootDataFile(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: ".claude/scheduled_tasks.lock", body: "lock\n"},
		{name: "input.txt", body: "data\n"},
		{name: "genagent/main.py", body: "print('hi')\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "python" {
		t.Fatalf("framework = %q, want python", res.Framework)
	}
}

// TestDetectTwoContentDirsAreNotAPythonApp pins the limit: a python directory
// beside another project directory is two projects, and building the python one
// would ship half the repo.
func TestDetectTwoContentDirsAreNotAPythonApp(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: "README.md", body: "hi\n"},
		{name: "worker/main.py", body: "print('hi')\n"},
		{name: "site/index.html", body: "<html>\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Fatalf("framework = %q, want empty (two content dirs are not one python app)", res.Framework)
	}
}

// TestDetectNestedPythonDoesNotHijackANodeRepo keeps a helper script from
// turning a JS project into a python build: package.json still decides.
func TestDetectNestedPythonDoesNotHijackANodeRepo(t *testing.T) {
	data := buildTarGz(t, []zipFile{
		{name: "package.json", body: `{"dependencies":{"vite":"5.0.0"}}`},
		{name: "scripts/gen.py", body: "print('hi')\n"},
	})
	res, err := Detect(data)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "vite" {
		t.Fatalf("framework = %q, want vite", res.Framework)
	}
}
