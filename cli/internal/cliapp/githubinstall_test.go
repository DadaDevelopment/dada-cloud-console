package cliapp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/ui"
)

// gitAccessCalls counts what the CLI asked the console for while it tried to
// open a private repository.
type gitAccessCalls struct {
	connects   int
	installs   int
	authorizes int
	polls      int
}

// privateRepoServer answers like the console does for a private repository the
// platform cannot clone: link attempts fail with github_access_required until
// the attempt numbered successAt, which succeeds. That models both real cases -
// authorizing was enough (successAt 2), or an install was needed too
// (successAt 3).
func privateRepoServer(t *testing.T, calls *gitAccessCalls, successAt, visibleAfter int) *httptest.Server {
	t.Helper()
	realBrowser, realInteractive := openBrowser, interactive
	openBrowser = func(string) {}
	interactive = func(io.Reader) bool { return true }
	t.Cleanup(func() { openBrowser, interactive = realBrowser, realInteractive })

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/repos"):
			writeJSON(t, w, http.StatusOK, map[string]any{"repos": []map[string]any{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/installations/available"):
			calls.polls++
			if calls.polls < visibleAfter {
				writeJSON(t, w, http.StatusOK, map[string]any{"installations": []map[string]any{}})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"installations": []map[string]any{
				{"installation_id": "143550113", "account_login": "KeksMD", "account_type": "User"},
			}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/git/github/authorize"):
			calls.authorizes++
			writeJSON(t, w, http.StatusOK, map[string]any{
				"url": "https://github.com/login/oauth/authorize?client_id=x&state=y",
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/git/install-url"):
			calls.installs++
			writeJSON(t, w, http.StatusOK, map[string]any{
				"url": "https://github.com/apps/argocd-dada/installations/new?state=signed",
			})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/repos"):
			calls.connects++
			if calls.connects < successAt {
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

// TestAlreadyInstalledAppIsAuthorizedNotReinstalled is the regression lock for
// what a user hit on 2026-08-13: the CLI sent him to install a GitHub App his
// account already had. Installations are recorded per org, so his live
// installation was invisible to a project in another org - and an install was
// the one thing that could not fix it, because GitHub answers an
// already-installed account with a configure page and never calls the setup URL
// back. Authorization is the door that opens; it must be tried first, and it
// must end the detour when it works.
func TestAlreadyInstalledAppIsAuthorizedNotReinstalled(t *testing.T) {
	dir := githubRepoDir(t)
	var calls gitAccessCalls
	srv := privateRepoServer(t, &calls, 2, 1)
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	buildID, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, strings.NewReader(""), prog)
	prog.Stop()

	if !fromGit {
		t.Fatalf("deploy fell back to archive: %s", out.String())
	}
	if buildID != "build-1" {
		t.Fatalf("build id = %q, want build-1", buildID)
	}
	if calls.authorizes != 1 {
		t.Fatalf("authorize url fetched %d times, want 1", calls.authorizes)
	}
	if calls.installs != 0 {
		t.Fatalf("a user who only had to authorize was sent to install %d times", calls.installs)
	}
	if !strings.Contains(out.String(), "oauth/authorize") {
		t.Fatalf("the authorize url must be printed, deploy said: %s", out.String())
	}
}

// TestRepoWithoutAnyInstallationStillGetsTheInstallURL keeps the second door
// open: a user who has never installed the App gets nothing back from
// authorization, so the deploy must go on to offer the install URL rather than
// give up on the git path.
func TestRepoWithoutAnyInstallationStillGetsTheInstallURL(t *testing.T) {
	dir := githubRepoDir(t)
	var calls gitAccessCalls
	srv := privateRepoServer(t, &calls, 3, 1)
	defer srv.Close()

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	_, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, strings.NewReader(""), prog)
	prog.Stop()

	if !fromGit {
		t.Fatalf("deploy fell back to archive: %s", out.String())
	}
	if calls.installs != 1 {
		t.Fatalf("install url fetched %d times, want 1", calls.installs)
	}
	if calls.connects != 3 {
		t.Fatalf("link attempted %d times, want 3 (initial, after authorize, after install)", calls.connects)
	}
}

// TestNonInteractiveDeployDoesNotWaitForAnInstall keeps CI and scripted runs
// out of the detour: with nobody behind stdin to click anything, the deploy
// must not burn accessWaitTimeout waiting and must ship the archive at once.
func TestNonInteractiveDeployDoesNotWaitForAnInstall(t *testing.T) {
	dir := githubRepoDir(t)
	var calls gitAccessCalls
	srv := privateRepoServer(t, &calls, 2, 1)
	defer srv.Close()
	interactive = func(io.Reader) bool { return false }

	client := apiclient.New(srv.URL, srv.Client(), nil, "")
	var out bytes.Buffer
	prog := ui.New(&out)
	_, fromGit := deployFromGit(context.Background(), client, "proj", "env", "genagent",
		DeployOptions{Dir: dir}, strings.NewReader(""), prog)
	prog.Stop()

	if fromGit {
		t.Fatal("an unconfirmed authorization must not be treated as connected")
	}
	if calls.authorizes != 0 {
		t.Fatalf("a scripted run must not be sent to authorize (%d times)", calls.authorizes)
	}
	if calls.connects != 1 {
		t.Fatalf("link attempted %d times, want 1", calls.connects)
	}
	if calls.installs != 0 {
		t.Fatalf("a scripted run must not be sent to install (%d times)", calls.installs)
	}
}
