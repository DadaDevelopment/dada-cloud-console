package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
)

// seedLivenessActiveHostname records a domain_hostnames row whose route
// health is already "active" -- the only state reconcile() will ever pick a
// candidate for a liveness probe, since a pending/failed route is not yet
// something a live-traffic HTTP check should judge.
func seedLivenessActiveHostname(t *testing.T, ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, appName, hostname string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status)
		 VALUES ($1, $2, $3, 'CNAME', 'active')`,
		envID, appName, hostname); err != nil {
		t.Fatalf("seed domain_hostnames (active): %v", err)
	}
}

// readLivenessFields reads back summary_json.http_status / http_reason /
// http_checked_at for the given App snapshot. present is false when none of
// the three keys exist in the JSON at all, which is how the "feature
// disabled" case is distinguished from "probed and came back empty".
func readLivenessFields(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string) (status int, reason string, checkedAt string, present bool) {
	t.Helper()
	var raw []byte
	row := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName)
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("read summary_json: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal summary_json: %v", err)
	}
	if _, ok := m["http_status"]; !ok {
		return 0, "", "", false
	}
	var parsed struct {
		HTTPStatus    int    `json:"http_status"`
		HTTPReason    string `json:"http_reason"`
		HTTPCheckedAt string `json:"http_checked_at"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal liveness fields: %v", err)
	}
	return parsed.HTTPStatus, parsed.HTTPReason, parsed.HTTPCheckedAt, true
}

// TestReconcile_LivenessProbe_HealthyApp exercises the happy path end to
// end: an App with an active primary hostname gets an in-cluster HTTP probe,
// and a 200 response lands as http_status=200 with an empty http_reason (the
// contract: reason is only ever set for a non-2xx/3xx outcome).
func TestReconcile_LivenessProbe_HealthyApp(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-ok", "prod", "liveness-ok-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "profi.dada-tuda.ru" {
			t.Errorf("probe Host header = %q, want profi.dada-tuda.ru", req.Host)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-ok-prod"))
	r := &StatusReconciler{
		pool:   pool,
		cfg:    &config.Config{LivenessProbeURL: upstream.URL, LivenessProbeMinInterval: 5 * time.Minute},
		client: client,
	}
	r.reconcile(ctx)

	status, reason, checkedAt, present := readLivenessFields(t, ctx, pool, projectID, envID, "profi")
	if !present {
		t.Fatalf("http_status not written after reconcile")
	}
	if status != http.StatusOK {
		t.Fatalf("http_status = %d, want 200", status)
	}
	if reason != "" {
		t.Fatalf("http_reason = %q, want empty for a 200 response", reason)
	}
	if checkedAt == "" {
		t.Fatalf("http_checked_at not set")
	}
	if _, err := time.Parse(time.RFC3339, checkedAt); err != nil {
		t.Fatalf("http_checked_at = %q not RFC3339: %v", checkedAt, err)
	}
}

// TestReconcile_LivenessProbe_BrokenApp is the failure-mode falsification
// case this whole feature exists for: a build reporting success and a
// snapshot at phase=Ready can still be answering every visitor with a 502,
// and the reconciler must record that, not paper over it.
func TestReconcile_LivenessProbe_BrokenApp(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-bad", "prod", "liveness-bad-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-bad-prod"))
	r := &StatusReconciler{
		pool:   pool,
		cfg:    &config.Config{LivenessProbeURL: upstream.URL, LivenessProbeMinInterval: 5 * time.Minute},
		client: client,
	}
	r.reconcile(ctx)

	status, reason, _, present := readLivenessFields(t, ctx, pool, projectID, envID, "profi")
	if !present {
		t.Fatalf("http_status not written after reconcile")
	}
	if status != http.StatusBadGateway {
		t.Fatalf("http_status = %d, want 502", status)
	}
	if reason != "status_502" {
		t.Fatalf("http_reason = %q, want status_502", reason)
	}
}

// TestReconcile_LivenessProbe_RateLimited proves the required floor: a
// second reconcile inside LivenessProbeMinInterval must not fire another
// HTTP request at the app, since the reconciler ticks far more often than
// any operator wants a real user app hit.
func TestReconcile_LivenessProbe_RateLimited(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-rl", "prod", "liveness-rl-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-rl-prod"))
	r := &StatusReconciler{
		pool:   pool,
		cfg:    &config.Config{LivenessProbeURL: upstream.URL, LivenessProbeMinInterval: 5 * time.Minute},
		client: client,
	}

	r.reconcile(ctx)
	if hits != 1 {
		t.Fatalf("hits after first reconcile = %d, want 1", hits)
	}
	firstCheckedAt := mustLivenessCheckedAt(t, ctx, pool, projectID, envID, "profi")

	r.reconcile(ctx)
	if hits != 1 {
		t.Fatalf("hits after second reconcile (within window) = %d, want still 1 -- rate limit did not hold", hits)
	}
	secondCheckedAt := mustLivenessCheckedAt(t, ctx, pool, projectID, envID, "profi")
	if secondCheckedAt != firstCheckedAt {
		t.Fatalf("http_checked_at changed on a rate-limited tick: %q -> %q", firstCheckedAt, secondCheckedAt)
	}
}

func mustLivenessCheckedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string) string {
	t.Helper()
	_, _, checkedAt, present := readLivenessFields(t, ctx, pool, projectID, envID, appName)
	if !present {
		t.Fatalf("http_checked_at not written")
	}
	return checkedAt
}

