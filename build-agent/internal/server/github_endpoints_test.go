package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/github"
)

// fakeApp is a minimal github.App for endpoint tests.
type fakeApp struct {
	repos    []github.RemoteRepo
	branches []github.RemoteBranch
	gotID    int64
	listErr  error
	acct     *github.InstallationAccount
	acctErr  error
	insts    []github.InstallationAccount
}

func (f *fakeApp) InstallToken(_ context.Context, _ int64) (string, error) { return "t", nil }
func (f *fakeApp) ListRepos(_ context.Context, id int64) ([]github.RemoteRepo, error) {
	f.gotID = id
	return f.repos, f.listErr
}
func (f *fakeApp) GetInstallation(_ context.Context, id int64) (*github.InstallationAccount, error) {
	f.gotID = id
	return f.acct, f.acctErr
}
func (f *fakeApp) ListInstallations(_ context.Context) ([]github.InstallationAccount, error) {
	return f.insts, nil
}
func (f *fakeApp) ListBranches(_ context.Context, id int64, _ string) ([]github.RemoteBranch, error) {
	f.gotID = id
	return f.branches, f.listErr
}
func (f *fakeApp) PostStatus(_ context.Context, _ int64, _, _, _, _, _ string) error { return nil }
func (f *fakeApp) BranchHead(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

func newTestServer(gh github.App) http.Handler {
	s := New(":0", &Options{GitHub: gh})
	mux := http.NewServeMux()
	if s.gh != nil {
		mux.HandleFunc("GET /github/installations/{id}/repos", s.handleInstallationRepos)
		mux.HandleFunc("GET /github/installations/{id}/account", s.handleInstallationAccount)
		mux.HandleFunc("GET /github/app/installations", s.handleAppInstallations)
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
	resp, err := http.Get(srv.URL + "/github/installations/not-a-number/repos")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleInstallationAccount(t *testing.T) {
	fa := &fakeApp{acct: &github.InstallationAccount{
		InstallationID: 4242, AccountLogin: "acme", AccountType: "Organization",
	}}
	srv := httptest.NewServer(newTestServer(fa))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/github/installations/4242/account")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out github.InstallationAccount
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if fa.gotID != 4242 {
		t.Errorf("installation id = %d, want 4242", fa.gotID)
	}
	if out.AccountLogin != "acme" || out.AccountType != "Organization" {
		t.Errorf("account = %+v, want acme/Organization", out)
	}
}

func TestHandleAppInstallations(t *testing.T) {
	fa := &fakeApp{insts: []github.InstallationAccount{
		{InstallationID: 1, AccountLogin: "acme", AccountType: "Organization"},
		{InstallationID: 2, AccountLogin: "bob", AccountType: "User"},
	}}
	srv := httptest.NewServer(newTestServer(fa))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/github/app/installations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Installations []github.InstallationAccount `json:"installations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Installations) != 2 || out.Installations[0].AccountLogin != "acme" {
		t.Errorf("installations = %+v, want acme+bob", out.Installations)
	}
}

func TestHandleInstallationAccountBadID(t *testing.T) {
	srv := httptest.NewServer(newTestServer(&fakeApp{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/github/installations/nope/account")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestHandleDetectErrorReturns502 verifies a detection failure (here the repo
// tree cannot be read) surfaces as 502 instead of being masked as a 200 empty
// result, so the wizard can tell "scan failed" from "scanned, no framework".
func TestHandleDetectErrorReturns502(t *testing.T) {
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	githubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusNotFound, map[string]string{"error": "not found"}), nil
	})}

	srv := httptest.NewServer(newTestServer(&fakeApp{}))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/github/installations/1/detect?repo=org/app&root_dir=.")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestHandleDetectNextJS(t *testing.T) {
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	githubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/org/app/contents":
			return jsonResponse(t, http.StatusOK, []map[string]any{
				{"type": "file", "name": "package.json", "path": "package.json"},
			}), nil
		case "/repos/org/app/contents/package.json":
			raw := `{"packageManager":"pnpm@10.12.4","dependencies":{"next":"15.0.0"},"scripts":{"build":"next build","start":"next start"}}`
			return jsonResponse(t, http.StatusOK, map[string]any{
				"type": "file", "name": "package.json", "path": "package.json",
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw)),
			}), nil
		default:
			return jsonResponse(t, http.StatusNotFound, map[string]string{"error": "not found"}), nil
		}
	})}

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
	if d.Framework == nil || *d.Framework != "nextjs" {
		t.Fatalf("framework = %v, want nextjs", d.Framework)
	}
	if d.PackageManager == nil || *d.PackageManager != "pnpm@10.12.4" {
		t.Fatalf("package_manager = %v, want pnpm@10.12.4", d.PackageManager)
	}
	if d.BuildCommand == nil || *d.BuildCommand != "pnpm run build" {
		t.Fatalf("build_command = %v, want pnpm run build", d.BuildCommand)
	}
	if d.InstallCommand == nil || *d.InstallCommand != "pnpm install" {
		t.Fatalf("install_command = %v, want pnpm install", d.InstallCommand)
	}
	if d.StartCommand == nil || *d.StartCommand != "pnpm run start" {
		t.Fatalf("start_command = %v, want pnpm run start", d.StartCommand)
	}
	if d.Port == nil || *d.Port != 3000 {
		t.Fatalf("port = %v, want 3000", d.Port)
	}
}

func TestHandleDetectSpringGradle(t *testing.T) {
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	githubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/org/app/contents":
			return jsonResponse(t, http.StatusOK, []map[string]any{
				{"type": "file", "name": "build.gradle", "path": "build.gradle"},
			}), nil
		case "/repos/org/app/contents/build.gradle":
			raw := `plugins { id 'java'; id 'org.springframework.boot' version '3.4.0' }`
			return jsonResponse(t, http.StatusOK, map[string]any{
				"type": "file", "name": "build.gradle", "path": "build.gradle",
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw)),
			}), nil
		default:
			return jsonResponse(t, http.StatusNotFound, map[string]string{"error": "not found"}), nil
		}
	})}

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
	if d.Framework == nil || *d.Framework != "spring-gradle" {
		t.Fatalf("framework = %v, want spring-gradle", d.Framework)
	}
	if d.BuildCommand == nil || *d.BuildCommand != "./gradlew build" {
		t.Fatalf("build_command = %v, want ./gradlew build", d.BuildCommand)
	}
	if d.InstallCommand == nil || *d.InstallCommand != "./gradlew dependencies" {
		t.Fatalf("install_command = %v, want ./gradlew dependencies", d.InstallCommand)
	}
	if d.Port == nil || *d.Port != 8080 {
		t.Fatalf("port = %v, want 8080", d.Port)
	}
}

func TestHandleDetectNestedMonorepoPackageManager(t *testing.T) {
	old := githubHTTPClient
	t.Cleanup(func() { githubHTTPClient = old })
	githubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/org/app/contents":
			return jsonResponse(t, http.StatusOK, []map[string]any{
				{"type": "file", "name": "package.json", "path": "package.json"},
				{"type": "dir", "name": "frontend", "path": "frontend"},
			}), nil
		case "/repos/org/app/contents/package.json":
			raw := `{"packageManager":"pnpm@10.12.4","private":true,"workspaces":["frontend"]}`
			return jsonResponse(t, http.StatusOK, map[string]any{
				"type": "file", "name": "package.json", "path": "package.json",
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw)),
			}), nil
		case "/repos/org/app/contents/frontend":
			return jsonResponse(t, http.StatusOK, []map[string]any{
				{"type": "file", "name": "package.json", "path": "frontend/package.json"},
			}), nil
		case "/repos/org/app/contents/frontend/package.json":
			raw := `{"dependencies":{"next":"15.0.0"},"scripts":{"build":"next build","start":"next start"}}`
			return jsonResponse(t, http.StatusOK, map[string]any{
				"type": "file", "name": "package.json", "path": "frontend/package.json",
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw)),
			}), nil
		default:
			return jsonResponse(t, http.StatusNotFound, map[string]string{"error": "not found"}), nil
		}
	})}

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
	if d.Framework == nil || *d.Framework != "nextjs" {
		t.Fatalf("framework = %v, want nextjs", d.Framework)
	}
	if d.PackageManager == nil || *d.PackageManager != "pnpm@10.12.4" {
		t.Fatalf("package_manager = %v, want pnpm@10.12.4", d.PackageManager)
	}
	if d.BuildCommand == nil || *d.BuildCommand != "pnpm run build" {
		t.Fatalf("build_command = %v, want pnpm run build", d.BuildCommand)
	}
	if d.InstallCommand == nil || *d.InstallCommand != "pnpm install" {
		t.Fatalf("install_command = %v, want pnpm install", d.InstallCommand)
	}
	if d.StartCommand == nil || *d.StartCommand != "pnpm run start" {
		t.Fatalf("start_command = %v, want pnpm run start", d.StartCommand)
	}
	if d.Port == nil || *d.Port != 3000 {
		t.Fatalf("port = %v, want 3000", d.Port)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(t *testing.T, status int, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(buf)),
	}
}
