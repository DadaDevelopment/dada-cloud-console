package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
)

// newTestPulseExporter builds a PulseExporter with no live pool and no live
// Handler dependency: buildOverview/collectCounters are swapped for the
// caller's stand-ins, so a tick never touches a database or a real network
// socket except the httptest.Server the caller points pulseGitHubBaseURL at.
func newTestPulseExporter(t *testing.T, cfg *config.Config) *PulseExporter {
	t.Helper()
	p := &PulseExporter{
		h:          nil,
		cfg:        cfg,
		httpClient: http.DefaultClient,
	}
	return p
}

func testPulseConfig() *config.Config {
	return &config.Config{
		PulseExportRepo:   "DadaDevelopment/argo-infra",
		PulseExportBranch: "pulse",
		PulseExportToken:  "test-token",
	}
}

type recordedPut struct {
	path string
	body githubContentsPutRequest
}

func newPulseTestServer(t *testing.T, onPut func(recordedPut)) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/DadaDevelopment/argo-infra/contents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/repos/DadaDevelopment/argo-infra/contents/")
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			var body githubContentsPutRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode PUT body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			onPut(recordedPut{path: path, body: body})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"content":{"sha":"deadbeef"}}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return httptest.NewServer(mux)
}

func withPulseGitHubBaseURL(t *testing.T, url string) {
	t.Helper()
	orig := pulseGitHubBaseURL
	pulseGitHubBaseURL = url
	t.Cleanup(func() { pulseGitHubBaseURL = orig })
}

func TestPulseExportTickPublishesLatestSnapshot(t *testing.T) {
	var puts []recordedPut
	srv := newPulseTestServer(t, func(rp recordedPut) {
		puts = append(puts, rp)
	})
	defer srv.Close()
	withPulseGitHubBaseURL(t, srv.URL)

	p := newTestPulseExporter(t, testPulseConfig())
	p.buildOverview = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"users": map[string]any{"total": 42}}, nil
	}
	p.collectCounters = func(ctx context.Context) (pulseCounters, []string) {
		n := 3
		return pulseCounters{NewUsers1h: &n}, nil
	}

	p.RunPulseExportTick(context.Background())

	if len(puts) != 2 {
		t.Fatalf("expected 2 PUTs (latest.json + one history file), got %d: %+v", len(puts), puts)
	}

	var sawLatest bool
	for _, rp := range puts {
		if rp.path != "pulse/latest.json" {
			continue
		}
		sawLatest = true
		if rp.body.Branch != "pulse" {
			t.Errorf("expected branch=pulse, got %q", rp.body.Branch)
		}
		raw, err := base64.StdEncoding.DecodeString(rp.body.Content)
		if err != nil {
			t.Fatalf("content is not valid base64: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("decoded content is not valid JSON: %v", err)
		}
		genAt, _ := parsed["generated_at"].(string)
		if genAt == "" {
			t.Errorf("expected non-empty generated_at, got %+v", parsed["generated_at"])
		}
		overview, ok := parsed["overview"]
		if !ok || overview == nil {
			t.Errorf("expected overview to be present on a successful tick, got %+v", parsed)
		}
	}
	if !sawLatest {
		t.Fatalf("no PUT hit pulse/latest.json: %+v", puts)
	}
}

// TestPulseExportOmitsOverviewOnCollectionFailure is the regression guard
// against the "panel blindness read as health" postmortem: when the
// snapshot's overview collection fails outright, the published JSON must
// carry the failure in errors and must NOT carry an "overview" key at all --
// a present-but-empty/zeroed overview would look identical to a genuinely
// healthy platform to anything reading pulse/latest.json downstream.
func TestPulseExportOmitsOverviewOnCollectionFailure(t *testing.T) {
	var puts []recordedPut
	srv := newPulseTestServer(t, func(rp recordedPut) {
		puts = append(puts, rp)
	})
	defer srv.Close()
	withPulseGitHubBaseURL(t, srv.URL)

	p := newTestPulseExporter(t, testPulseConfig())
	p.buildOverview = func(ctx context.Context) (map[string]any, error) {
		return nil, context.DeadlineExceeded
	}
	p.collectCounters = func(ctx context.Context) (pulseCounters, []string) {
		return pulseCounters{}, nil
	}

	p.RunPulseExportTick(context.Background())

	if len(puts) == 0 {
		t.Fatalf("expected at least one PUT even on a failed overview collection")
	}
	var raw []byte
	for _, rp := range puts {
		if rp.path == "pulse/latest.json" {
			decoded, err := base64.StdEncoding.DecodeString(rp.body.Content)
			if err != nil {
				t.Fatalf("content is not valid base64: %v", err)
			}
			raw = decoded
		}
	}
	if raw == nil {
		t.Fatalf("no PUT hit pulse/latest.json: %+v", puts)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decoded content is not valid JSON: %v", err)
	}
	if _, present := parsed["overview"]; present {
		t.Errorf("overview must be absent on a failed collection, got %+v", parsed["overview"])
	}
	errs, _ := parsed["errors"].([]any)
	if len(errs) == 0 {
		t.Errorf("expected non-empty errors on a failed collection, got %+v", parsed["errors"])
	}
}

// TestPulseExportTickSurvivesGitHubAuthFailure asserts a 401/403 from GitHub
// is logged and swallowed, never panics -- the caller is an unattended
// hourly ticker goroutine, and a bad or rotated token must not kill it.
func TestPulseExportTickSurvivesGitHubAuthFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/DadaDevelopment/argo-infra/contents/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	withPulseGitHubBaseURL(t, srv.URL)

	p := newTestPulseExporter(t, testPulseConfig())
	p.buildOverview = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"users": map[string]any{"total": 1}}, nil
	}
	p.collectCounters = func(ctx context.Context) (pulseCounters, []string) {
		return pulseCounters{}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunPulseExportTick panicked on a GitHub auth failure: %v", r)
		}
	}()
	p.RunPulseExportTick(context.Background())
}
