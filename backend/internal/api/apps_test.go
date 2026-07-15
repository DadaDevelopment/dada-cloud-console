package api_test

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/api"
	"github.com/dada-tuda/console/backend/internal/models"
)

func TestValidateImage(t *testing.T) {
	good := []string{
		"ghcr.io/dada-tuda/codex-lb:1.14.2",
		"registry.dada-tuda.ru/app:latest",
		"nginx:1.25",
		"my-app:v2.3.1-rc1",
		"ghcr.io/MyOrg/my-app:v1.0",          // uppercase org (GitHub Container Registry)
		"registry.example.com:5000/app:v1.0", // registry with port
		"MYAPP:latest",                       // uppercase image name
	}
	bad := []string{
		"",
		"no-tag",
		"has space:v1",
		"image with spaces:v1",
	}
	for _, img := range good {
		if err := api.ValidateImage(img); err != nil {
			t.Errorf("expected %q to be valid, got: %v", img, err)
		}
	}
	for _, img := range bad {
		if err := api.ValidateImage(img); err == nil {
			t.Errorf("expected %q to be invalid", img)
		}
	}
}

func TestFillRepoFullName(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "deployed-no-repo", SummaryJSON: json.RawMessage(`{"port":8501,"status":"Ready"}`)},
		{Name: "already-has-repo", SummaryJSON: json.RawMessage(`{"repo_full_name":"keep/me"}`)},
		{Name: "no-git-link", SummaryJSON: json.RawMessage(`{"port":80}`)},
		{Name: "empty-summary", SummaryJSON: nil},
	}
	repoByName := map[string]string{
		"deployed-no-repo": "keksmd/AIOS-Hackaton",
		"already-has-repo": "other/repo",
		"empty-summary":    "acme/svc",
	}
	api.FillRepoFullName(apps, repoByName)

	get := func(raw json.RawMessage) string {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s, _ := m["repo_full_name"].(string)
		return s
	}
	if got := get(apps[0].SummaryJSON); got != "keksmd/AIOS-Hackaton" {
		t.Errorf("deployed-no-repo: repo_full_name = %q, want keksmd/AIOS-Hackaton", got)
	}
	if got := get(apps[1].SummaryJSON); got != "keep/me" {
		t.Errorf("already-has-repo: overwrote existing repo -> %q, want keep/me", got)
	}
	if got := get(apps[2].SummaryJSON); got != "" {
		t.Errorf("no-git-link: injected repo %q, want empty", got)
	}
	if got := get(apps[3].SummaryJSON); got != "acme/svc" {
		t.Errorf("empty-summary: repo_full_name = %q, want acme/svc", got)
	}
}

func TestSuppressNonHTTPURL(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "top-decker-redis", SummaryJSON: json.RawMessage(`{"port":6379,"url":"https://myredis-c1e9e9.dada-tuda.ru","status":"Ready"}`)},
		{Name: "web-app", SummaryJSON: json.RawMessage(`{"port":8080,"url":"https://web-a1b2c3.dada-tuda.ru","status":"Ready"}`)},
		{Name: "no-port-known", SummaryJSON: json.RawMessage(`{"url":"https://legacy-app.dada-tuda.ru","status":"Ready"}`)},
		{Name: "no-url-yet", SummaryJSON: json.RawMessage(`{"port":6379,"status":"Provisioning"}`)},
		{Name: "empty-summary", SummaryJSON: nil},
	}
	api.SuppressNonHTTPURL(apps)

	get := func(raw json.RawMessage) (string, bool) {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s, ok := m["url"].(string)
		return s, ok
	}
	if _, ok := get(apps[0].SummaryJSON); ok {
		t.Errorf("top-decker-redis: url should be suppressed for datastore port 6379")
	}
	if got, ok := get(apps[1].SummaryJSON); !ok || got != "https://web-a1b2c3.dada-tuda.ru" {
		t.Errorf("web-app: url = %q, ok=%v, want unchanged HTTP url", got, ok)
	}
	if got, ok := get(apps[2].SummaryJSON); !ok || got != "https://legacy-app.dada-tuda.ru" {
		t.Errorf("no-port-known: url = %q, ok=%v, want unchanged (ambiguous port must not regress)", got, ok)
	}
	if _, ok := get(apps[3].SummaryJSON); ok {
		t.Errorf("no-url-yet: should still have no url key")
	}
	if apps[4].SummaryJSON != nil {
		t.Errorf("empty-summary: should stay untouched")
	}
}
