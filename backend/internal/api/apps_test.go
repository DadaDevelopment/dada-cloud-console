package api_test

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/api"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
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
		"ghcr.io/dada-tuda/app@sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90", // digest-pinned
	}
	bad := []string{
		"",
		"no-tag",
		"has space:v1",
		"image with spaces:v1",
		"ghcr.io/org/app@sha256:short", // digest too short
		"ghcr.io/org/app@sha256:",      // empty digest
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

func TestFillRepoFullNameAndSource(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "uploaded-app", SummaryJSON: json.RawMessage(`{"repo_full_name":"upload/uploaded-app"}`)},
		{Name: "git-app", SummaryJSON: json.RawMessage(`{"repo_full_name":"acme/git-app"}`)},
		{Name: "already-set", SummaryJSON: json.RawMessage(`{"repo_full_name":"acme/x","source":"archive"}`)},
	}
	repoByName := map[string]string{
		"uploaded-app": "upload/uploaded-app",
		"git-app":      "acme/git-app",
		"already-set":  "acme/x",
	}
	sourceByName := map[string]string{
		"uploaded-app": "archive",
		"git-app":      "git",
		"already-set":  "git",
	}
	api.FillRepoFullNameAndSource(apps, repoByName, sourceByName)

	get := func(raw json.RawMessage) string {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s, _ := m["source"].(string)
		return s
	}
	if got := get(apps[0].SummaryJSON); got != "archive" {
		t.Errorf("uploaded-app: source = %q, want archive", got)
	}
	if got := get(apps[1].SummaryJSON); got != "git" {
		t.Errorf("git-app: source = %q, want git", got)
	}
	if got := get(apps[2].SummaryJSON); got != "archive" {
		t.Errorf("already-set: overwrote existing source -> %q, want archive kept", got)
	}
}

func TestSynthesizeGitRepoApps(t *testing.T) {
	projectID := uuid.New()
	envID := uuid.New()

	apps := []models.ResourceSnapshot{{Name: "deployed", Kind: "App", Phase: "Ready"}}
	seen := map[string]struct{}{"deployed": {}}

	rows := []api.GitRepoRow{
		{ID: uuid.New(), Name: "deployed", Repo: "acme/deployed", Provider: "github", Profile: "small", Replicas: 1, Port: 8080, LatestStatus: "success"},
		{ID: uuid.New(), Name: "linked-no-build", Repo: "acme/linked", Provider: "github", Profile: "small", Replicas: 1, Port: 3000, LatestStatus: ""},
		{ID: uuid.New(), Name: "building", Repo: "acme/building", Provider: "github", Profile: "small", Replicas: 1, Port: 3000, LatestStatus: "building"},
		{ID: uuid.New(), Name: "failed-first", Repo: "acme/failed", Provider: "github", Profile: "small", Replicas: 1, Port: 3000, LatestStatus: "failed"},
		{ID: uuid.New(), Name: "canceled-ghost", Repo: "acme/canceled", Provider: "github", Profile: "small", Replicas: 1, Port: 3000, LatestStatus: "canceled"},
		{ID: uuid.New(), Name: "uploaded", Repo: "upload/uploaded", Provider: "archive", Profile: "small", Replicas: 1, Port: 3000, LatestStatus: "success"},
	}

	out, repoByName, sourceByName := api.SynthesizeGitRepoApps(apps, rows, seen, projectID, envID)

	names := map[string]models.ResourceSnapshot{}
	for _, a := range out {
		names[a.Name] = a
	}

	if _, ok := names["canceled-ghost"]; ok {
		t.Errorf("canceled first deploy must leave no visible app; canceled-ghost placeholder was synthesized")
	}
	for _, want := range []string{"linked-no-build", "building", "failed-first"} {
		a, ok := names[want]
		if !ok {
			t.Errorf("%s: expected a NotDeployed placeholder (only canceled must be hidden)", want)
			continue
		}
		if a.Phase != "NotDeployed" {
			t.Errorf("%s: phase = %q, want NotDeployed", want, a.Phase)
		}
	}
	if got := countName(out, "deployed"); got != 1 {
		t.Errorf("deployed: appears %d times, want 1 (a live snapshot must not be duplicated by synth)", got)
	}
	if repoByName["canceled-ghost"] != "acme/canceled" {
		t.Errorf("repoByName[canceled-ghost] = %q, want acme/canceled; must be set even when the placeholder is skipped", repoByName["canceled-ghost"])
	}
	if repoByName["deployed"] != "acme/deployed" {
		t.Errorf("repoByName[deployed] = %q, want acme/deployed", repoByName["deployed"])
	}
	if sourceByName["uploaded"] != "archive" {
		t.Errorf("sourceByName[uploaded] = %q, want archive", sourceByName["uploaded"])
	}
	if sourceByName["deployed"] != "git" {
		t.Errorf("sourceByName[deployed] = %q, want git", sourceByName["deployed"])
	}
	uploaded, ok := names["uploaded"]
	if !ok {
		t.Fatalf("uploaded: expected a NotDeployed placeholder")
	}
	var uploadedSummary map[string]any
	_ = json.Unmarshal(uploaded.SummaryJSON, &uploadedSummary)
	if got, _ := uploadedSummary["source"].(string); got != "archive" {
		t.Errorf("uploaded placeholder summary source = %q, want archive", got)
	}
}

