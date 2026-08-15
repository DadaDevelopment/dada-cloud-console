package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedLiveURLApp inserts an App snapshot with an active hostname and an
// arbitrary summary_json fragment (the probe fields under test), mirroring
// seedNoSignalApp's pattern of building the raw JSON string by hand so a test
// can state exactly what gitops-agent did or did not write.
func seedLiveURLApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, summaryJSON string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, first_seen_at, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4::jsonb, now(), now())`,
		projectID, envID, name, summaryJSON,
	)
	if err != nil {
		t.Fatalf("seed live-url app %s: %v", name, err)
	}
}

// seedLiveURLOwner creates a user and assigns it as the given project's owner,
// so a test can control whether overviewLiveURLs sees an internal or external
// owner email, following the same pattern as setProjectOwner
// (admin_collected_test.go) but with a real users row so owner_email joins.
func seedLiveURLOwner(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, username, email string) {
	t.Helper()
	userID := overviewBrokenSeedUser(t, pool, username, email)
	if _, err := pool.Exec(context.Background(),
		`UPDATE projects SET owner_id = $1 WHERE id = $2`, userID, projectID,
	); err != nil {
		t.Fatalf("set project owner: %v", err)
	}
}

// TestOverviewLiveURLsMissingProbeFieldsAreStaleNotZero pins the day-one shape
// of this panel: gitops-agent has not started writing http_status/http_reason/
// http_checked_at yet when this lands, so every App row with an active
// hostname must read as stale, and the handler must not panic or error trying
// to parse fields that are not there.
func TestOverviewLiveURLsMissingProbeFieldsAreStaleNotZero(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-missing-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	seedLiveURLApp(t, pool, projectID, envID, "no-probe-a-"+suffix,
		`{"url":"https://a.example.dada-tuda.ru","url_status":"active"}`)
	seedLiveURLApp(t, pool, projectID, envID, "no-probe-b-"+suffix,
		`{"url":"https://b.example.dada-tuda.ru","url_status":"active"}`)

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.Checked - before.Checked; got != 0 {
		t.Fatalf("Checked delta = %d, want 0: a row with no http_checked_at must never count as checked", got)
	}
	if got := after.Stale - before.Stale; got != 2 {
		t.Fatalf("Stale delta = %d, want 2: both apps have an active url and no probe result, so both are stale", got)
	}
	if got := after.Dead - before.Dead; got != 0 {
		t.Fatalf("Dead delta = %d, want 0", got)
	}
}

// TestOverviewLiveURLsSplitsOkAndDeadAmongFreshProbes covers the mixed
// 200/502/0 case: fresh results must split cleanly into ok vs dead, and every
// dead one must be named in dead_apps with its status and reason carried
// through untouched.
func TestOverviewLiveURLsSplitsOkAndDeadAmongFreshProbes(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-mixed-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	okName := "ok-app-" + suffix
	dead502Name := "dead-502-" + suffix
	dead0Name := "dead-zero-" + suffix

	seedLiveURLApp(t, pool, projectID, envID, okName, fmt.Sprintf(
		`{"url":"https://ok.example.dada-tuda.ru","url_status":"active","http_status":200,"http_reason":"status_200","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, projectID, envID, dead502Name, fmt.Sprintf(
		`{"url":"https://bad.example.dada-tuda.ru","url_status":"active","http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, projectID, envID, dead0Name, fmt.Sprintf(
		`{"url":"https://unreachable.example.dada-tuda.ru","url_status":"active","http_status":0,"http_reason":"connect_refused","http_checked_at":"%s"}`,
		checkedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.Checked - before.Checked; got != 3 {
		t.Fatalf("Checked delta = %d, want 3", got)
	}
	if got := after.OK - before.OK; got != 1 {
		t.Fatalf("OK delta = %d, want 1: only the 200 counts as ok", got)
	}
	if got := after.Dead - before.Dead; got != 2 {
		t.Fatalf("Dead delta = %d, want 2: http_status 502 and 0 must both count as dead", got)
	}

	byName := map[string]overviewDeadApp{}
	for _, a := range after.DeadApps {
		byName[a.Name] = a
	}
	if _, ok := byName[okName]; ok {
		t.Fatalf("%s answered 200 and must not appear in dead_apps", okName)
	}
	got502, ok := byName[dead502Name]
	if !ok {
		t.Fatalf("dead_apps must name %s", dead502Name)
	}
	if got502.HTTPStatus != 502 || got502.HTTPReason != "status_502" {
		t.Fatalf("%s: HTTPStatus=%d HTTPReason=%q, want 502/status_502", dead502Name, got502.HTTPStatus, got502.HTTPReason)
	}
	got0, ok := byName[dead0Name]
	if !ok {
		t.Fatalf("dead_apps must name %s", dead0Name)
	}
	if got0.HTTPStatus != 0 || got0.HTTPReason != "connect_refused" {
		t.Fatalf("%s: HTTPStatus=%d HTTPReason=%q, want 0/connect_refused", dead0Name, got0.HTTPStatus, got0.HTTPReason)
	}
}

// TestOverviewLiveURLsExpiredProbeIsStaleNotOK guards against a wedged prober
// reading as a wave of healthy apps: a 200 result stamped 45 minutes ago is
// older than liveURLProbeFreshnessWindow (30 minutes) and must count as
// stale, never as ok.
func TestOverviewLiveURLsExpiredProbeIsStaleNotOK(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	staleCheckedAt := time.Now().Add(-45 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-expired-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	name := "expired-probe-" + suffix
	seedLiveURLApp(t, pool, projectID, envID, name, fmt.Sprintf(
		`{"url":"https://expired.example.dada-tuda.ru","url_status":"active","http_status":200,"http_reason":"status_200","http_checked_at":"%s"}`,
		staleCheckedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.Stale - before.Stale; got != 1 {
		t.Fatalf("Stale delta = %d, want 1: a 45-minute-old result is past the 30-minute freshness window", got)
	}
	if got := after.Checked - before.Checked; got != 0 {
		t.Fatalf("Checked delta = %d, want 0", got)
	}
	if got := after.OK - before.OK; got != 0 {
		t.Fatalf("OK delta = %d, want 0: an expired result must never be counted as ok", got)
	}
	for _, a := range after.DeadApps {
		if a.Name == name {
			t.Fatalf("%s has an expired probe result, not a dead one; it must not appear in dead_apps", name)
		}
	}
}

// TestOverviewLiveURLsDeadAppsListExternalOwnersFirst pins the ordering
// contract: dead_apps must surface external customers before internal/staff
// apps, so an operator triaging the list sees real customer impact first.
func TestOverviewLiveURLsDeadAppsListExternalOwnersFirst(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	internalProjectID := overviewBrokenSeedProject(t, pool, "liveurl-internal-"+suffix)
	internalEnvID := overviewBrokenSeedEnv(t, pool, internalProjectID, "prod")
	seedLiveURLOwner(t, pool, internalProjectID, "staff-"+suffix, "ops+"+suffix+"@dada-tuda.ru")
	internalName := "internal-dead-" + suffix
	seedLiveURLApp(t, pool, internalProjectID, internalEnvID, internalName, fmt.Sprintf(
		`{"url":"https://internal.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"status_503","http_checked_at":"%s"}`,
		checkedAt))

	externalProjectID := overviewBrokenSeedProject(t, pool, "liveurl-external-"+suffix)
	externalEnvID := overviewBrokenSeedEnv(t, pool, externalProjectID, "prod")
	seedLiveURLOwner(t, pool, externalProjectID, "customer-"+suffix, "customer-"+suffix+"@gmail.com")
	externalName := "external-dead-" + suffix
	seedLiveURLApp(t, pool, externalProjectID, externalEnvID, externalName, fmt.Sprintf(
		`{"url":"https://external.example.dada-tuda.ru","url_status":"active","http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt))

	out, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs: %v", err)
	}

	internalIdx, externalIdx := -1, -1
	for i, a := range out.DeadApps {
		switch a.Name {
		case internalName:
			internalIdx = i
			if a.External {
				t.Fatalf("%s owner is staff (@dada-tuda.ru); External must be false", internalName)
			}
		case externalName:
			externalIdx = i
			if !a.External {
				t.Fatalf("%s owner is a customer; External must be true", externalName)
			}
		}
	}
	if internalIdx == -1 {
		t.Fatalf("dead_apps must name %s", internalName)
	}
	if externalIdx == -1 {
		t.Fatalf("dead_apps must name %s", externalName)
	}
	if externalIdx > internalIdx {
		t.Fatalf("external app at index %d, internal app at index %d: dead_apps must list external owners first", externalIdx, internalIdx)
	}
}

// TestOverviewLiveURLsSplitsDeadAndCheckedByOwnerClass pins the fix for the
// mixed-signal aggregate: an operator reading live_urls.dead alone cannot
// tell a customer-facing outage from our own internal apps going down.
// DeadExternal/DeadInternal and CheckedExternal/CheckedInternal must each
// reflect isInternalOwnerEmail exactly, and the aggregate Dead/Checked must
// still equal the sum of their two halves.
func TestOverviewLiveURLsSplitsDeadAndCheckedByOwnerClass(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	internalProjectID := overviewBrokenSeedProject(t, pool, "liveurl-split-internal-"+suffix)
	internalEnvID := overviewBrokenSeedEnv(t, pool, internalProjectID, "prod")
	seedLiveURLOwner(t, pool, internalProjectID, "staff-split-"+suffix, "ops-split+"+suffix+"@dada-tuda.ru")
	internalDeadName := "internal-split-dead-" + suffix
	internalOKName := "internal-split-ok-" + suffix
	seedLiveURLApp(t, pool, internalProjectID, internalEnvID, internalDeadName, fmt.Sprintf(
		`{"url":"https://internal-split-dead.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"status_503","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, internalProjectID, internalEnvID, internalOKName, fmt.Sprintf(
		`{"url":"https://internal-split-ok.example.dada-tuda.ru","url_status":"active","http_status":200,"http_reason":"status_200","http_checked_at":"%s"}`,
		checkedAt))

	externalProjectID := overviewBrokenSeedProject(t, pool, "liveurl-split-external-"+suffix)
	externalEnvID := overviewBrokenSeedEnv(t, pool, externalProjectID, "prod")
	seedLiveURLOwner(t, pool, externalProjectID, "customer-split-"+suffix, "customer-split-"+suffix+"@gmail.com")
	externalDeadName1 := "external-split-dead-1-" + suffix
	externalDeadName2 := "external-split-dead-2-" + suffix
	seedLiveURLApp(t, pool, externalProjectID, externalEnvID, externalDeadName1, fmt.Sprintf(
		`{"url":"https://external-split-dead-1.example.dada-tuda.ru","url_status":"active","http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, externalProjectID, externalEnvID, externalDeadName2, fmt.Sprintf(
		`{"url":"https://external-split-dead-2.example.dada-tuda.ru","url_status":"active","http_status":504,"http_reason":"status_504","http_checked_at":"%s"}`,
		checkedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.DeadExternal - before.DeadExternal; got != 2 {
		t.Fatalf("DeadExternal delta = %d, want 2: two customer apps went dead", got)
	}
	if got := after.DeadInternal - before.DeadInternal; got != 1 {
		t.Fatalf("DeadInternal delta = %d, want 1: one staff app went dead", got)
	}
	if got := after.CheckedExternal - before.CheckedExternal; got != 2 {
		t.Fatalf("CheckedExternal delta = %d, want 2", got)
	}
	if got := after.CheckedInternal - before.CheckedInternal; got != 2 {
		t.Fatalf("CheckedInternal delta = %d, want 2", got)
	}

	deadDelta := after.Dead - before.Dead
	if deadDelta != (after.DeadExternal-before.DeadExternal)+(after.DeadInternal-before.DeadInternal) {
		t.Fatalf("Dead delta = %d must equal DeadExternal delta + DeadInternal delta", deadDelta)
	}
	checkedDelta := after.Checked - before.Checked
	if checkedDelta != (after.CheckedExternal-before.CheckedExternal)+(after.CheckedInternal-before.CheckedInternal) {
		t.Fatalf("Checked delta = %d must equal CheckedExternal delta + CheckedInternal delta", checkedDelta)
	}
}

// TestOverviewLiveURLsAppErrorIsNotDeadButGatewayErrorIs pins the exact
// incident that made live_urls.dead lie: telemost-bot and reels-tracker are
// healthy FastAPI apps with no route at "/" (answer 404 there, 200 on
// /health) and were counted as dead alongside n8n, whose hash-domain ingress
// pointed at a Service that no longer exists and answers 503 with no
// backend at all. A 404 the application itself produced must land in
// AppResponded, never in Dead or DeadApps; a 503 from a backend-less proxy
// must land in Dead and be named in DeadApps.
func TestOverviewLiveURLsAppErrorIsNotDeadButGatewayErrorIs(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-classclash-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	appRespondingName := "telemost-bot-like-" + suffix
	noBackendName := "n8n-hashdomain-like-" + suffix

	seedLiveURLApp(t, pool, projectID, envID, appRespondingName, fmt.Sprintf(
		`{"url":"https://telemost-bot.example.dada-tuda.ru","url_status":"active","http_status":404,"http_reason":"status_404","http_checked_at":"%s"}`,
		checkedAt))
	seedLiveURLApp(t, pool, projectID, envID, noBackendName, fmt.Sprintf(
		`{"url":"https://n8n-64b3d0.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"status_503","http_checked_at":"%s"}`,
		checkedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.AppResponded - before.AppResponded; got != 1 {
		t.Fatalf("AppResponded delta = %d, want 1: the 404 app answered, it did not die", got)
	}
	if got := after.Dead - before.Dead; got != 1 {
		t.Fatalf("Dead delta = %d, want 1: only the backend-less 503 counts as dead", got)
	}

	byName := map[string]overviewDeadApp{}
	for _, a := range after.DeadApps {
		byName[a.Name] = a
	}
	if _, ok := byName[appRespondingName]; ok {
		t.Fatalf("%s answered 404 from the app itself and must NOT appear in dead_apps", appRespondingName)
	}
	if _, ok := byName[noBackendName]; !ok {
		t.Fatalf("dead_apps must name %s (503 from a proxy with no backend)", noBackendName)
	}
}
