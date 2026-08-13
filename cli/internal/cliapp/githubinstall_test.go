package cliapp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/ui"
)

// privateRepoServer answers like the console does for a private repository the
// platform cannot clone: the first link attempt is rejected with
// github_access_required, and the second one - the one after the user has
// installed the GitHub App - succeeds. installs counts how many times the CLI
// asked for the install URL.
func privateRepoServer(t *testing.T, connects *int, installs *int) *httptest.Server {
	t.Helper()
	real := openBrowser
	openBrowser = func(string) {}
	t.Cleanup(func() { openBrowser = real })
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/repos"):
			writeJSON(t, w, http.StatusOK, map[string]any{"repos": []map[string]any{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/git/install-url"):
			*installs++
			if got := r.URL.Query().Get("provider"); got != "github" {
				t.Errorf("install-url provider = %q, want github", got)
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"url": "https://github.com/apps/dada-cloud/installations/new?state=signed",
			})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/repos"):
			*connects++
			if *connects == 1 {
				writeJSON(t, w, http.StatusBadRequest, map[string]any{
					"code":  "github_access_required",
					"error": "This repository is private or unavailable.",
				})
				return
			}
			writeJSON(t, w, http.StatusCreated, map[string]any{"repos": []map[string]any{{
				"id": "repo-2", "app_name": "genagent", "provider": "github",
				"repo_full_name": "keksmd/genagent", "production_branch": "main",
				"auto_deploy": true,
			}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/builds"):
			writeJSON(t, w, http.StatusCreated, map[string]any{"build": map[string]any{"id": "build-1"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestPrivateRepoIsConnectedAfterInstallingTheApp locks the detour that keeps a
// private repository on the git path. Before it, github_access_required ended
// the git path for good and every deploy of a private repo silently became an
// archive upload - no commit, no auto-deploy hook, no way back.
func TestPrivateRepoIsConnectedAfterInstallingTheApp(t *testing.T) {
	dir := githubRepoDir(t)
	connects, installs := 0, 0
	srv := privateRepoServer(t, &connects, &installs)
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	buildID, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, strings.NewReader("\n"), prog)
	prog.Stop()

	if !fromGit {
		t.Fatalf("deploy fell back to archive: %s", out.String())
	}
	if buildID != "build-1" {
		t.Fatalf("build id = %q, want build-1", buildID)
	}
	if installs != 1 {
		t.Fatalf("install url fetched %d times, want 1", installs)
	}
	if connects != 2 {
		t.Fatalf("link attempted %d times, want 2 (before and after the install)", connects)
	}
	if !strings.Contains(out.String(), "installations/new") {
		t.Fatalf("the install url must be printed, deploy said: %s", out.String())
	}
}

// TestNonInteractiveDeployDoesNotWaitForAnInstall keeps CI and scripted runs
// out of the prompt: with nothing behind stdin the detour must end at once and
// the deploy must continue on the archive path instead of hanging.
func TestNonInteractiveDeployDoesNotWaitForAnInstall(t *testing.T) {
	dir := githubRepoDir(t)
	connects, installs := 0, 0
	srv := privateRepoServer(t, &connects, &installs)
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	_, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, strings.NewReader(""), prog)
	prog.Stop()

	if fromGit {
		t.Fatal("an unconfirmed install must not be treated as connected")
	}
	if connects != 1 {
		t.Fatalf("link attempted %d times, want 1", connects)
	}
}
