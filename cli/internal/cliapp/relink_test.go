package cliapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/ui"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// githubRepoDir builds a repository whose origin is a GitHub URL and whose
// branch already exists on that origin, without touching the network: the
// remote-tracking ref is written by hand, which is exactly what
// BranchOnOrigin reads first.
func githubRepoDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "t@example.com")
	gitRun(t, dir, "config", "user.name", "t")
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/keksmd/genagent.git")
	if err := os.WriteFile(filepath.Join(dir, "agent.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "first")
	gitRun(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

// TestUploadPlaceholderDoesNotLockTheAppOntoArchives is the regression lock
// for the deploy a real user hit on 2026-08-13: one fallback upload had left
// an "upload/genagent" link behind, and every later `ddc deploy` read that as
// "this app is already connected to another repo" and fell back to the archive
// path again — permanently, even though the code was on GitHub the whole time.
func TestUploadPlaceholderDoesNotLockTheAppOntoArchives(t *testing.T) {
	dir := githubRepoDir(t)

	var deleted, connected string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/repos"):
			writeJSON(t, w, http.StatusOK, map[string]any{"repos": []map[string]any{{
				"id": "repo-1", "app_name": "genagent", "provider": "archive",
				"repo_full_name": "upload/genagent", "production_branch": "upload",
			}}})
		case r.Method == "DELETE":
			deleted = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/repos"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			connected, _ = body["repo_full_name"].(string)
			writeJSON(t, w, http.StatusCreated, map[string]any{"repos": []map[string]any{{
				"id": "repo-2", "app_name": "genagent", "provider": "github",
				"platform_access": "anonymous", "repo_full_name": connected,
				"production_branch": "main", "auto_deploy": true,
			}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/builds"):
			writeJSON(t, w, http.StatusCreated, map[string]any{"build": map[string]any{"id": "build-1"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	buildID, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, prog)
	prog.Stop()

	if !fromGit {
		t.Fatalf("deploy fell back to archive: %s", out.String())
	}
	if buildID != "build-1" {
		t.Fatalf("build id = %q, want build-1", buildID)
	}
	if !strings.HasSuffix(deleted, "/repos/repo-1") {
		t.Fatalf("upload placeholder was not unlinked, delete path = %q", deleted)
	}
	if connected != "keksmd/genagent" {
		t.Fatalf("connected repo = %q, want keksmd/genagent", connected)
	}
}

// TestRealForeignLinkStillStopsTheGitPath keeps the guard that matters: an app
// deliberately linked to a different GitHub repo must not be silently
// re-pointed at whatever folder the user happens to stand in.
func TestRealForeignLinkStillStopsTheGitPath(t *testing.T) {
	dir := githubRepoDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/repos") {
			writeJSON(t, w, http.StatusOK, map[string]any{"repos": []map[string]any{{
				"id": "repo-1", "app_name": "genagent", "provider": "github",
				"repo_full_name": "someoneelse/other", "production_branch": "main",
			}}})
			return
		}
		t.Errorf("git path must stop before %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	_, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, prog)
	prog.Stop()

	if fromGit {
		t.Fatal("an app linked to another repo must not be re-pointed by deploy")
	}
	if !strings.Contains(out.String(), "someoneelse/other") {
		t.Fatalf("fallback must name the existing link: %s", out.String())
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatal(err)
	}
}