func countName(apps []models.ResourceSnapshot, name string) int {
	n := 0
	for _, a := range apps {
		if a.Name == name {
			n++
		}
	}
	return n
}

// TestApplyStartCommandUpdate covers the set/read/clear round trip of the
// start-command-arguments lever without needing a database: set writes the
// start_command key, read sees it verbatim (the same summary_json map ListApps
// hands back to the console), and clearing (empty string) deletes the key
// rather than leaving an empty string behind -- the shape a snapshot that
// never touched the field has, which is what keeps the gitops-agent renderer
// byte-identical for every app that never sets this.
func TestApplyStartCommandUpdate(t *testing.T) {
	cur := api.ApplyStartCommandUpdate(nil, "--config /etc/app.yaml --workers 4")
	if got := cur["start_command"]; got != "--config /etc/app.yaml --workers 4" {
		t.Fatalf("after set: start_command = %v, want the configured string", got)
	}

	cur = api.ApplyStartCommandUpdate(cur, "--config /etc/app.yaml --workers 8")
	if got := cur["start_command"]; got != "--config /etc/app.yaml --workers 8" {
		t.Fatalf("after re-set: start_command = %v, want the updated string", got)
	}

	cur = api.ApplyStartCommandUpdate(cur, "")
	if _, ok := cur["start_command"]; ok {
		t.Fatalf("after clear: start_command key still present, want deleted entirely")
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

// TestRestatePlaceholderPhase pins the false-green defect found by dogfooding
// the upload flow: the pause stand-in image starts instantly and probes are
// absent, so k8s (and the reconciler) call the app Ready while its real build
// is still running — or has already failed.
func TestRestatePlaceholderPhase(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "building", Phase: "Ready", SummaryJSON: json.RawMessage(`{"image":"registry.k8s.io/pause:3.9","port":8080,"url":"https://building-a1b2c3.dada-tuda.ru"}`)},
		{Name: "broken", Phase: "Ready", SummaryJSON: json.RawMessage(`{"image":"registry.k8s.io/pause:3.9","port":8080,"url":"https://broken-a1b2c3.dada-tuda.ru"}`)},
		{Name: "never-built", Phase: "Ready", SummaryJSON: json.RawMessage(`{"image":"registry.k8s.io/pause:3.9","port":8080}`)},
		{Name: "real", Phase: "Ready", SummaryJSON: json.RawMessage(`{"image":"nexus.dada-tuda.ru/org/real@sha256:abc","port":8080,"url":"https://real-a1b2c3.dada-tuda.ru"}`)},
		{Name: "empty-summary", Phase: "Ready", SummaryJSON: nil},
	}
	api.RestatePlaceholderPhase(apps, map[string]string{
		"building": "running",
		"broken":   "failed",
		"real":     "succeeded",
	})

	want := map[string]string{
		"building":      "Building",
		"broken":        "Failed",
		"never-built":   "NotDeployed",
		"real":          "Ready",
		"empty-summary": "Ready",
	}
	for _, a := range apps {
		if a.Phase != want[a.Name] {
			t.Errorf("%s: phase = %q, want %q", a.Name, a.Phase, want[a.Name])
		}
	}

	hasURL := func(raw json.RawMessage) bool {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		_, ok := m["url"]
		return ok
	}
	if hasURL(apps[0].SummaryJSON) {
		t.Error("building: url must be dropped — a pause container answers no HTTP request")
	}
	if !hasURL(apps[3].SummaryJSON) {
		t.Error("real: url must survive on an app running its real image")
	}
}

// TestRestateUnreachablePhaseDemotesOnlyReady pins the second false-green
// defect: the URL watcher had recorded 2057 consecutive failing probes against
// oxygen since 2026-08-04 while the console kept showing a green Ready badge,
// because "Ready" is replica readiness and says nothing about whether anything
// bound the port.
func TestRestateUnreachablePhaseDemotesOnlyReady(t *testing.T) {
	urlAlert := []models.AppAlert{{Type: "url", Reason: "no_listener", Detail: "dial tcp 10.96.163.146:8080: i/o timeout"}}
	apps := []models.ResourceSnapshot{
		{Name: "silent", Phase: "Ready", Alerts: urlAlert, SummaryJSON: json.RawMessage(`{"url":"https://silent-a1b2c3.dada-tuda.ru"}`)},
		{Name: "healthy", Phase: "Ready"},
		{Name: "crashing", Phase: "CrashLoop", Alerts: urlAlert},
		{Name: "stopped", Phase: "Stopped", Alerts: urlAlert},
		{Name: "disk", Phase: "Ready", Alerts: []models.AppAlert{{Type: "volume"}}},
	}
	api.RestateUnreachablePhase(apps)

	want := map[string]string{
		"silent":   "Unreachable",
		"healthy":  "Ready",
		"crashing": "CrashLoop",
		"stopped":  "Stopped",
		"disk":     "Ready",
	}
	for _, a := range apps {
		if a.Phase != want[a.Name] {
			t.Errorf("%s: phase = %q, want %q", a.Name, a.Phase, want[a.Name])
		}
	}

	var m map[string]any
	if err := json.Unmarshal(apps[0].SummaryJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["url"]; !ok {
		t.Error("silent: url must survive — a worker that binds no port still needs its address shown")
	}
}
