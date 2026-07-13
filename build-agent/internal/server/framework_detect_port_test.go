package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// fakeGitHubContents serves the GitHub "contents" API from an in-memory file
// map so framework/port detection can be exercised hermetically (no token, no
// network). Keys are repo-relative POSIX paths; directory listings are derived
// from the key set.
type fakeGitHubContents struct {
	owner   string
	repo    string
	files   map[string]string
	errDirs map[string]bool
}

func (g fakeGitHubContents) prefix() string {
	return "/repos/" + g.owner + "/" + g.repo + "/contents"
}

func (g fakeGitHubContents) children(dir string) []map[string]any {
	dir = strings.Trim(dir, "/")
	seen := map[string]bool{}
	out := []map[string]any{}
	for p := range g.files {
		rel := p
		if dir != "" {
			if !strings.HasPrefix(p, dir+"/") {
				continue
			}
			rel = p[len(dir)+1:]
		}
		segs := strings.SplitN(rel, "/", 2)
		name := segs[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		typ := "file"
		if len(segs) > 1 {
			typ = "dir"
		}
		child := name
		if dir != "" {
			child = dir + "/" + name
		}
		out = append(out, map[string]any{"type": typ, "name": name, "path": child})
	}
	return out
}

func (g fakeGitHubContents) roundTrip(t *testing.T) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		rest := strings.TrimPrefix(req.URL.Path, g.prefix())
		rest = strings.Trim(rest, "/")
		if g.errDirs[rest] {
			return jsonResponse(t, http.StatusInternalServerError, map[string]string{"error": "boom"}), nil
		}
		if raw, ok := g.files[rest]; ok {
			return jsonResponse(t, http.StatusOK, map[string]any{
				"type": "file", "name": rest, "path": rest,
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw)),
			}), nil
		}
		kids := g.children(rest)
		if len(kids) == 0 {
			return jsonResponse(t, http.StatusNotFound, map[string]string{"error": "not found"}), nil
		}
		return jsonResponse(t, http.StatusOK, kids), nil
	}
}

func detectFakeRepo(t *testing.T, files map[string]string) frameworkDetection {
	t.Helper()
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	fake := fakeGitHubContents{owner: "org", repo: "app", files: files}
	githubHTTPClient = &http.Client{Transport: fake.roundTrip(t)}
	det, err := detectWithToken(context.Background(), "tok", "org/app", "")
	if err != nil {
		t.Fatalf("detectWithToken: %v", err)
	}
	return det
}

type portCase struct {
	name     string
	files    map[string]string
	wantFW   string
	wantPort int
}

func runPortCases(t *testing.T, cases []portCase) {
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			det := detectFakeRepo(t, tc.files)
			if det.Framework == nil {
				t.Fatalf("framework = nil, want %q (port %d)", tc.wantFW, tc.wantPort)
			}
			if *det.Framework != tc.wantFW {
				t.Fatalf("framework = %q, want %q", *det.Framework, tc.wantFW)
			}
			if det.Port == nil {
				t.Fatalf("port = nil, want %d", tc.wantPort)
			}
			if *det.Port != tc.wantPort {
				t.Fatalf("port = %d, want %d (framework %s)", *det.Port, tc.wantPort, *det.Framework)
			}
		})
	}
}

func pkg(deps, extra string) string {
	return `{"dependencies":{` + deps + `},"scripts":{"build":"build","start":"start"}` + extra + `}`
}

func pkgWith(deps, scripts string) string {
	return `{"dependencies":{` + deps + `},"scripts":{` + scripts + `}}`
}