// TestReconcile_LivenessProbe_FollowsRedirectToDeadApp is the end-to-end
// regression test for the prod incident this fix addresses: an ingress-style
// same-host redirect on the first hop must be chased through to the app's
// real terminal status, not recorded as the redirect itself. Before this
// fix every probed app landed here with http_status=308 and an empty
// reason -- read by the backend's live_urls aggregation as "healthy" -- no
// matter what the app behind the redirect actually answered.
func TestReconcile_LivenessProbe_FollowsRedirectToDeadApp(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-redirect", "prod", "liveness-redirect-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		if req.Host != "profi.dada-tuda.ru" {
			t.Errorf("probe Host header = %q, want profi.dada-tuda.ru", req.Host)
		}
		if req.URL.Path == "/" {
			w.Header().Set("Location", "/app")
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-redirect-prod"))
	r := &StatusReconciler{
		pool:   pool,
		cfg:    &config.Config{LivenessProbeURL: upstream.URL, LivenessProbeMinInterval: 5 * time.Minute},
		client: client,
	}
	r.reconcile(ctx)

	status, reason, _, present := readLivenessFields(t, ctx, pool, projectID, envID, "profi")
	if !present {
		t.Fatalf("http_status not written after reconcile")
	}
	if status != http.StatusBadGateway {
		t.Fatalf("http_status = %d, want 502 (the redirect must be chased through to the app's real answer)", status)
	}
	if reason != "status_502" {
		t.Fatalf("http_reason = %q, want status_502", reason)
	}
	if hits < 2 {
		t.Fatalf("upstream hit %d times, want at least 2 (initial request + at least one followed redirect)", hits)
	}
}

// TestReconcile_LivenessProbe_OffHostRedirectStaysUnfollowed proves the
// probe records an off-host redirect as-is (a 3xx it never chased) rather
// than crashing or silently retargeting at whatever host the redirect names.
func TestReconcile_LivenessProbe_OffHostRedirectStaysUnfollowed(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-offhost", "prod", "liveness-offhost-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		w.Header().Set("Location", "https://not-the-probed-app.example.com/")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-offhost-prod"))
	r := &StatusReconciler{
		pool:   pool,
		cfg:    &config.Config{LivenessProbeURL: upstream.URL, LivenessProbeMinInterval: 5 * time.Minute},
		client: client,
	}
	r.reconcile(ctx)

	status, reason, _, present := readLivenessFields(t, ctx, pool, projectID, envID, "profi")
	if !present {
		t.Fatalf("http_status not written after reconcile")
	}
	if status != http.StatusFound {
		t.Fatalf("http_status = %d, want 302 (recorded as-is, never followed)", status)
	}
	if reason != "" {
		t.Fatalf("http_reason = %q, want empty for an unfollowed 3xx", reason)
	}
	if hits != 1 {
		t.Fatalf("upstream hit %d times, want exactly 1 -- an off-host redirect must never be chased", hits)
	}
}

// TestReconcile_LivenessProbe_DisabledWithoutURL confirms the off switch: no
// LivenessProbeURL means the reconciler never sends a probe and never writes
// the http_* fields at all, even for an app with an active hostname.
func TestReconcile_LivenessProbe_DisabledWithoutURL(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "liveness-off", "prod", "liveness-off-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, "profi", "Unknown")
	seedLivenessActiveHostname(t, ctx, pool, envID, "profi", "profi.dada-tuda.ru")

	client := fake.NewSimpleClientset(infraDeployment("profi-deploy", "liveness-off-prod"))
	r := &StatusReconciler{pool: pool, cfg: &config.Config{}, client: client}
	r.reconcile(ctx)

	_, _, _, present := readLivenessFields(t, ctx, pool, projectID, envID, "profi")
	if present {
		t.Fatalf("http_status written with LivenessProbeURL unset -- the feature must stay off with no env configured")
	}
}
