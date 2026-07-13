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
	owner string
	repo  string
	files map[string]string
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

func TestDetectPortNuxtSvelte(t *testing.T) {
	runPortCases(t, []portCase{
		{"nuxt-dep", map[string]string{"package.json": pkg(`"nuxt":"3"`, "")}, "nuxt", 3000},
		{"nuxt-config", map[string]string{"nuxt.config.ts": "export default {}", "package.json": "{}"}, "nuxt", 3000},
		{"svelte-dep", map[string]string{"package.json": pkg(`"@sveltejs/kit":"2"`, "")}, "sveltekit", 3000},
		{"svelte-config", map[string]string{"svelte.config.js": "export default {}", "package.json": "{}"}, "sveltekit", 3000},
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

func TestDetectPortGo(t *testing.T) {
	runPortCases(t, []portCase{
		{"go-mod", map[string]string{"go.mod": "module x\ngo 1.22\n"}, "go", 8080},
		{"go-with-main", map[string]string{"go.mod": "module x\n", "main.go": "package main"}, "go", 8080},
		{"go-with-sum", map[string]string{"go.mod": "module x\n", "go.sum": "h1:..."}, "go", 8080},
		{"go-nested-cmd", map[string]string{"go.mod": "module x\n", "cmd/app/main.go": "package main"}, "go", 8080},
		{"go-dockerfile-static-port", map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM scratch\nEXPOSE 8080\n"}, "go", 8080},
	})
}

// TestDetectStaticSiteGap documents a real gap: a plain static site (index.html
// with no framework marker) is NOT detected at all, even though
// frameworkDefaultPort carries a "static" -> 80 entry that nothing ever emits.
// Such a repo returns an empty detection and the wizard falls back to manual
// selection. Update this test if a static detector is added.
func TestDetectStaticSiteGap(t *testing.T) {
	for _, files := range []map[string]string{
		{"index.html": "<html></html>"},
		{"index.html": "<html></html>", "style.css": "body{}"},
	} {
		det := detectFakeRepo(t, files)
		if det.Framework != nil {
			t.Fatalf("static site now detected as %q -- update this gap test and frameworkDefaultPort coverage", *det.Framework)
		}
	}
}
