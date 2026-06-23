package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/github"
)

// fakeApp is a minimal github.App for endpoint tests.
type fakeApp struct {
	repos   []github.RemoteRepo
	gotID   int64
	listErr error
}

func (f *fakeApp) InstallToken(_ context.Context, _ int64) (string, error) { return "t", nil }
func (f *fakeApp) ListRepos(_ context.Context, id int64) ([]github.RemoteRepo, error) {
	f.gotID = id
	return f.repos, f.listErr
}
func (f *fakeApp) PostStatus(_ context.Context, _ int64, _, _, _, _, _ string) error { return nil }

func newTestServer(gh github.App) http.Handler {
	s := New(":0", &Options{GitHub: gh})
	mux := http.NewServeMux()
	if s.gh != nil {
		mux.HandleFunc("GET /github/installations/{id}/repos", s.handleInstallationRepos)
	}
	mux.HandleFunc("GET /github/installations/{id}/detect", s.handleDetect)
	return mux
}

func TestHandleInstallationRepos(t *testing.T) {
	fa := &fakeApp{repos: []github.RemoteRepo{
		{FullName: "org/app", CloneURL: "https://github.com/org/app.git", DefaultBranch: "main", Private: true},
	}}
	srv := httptest.NewServer(newTestServer(fa))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/github/installations/4242/repos")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Repos []github.RemoteRepo `json:"repos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if fa.gotID != 4242 {
		t.Errorf("installation id = %d, want 4242", fa.gotID)
	}
	if len(out.Repos) != 1 || out.Repos[0].FullName != "org/app" {
		t.Errorf("repos = %+v, want one org/app", out.Repos)
	}
}

func TestHandleInstallationReposBadID(t *testing.T) {
	srv := httptest.NewServer(newTestServer(&fakeApp{}))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/github/installations/not-a-number/repos")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleDetectNullBestEffort(t *testing.T) {
	srv := httptest.NewServer(newTestServer(&fakeApp{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/github/installations/1/detect?repo=org/app&root_dir=.")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var d frameworkDetection
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Framework != nil {
		t.Errorf("framework = %v, want null (best-effort)", *d.Framework)
	}
}