// TestDetectPortDockerfileExpose is the regression lock for the bug in the
// import wizard: a repo carrying its own Dockerfile must take the port from the
// Dockerfile's EXPOSE directive, not from the static per-framework default.
func TestDetectPortDockerfileExpose(t *testing.T) {
	runPortCases(t, []portCase{
		{"streamlit-expose-8501", map[string]string{"Dockerfile": "FROM python:3.10-slim\nEXPOSE 8501\n"}, "dockerfile", 8501},
		{"expose-with-proto", map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 8080/tcp\n"}, "dockerfile", 8080},
		{"expose-commented-then-real", map[string]string{"Dockerfile": "FROM x\n# EXPOSE 9999\nEXPOSE 4000\n"}, "dockerfile", 4000},
		{"expose-multi-first-wins", map[string]string{"Dockerfile": "FROM x\nEXPOSE 7000 7001\n"}, "dockerfile", 7000},
		{"no-expose-falls-to-default", map[string]string{"Dockerfile": "FROM x\nCMD [\"run\"]\n"}, "dockerfile", 8080},
	})
}

// TestDetectPortDockerfileWinsOverFramework covers a framework repo that also
// ships a Dockerfile: the Dockerfile is what actually builds, so its EXPOSE wins
// over the framework default guess.
func TestDetectPortDockerfileWinsOverFramework(t *testing.T) {
	runPortCases(t, []portCase{
		{"next-plus-dockerfile-8000", map[string]string{
			"package.json": pkg(`"next":"14"`, ""),
			"Dockerfile":   "FROM node\nEXPOSE 8000\n",
		}, "nextjs", 8000},
		{"vite-plus-dockerfile-80", map[string]string{
			"package.json":   pkg(`"vite":"5"`, ""),
			"vite.config.ts": "export default {}",
			"Dockerfile":     "FROM nginx\nEXPOSE 80\n",
		}, "vite", 80},
		{"fastapi-plus-dockerfile-9000", map[string]string{
			"requirements.txt": "fastapi\nuvicorn\n",
			"Dockerfile":       "FROM python\nEXPOSE 9000\n",
		}, "fastapi", 9000},
		{"express-plus-dockerfile-4321", map[string]string{
			"package.json": pkg(`"express":"4"`, ""),
			"Dockerfile":   "FROM node\nEXPOSE 4321\n",
		}, "express", 4321},
		{"go-plus-dockerfile-2112", map[string]string{
			"go.mod":     "module x\ngo 1.22\n",
			"Dockerfile": "FROM golang\nEXPOSE 2112\n",
		}, "go", 2112},
	})
}

// TestDetectSurvivesFlakySubtree reproduces the "detects nothing" symptom: a
// repo with a root Dockerfile plus several subdirectories where one
// subdirectory listing fails (transient 5xx / forbidden). Detection must keep
// the root candidates instead of aborting the whole scan and returning empty.
func TestDetectSurvivesFlakySubtree(t *testing.T) {
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	fake := fakeGitHubContents{
		owner: "org", repo: "app",
		files: map[string]string{
			"README.md":            "# monorepo",
			"broken/keep.txt":      "x",
			"svc/requirements.txt": "fastapi\nuvicorn\n",
			"svc/main.py":          "from fastapi import FastAPI",
		},
		errDirs: map[string]bool{"broken": true},
	}
	githubHTTPClient = &http.Client{Transport: fake.roundTrip(t)}
	det, err := detectWithToken(context.Background(), "tok", "org/app", "")
	if err != nil {
		t.Fatalf("detectWithToken aborted on a flaky subtree: %v", err)
	}
	if det.Framework == nil {
		t.Fatal("framework = nil; flaky subdirectory aborted detection instead of skipping only that subtree")
	}
	if *det.Framework != "fastapi" {
		t.Fatalf("framework = %q, want fastapi (from the healthy subtree)", *det.Framework)
	}
}

// TestDetectEarlyReturnPrunesSubtree locks the performance contract: once the
// root directory yields a candidate, the scan stops instead of walking every
// subtree (which was making detection exceed the wizard's request timeout). A
// subtree is only scanned when the root has no candidate (monorepo fallback).
func TestDetectEarlyReturnPrunesSubtree(t *testing.T) {
	t.Run("root-candidate-wins-no-recursion", func(t *testing.T) {
		det := detectFakeRepo(t, map[string]string{
			"Dockerfile":            "FROM python\nEXPOSE 8501\n",
			"frontend/package.json": pkg(`"next":"14"`, ""),
		})
		if det.Framework == nil || *det.Framework != "dockerfile" {
			t.Fatalf("framework = %v, want dockerfile (root candidate must short-circuit recursion)", det.Framework)
		}
	})
	t.Run("monorepo-fallback-recurses", func(t *testing.T) {
		det := detectFakeRepo(t, map[string]string{
			"README.md":                 "# monorepo",
			"packages/web/package.json": pkg(`"next":"14"`, ""),
		})
		if det.Framework == nil || *det.Framework != "nextjs" {
			t.Fatalf("framework = %v, want nextjs (barren root must recurse into packages)", det.Framework)
		}
	})
}

func TestDetectPortNextJS(t *testing.T) {
	runPortCases(t, []portCase{
		{"npm-lock", map[string]string{"package.json": pkg(`"next":"14"`, ""), "package-lock.json": "{}"}, "nextjs", 3000},
		{"pnpm-lock", map[string]string{"package.json": pkg(`"next":"14"`, ""), "pnpm-lock.yaml": ""}, "nextjs", 3000},
		{"yarn-lock", map[string]string{"package.json": pkg(`"next":"14"`, ""), "yarn.lock": ""}, "nextjs", 3000},
		{"bun-lock", map[string]string{"package.json": pkg(`"next":"14"`, ""), "bun.lockb": ""}, "nextjs", 3000},
		{"config-only", map[string]string{"next.config.js": "module.exports={}", "package.json": "{}"}, "nextjs", 3000},
	})
}

func TestDetectPortViteAndReact(t *testing.T) {
	runPortCases(t, []portCase{
		{"vite-config-ts", map[string]string{"package.json": pkg(`"vite":"5"`, ""), "vite.config.ts": "export default {}"}, "vite", 4173},
		{"vite-config-js", map[string]string{"package.json": pkg(`"vite":"5"`, ""), "vite.config.js": "export default {}"}, "vite", 4173},
		{"vite-config-only", map[string]string{"vite.config.ts": "export default {}", "package.json": "{}"}, "vite", 4173},
		{"react-dom", map[string]string{"package.json": pkg(`"react":"18","react-dom":"18"`, "")}, "react", 3000},
		{"react-plugin", map[string]string{"package.json": pkg(`"react":"18","@vitejs/plugin-react":"4"`, "")}, "react", 3000},
	})
}

// TestDetectPortViteStartAlignment locks the contract that the reported port
// matches the command the start actually launches: a Vite app served via
// `vite preview` binds 4173, not the 3000 that react/sveltekit default to.
// The effective start body is read from the real start/preview script when
// present, else from the synthesized fallback command.
func TestDetectPortViteStartAlignment(t *testing.T) {
	runPortCases(t, []portCase{
		{"react-no-scripts-fallback-preview", map[string]string{"package.json": pkgWith(`"react":"18","react-dom":"18"`, "")}, "react", 4173},
		{"sveltekit-no-scripts-fallback-preview", map[string]string{"package.json": pkgWith(`"@sveltejs/kit":"2"`, "")}, "sveltekit", 4173},
		{"react-preview-script-vite", map[string]string{"package.json": pkgWith(`"react":"18","react-dom":"18"`, `"build":"vite build","preview":"vite preview"`)}, "react", 4173},
		{"react-start-script-node-ssr", map[string]string{"package.json": pkgWith(`"react":"18","react-dom":"18"`, `"build":"vite build","start":"node server.js"`)}, "react", 3000},
		{"vite-preview-script-idempotent", map[string]string{"package.json": pkgWith(`"vite":"5"`, `"preview":"vite preview"`), "vite.config.ts": "export default {}"}, "vite", 4173},
	})
}

func TestDetectPortNuxtSvelte(t *testing.T) {
	runPortCases(t, []portCase{
		{"nuxt-dep", map[string]string{"package.json": pkg(`"nuxt":"3"`, "")}, "nuxt", 3000},
		{"nuxt-config", map[string]string{"nuxt.config.ts": "export default {}", "package.json": "{}"}, "nuxt", 3000},
		{"svelte-dep", map[string]string{"package.json": pkg(`"@sveltejs/kit":"2"`, "")}, "sveltekit", 3000},
		{"svelte-config", map[string]string{"svelte.config.js": "export default {}", "package.json": "{}"}, "sveltekit", 4173},
		{"nuxt-devtools", map[string]string{"package.json": pkg(`"@nuxt/devtools":"1"`, "")}, "nuxt", 3000},
	})
}

func TestDetectPortNodeBackends(t *testing.T) {
	runPortCases(t, []portCase{
		{"nestjs", map[string]string{"package.json": pkg(`"@nestjs/core":"10"`, "")}, "nestjs", 3000},
		{"fastify", map[string]string{"package.json": pkg(`"fastify":"4"`, "")}, "fastify", 3000},
		{"express", map[string]string{"package.json": pkg(`"express":"4"`, "")}, "express", 3000},
		{"remix", map[string]string{"package.json": pkg(`"@remix-run/node":"2"`, "")}, "remix", 3000},
		{"nestjs-fastify-platform", map[string]string{"package.json": pkg(`"@nestjs/platform-fastify":"10"`, "")}, "nestjs", 3000},
	})
}

func TestDetectPortPython(t *testing.T) {
	runPortCases(t, []portCase{
		{"fastapi-requirements", map[string]string{"requirements.txt": "fastapi\nuvicorn\n"}, "fastapi", 8000},
		{"django-requirements", map[string]string{"requirements.txt": "django>=5\n"}, "django", 8000},
		{"flask-requirements", map[string]string{"requirements.txt": "flask==3\n"}, "flask", 5000},
		{"fastapi-pyproject", map[string]string{"pyproject.toml": "[project]\ndependencies=[\"fastapi\"]\n"}, "fastapi", 8000},
		{"flask-setup-py", map[string]string{"setup.py": "install_requires=['flask']"}, "flask", 5000},
	})
}

func TestDetectPortJVM(t *testing.T) {
	runPortCases(t, []portCase{
		{"spring-gradle", map[string]string{"build.gradle": "id 'org.springframework.boot' version '3.4.0'"}, "spring-gradle", 8080},
		{"spring-maven", map[string]string{"pom.xml": "<artifactId>spring-boot-starter-web</artifactId>"}, "spring-maven", 8080},
		{"gradle-kts-spring", map[string]string{"build.gradle.kts": "id(\"org.springframework.boot\") version \"3.4.0\""}, "spring-gradle", 8080},
		{"plain-gradle", map[string]string{"build.gradle": "apply plugin: 'java'"}, "gradle", 8080},
		{"plain-maven", map[string]string{"pom.xml": "<project><modelVersion>4.0.0</modelVersion></project>"}, "maven", 8080},
	})
}

// TestDetectPortSpringServerPort proves the detector reads the real Spring
// server.port instead of the 8080 default guess.
func TestDetectPortSpringServerPort(t *testing.T) {
	spring := "id 'org.springframework.boot' version '3.4.0'"
	runPortCases(t, []portCase{
		{"props-root", map[string]string{"build.gradle": spring, "application.properties": "server.port=8081\n"}, "spring-gradle", 8081},
		{"props-resources", map[string]string{"build.gradle": spring, "src/main/resources/application.properties": "spring.application.name=x\nserver.port=8082\n"}, "spring-gradle", 8082},
		{"yaml-flattened", map[string]string{"build.gradle": spring, "application.yml": "server.port: 7000\n"}, "spring-gradle", 7000},
		{"yaml-nested-resources", map[string]string{"build.gradle": spring, "src/main/resources/application.yaml": "server:\n  port: 9090\n  servlet:\n    context-path: /api\n"}, "spring-gradle", 9090},
		{"maven-no-config-default", map[string]string{"pom.xml": "<artifactId>spring-boot-starter-web</artifactId>"}, "spring-maven", 8080},
	})
}

// TestDetectPortPythonSource proves the detector reads the real uvicorn/flask
// bind port from the entrypoint module instead of the 8000/5000 default.
func TestDetectPortPythonSource(t *testing.T) {
	runPortCases(t, []portCase{
		{"uvicorn-run-kwarg", map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "import uvicorn\nuvicorn.run(\"main:app\", host=\"0.0.0.0\", port=8001)\n"}, "fastapi", 8001},
		{"flask-app-run", map[string]string{"requirements.txt": "flask\n", "app.py": "app.run(host=\"0.0.0.0\", port=5001)\n"}, "flask", 5001},
		{"uvicorn-cli-flag", map[string]string{"requirements.txt": "fastapi\n", "run.py": "import os\nos.system(\"uvicorn app:app --port 8002\")\n"}, "fastapi", 8002},
		{"gunicorn-bind", map[string]string{"requirements.txt": "fastapi\n", "server.py": "BIND = \"gunicorn -b 0.0.0.0:8003 app:app\"\n"}, "fastapi", 8003},
		{"fastapi-no-port-default", map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"}, "fastapi", 8000},
	})
}

// TestDetectPortDotEnv proves a root .env PORT overrides the Node framework
// default guess (and its vite-preview alignment).
func TestDetectPortDotEnv(t *testing.T) {
	runPortCases(t, []portCase{
		{"express-env", map[string]string{"package.json": pkg(`"express":"4"`, ""), ".env": "PORT=4001\n"}, "express", 4001},
		{"nextjs-env", map[string]string{"package.json": pkg(`"next":"14"`, ""), ".env": "NODE_ENV=production\nPORT=4002\n"}, "nextjs", 4002},
		{"vite-env-over-4173", map[string]string{"package.json": pkgWith(`"vite":"5"`, ""), "vite.config.ts": "export default {}", ".env": "PORT=4003\n"}, "vite", 4003},
		{"react-env-export", map[string]string{"package.json": pkg(`"react":"18","react-dom":"18"`, ""), ".env": "export PORT=4004\n"}, "react", 4004},
		{"express-no-env-default", map[string]string{"package.json": pkg(`"express":"4"`, "")}, "express", 3000},
	})
}

func TestDetectPortGo(t *testing.T) {
	runPortCases(t, []portCase{
		{"go-mod", map[string]string{"go.mod": "module x\ngo 1.22\n"}, "go", 8080},
		{"go-with-main", map[string]string{"go.mod": "module x\n", "main.go": "package main"}, "go", 8080},
		{"go-with-sum", map[string]string{"go.mod": "module x\n", "go.sum": "h1:..."}, "go", 8080},
		{"go-nested-cmd", map[string]string{"go.mod": "module x\n", "cmd/app/main.go": "package main"}, "go", 8080},
		{"go-dockerfile-static-port", map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM scratch\nEXPOSE 8080\n"}, "go", 8080},
	})
}

// TestDetectPortStatic covers plain static sites: a repo with index.html and no
// higher-priority framework marker resolves to framework "static" on port 80.
// The static detector is the lowest-priority candidate, so any real framework
// or a Dockerfile still wins (asserted by the last two cases).
func TestDetectPortStatic(t *testing.T) {
	runPortCases(t, []portCase{
		{"index-only", map[string]string{"index.html": "<html></html>"}, "static", 80},
		{"index-with-css", map[string]string{"index.html": "<html></html>", "style.css": "body{}"}, "static", 80},
		{"index-with-js-assets", map[string]string{"index.html": "<html></html>", "app.js": "//", "assets/logo.png": "x"}, "static", 80},
		{"index-plus-dockerfile-loses", map[string]string{"index.html": "<html></html>", "Dockerfile": "FROM nginx\nEXPOSE 80\n"}, "dockerfile", 80},
		{"index-plus-vite-loses", map[string]string{"index.html": "<html></html>", "package.json": pkg(`"vite":"5"`, ""), "vite.config.ts": "export default {}"}, "vite", 4173},
	})
}
