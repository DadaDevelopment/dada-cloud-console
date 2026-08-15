package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestOverviewLiveURLsSeparatesEmitterOf5xx pins the second half of the
// "dead" boundary: the status code alone does not say WHO produced it.
// Measured on production 2026-08-15, all three rows below carried a 5xx and
// all three were counted as dead, while only one of them was broken:
//
//   - fonbet-value answered 503 from its own container (JSON body listing
//     its readiness blockers) -- the app is up and talking;
//   - fanvk is a worker with no HTTP surface at all, still carrying a
//     default domain granted before it became a worker; the 502 is
//     ingress-nginx's own error page for a route with no Service;
//   - n8n's hash-domain ingress points at a Service that no longer exists,
//     which is the only genuine last-mile break of the three.
//
// gitops-agent now records the emitter in http_reason (app_status_<code>
// when the body is the app's own, status_<code> when it is ingress-nginx's
// default page -- see classifyLivenessResponse), and the worker flag already
// rides in summary_json. Both poles are asserted in one test on purpose: a
// change that rescues the two false positives by widening "alive" must still
// leave the real outage red.
func TestOverviewLiveURLsSeparatesEmitterOf5xx(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-emitter-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	appAuthoredName := "fonbet-value-like-" + suffix
	workerName := "fanvk-like-" + suffix
	routeBrokenName := "n8n-like-" + suffix

	seedLiveURLApp(t, pool, projectID, envID, appAuthoredName, fmt.Sprintf(
		`{"url":"https://fonbet-value.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"app_status_503","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, projectID, envID, workerName, fmt.Sprintf(
		`{"url":"https://fanvk.example.dada-tuda.ru","url_status":"active","worker":true,"http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, projectID, envID, routeBrokenName, fmt.Sprintf(
		`{"url":"https://n8n-64b3d0.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"status_503","http_checked_at":"%s"}`,
		checkedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.AppResponded - before.AppResponded; got != 1 {
		t.Fatalf("AppResponded delta = %d, want 1: the app-authored 503 is the app talking, not a dead route", got)
	}
	if got := after.Workers - before.Workers; got != 1 {
		t.Fatalf("Workers delta = %d, want 1: a worker serves no HTTP, so its orphan domain's 502 is neither health nor death", got)
	}
	if got := after.Dead - before.Dead; got != 1 {
		t.Fatalf("Dead delta = %d, want 1: only the backend-less route is dead", got)
	}
	if got := after.Checked - before.Checked; got != 3 {
		t.Fatalf("Checked delta = %d, want 3: all three were probed", got)
	}

	byName := map[string]overviewDeadApp{}
	for _, a := range after.DeadApps {
		byName[a.Name] = a
	}
	if _, ok := byName[appAuthoredName]; ok {
		t.Fatalf("%s answered 503 itself and must NOT appear in dead_apps", appAuthoredName)
	}
	if _, ok := byName[workerName]; ok {
		t.Fatalf("%s is a worker with no HTTP surface and must NOT appear in dead_apps", workerName)
	}
	if _, ok := byName[routeBrokenName]; !ok {
		t.Fatalf("dead_apps must still name %s: its ingress has no backend", routeBrokenName)
	}
}
