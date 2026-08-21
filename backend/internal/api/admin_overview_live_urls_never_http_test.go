package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAppURLHTTPSeen inserts a row into app_url_http_seen, the same table
// hasServedHTTP (app_url_watcher.go) reads to decide whether a failing probe
// is a real outage or an app that was never a web app. It mirrors the row
// recordURLHTTPSeen writes on every passing probe.
func seedAppURLHTTPSeen(t *testing.T, pool *pgxpool.Pool, namespace, appName string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO app_url_http_seen (namespace, app_name, first_seen_at, last_seen_at)
		 VALUES ($1, $2, now(), now())`,
		namespace, appName,
	)
	if err != nil {
		t.Fatalf("seed app_url_http_seen for %s/%s: %v", namespace, appName, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app_url_http_seen WHERE namespace = $1 AND app_name = $2`,
			namespace, appName)
	})
}

// seedLiveURLAppAged is seedLiveURLApp (admin_overview_live_urls_test.go)
// with an explicit first_seen_at, so a test can control the row's age
// against liveURLNeverHTTPMinAge instead of always getting the now() a plain
// insert would carry.
func seedLiveURLAppAged(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, summaryJSON string, firstSeenAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, first_seen_at, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4::jsonb, $5, now())`,
		projectID, envID, name, summaryJSON, firstSeenAt,
	)
	if err != nil {
		t.Fatalf("seed aged live-url app %s: %v", name, err)
	}
}

// TestOverviewLiveURLsNeverHTTPIsTwoPoled pins the exact bug behind backlog
// item 0435: fanvk and sevarateambot (bruzas.85@mail.ru) are long-lived
// Telegram bots with no listening socket at all, measured live on prod
// 2026-08-15 with a 502 on their public hostname and no summary_json.worker
// flag (that flag is a create-time declaration nobody made for them, not an
// observation). The panel folded both into Dead/DeadApps, showing their
// owners a permanently broken app for a bot that was never a web app.
//
// The fix must be two-poled, not a one-way valve that quietly hides Dead
// rows: an old app that never once answered HTTP (no app_url_http_seen row,
// first_seen_at well past liveURLNeverHTTPMinAge) must leave Dead/DeadApps
// and land in NeverHTTP instead (pole A, seeded below as botName with a
// 30-day-old first_seen_at, exactly like fanvk on prod), but an app that DID
// answer HTTP at some point and has since gone dark (pole B, seeded below as
// webAppName, backed by a real app_url_http_seen row) must stay in Dead and
// DeadApps unconditionally -- that is a real outage, and this fix must never
// be able to hide it.
func TestOverviewLiveURLsNeverHTTPIsTwoPoled(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	oldFirstSeen := time.Now().Add(-30 * 24 * time.Hour)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-neverhttp-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod-"+suffix)

	var namespace string
	if err := pool.QueryRow(context.Background(),
		`SELECT namespace FROM environments WHERE id = $1`, envID,
	).Scan(&namespace); err != nil {
		t.Fatalf("read seeded env namespace: %v", err)
	}

	botName := "fanvk-like-" + suffix
	webAppName := "web-app-outage-" + suffix

	seedLiveURLAppAged(t, pool, projectID, envID, botName, fmt.Sprintf(
		`{"url":"https://fanvk-like.example.dada-tuda.ru","url_status":"active","http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt), oldFirstSeen)

	seedLiveURLApp(t, pool, projectID, envID, webAppName, fmt.Sprintf(
		`{"url":"https://web-app-outage.example.dada-tuda.ru","url_status":"active","http_status":502,"http_reason":"status_502","http_checked_at":"%s"}`,
		checkedAt))
	seedAppURLHTTPSeen(t, pool, namespace, webAppName)

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.NeverHTTP - before.NeverHTTP; got != 1 {
		t.Fatalf("NeverHTTP delta = %d, want 1: %s is 30 days old, has never answered HTTP, and must be reclassified out of Dead", got, botName)
	}
	if got := after.Dead - before.Dead; got != 1 {
		t.Fatalf("Dead delta = %d, want 1: only %s (which HAS served HTTP before) may count as dead", got, webAppName)
	}

	byName := map[string]overviewDeadApp{}
	for _, a := range after.DeadApps {
		byName[a.Name] = a
	}
	if _, ok := byName[botName]; ok {
		t.Fatalf("%s has never served HTTP and is well past the age gate; it must NOT appear in dead_apps", botName)
	}
	if _, ok := byName[webAppName]; !ok {
		t.Fatalf("dead_apps must still name %s: it served HTTP before and this is a real outage", webAppName)
	}
}

// TestOverviewLiveURLsYoungNeverHTTPAppStaysDead pins the regression this
// backlog item almost shipped: a row with no app_url_http_seen row does NOT
// by itself mean "this was never a web app" -- it is exactly as consistent
// with "this is a real web app that has been broken since the moment it was
// created and has therefore never once answered either". Reclassifying every
// never-served row into NeverHTTP regardless of age would have hidden that
// second case from the one inventory an owner has, caught by
// TestOverviewLiveURLsAppErrorIsNotDeadButGatewayErrorIs
// (admin_overview_live_urls_test.go) going red the first time this file
// classified purely on ever_served_http.
//
// A row seeded with the default first_seen_at (now(), well inside
// liveURLNeverHTTPMinAge) and no app_url_http_seen row must stay in Dead and
// DeadApps, not fall into NeverHTTP just because it happens to share the
// never-served half of the predicate with a genuine long-lived bot.
func TestOverviewLiveURLsYoungNeverHTTPAppStaysDead(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	checkedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	before, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (before): %v", err)
	}

	projectID := overviewBrokenSeedProject(t, pool, "liveurl-young-neverhttp-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod-"+suffix)

	brokenFromBirthName := "broken-from-birth-" + suffix
	seedLiveURLApp(t, pool, projectID, envID, brokenFromBirthName, fmt.Sprintf(
		`{"url":"https://broken-from-birth.example.dada-tuda.ru","url_status":"active","http_status":503,"http_reason":"status_503","http_checked_at":"%s"}`,
		checkedAt))

	after, err := h.overviewLiveURLs(context.Background())
	if err != nil {
		t.Fatalf("overviewLiveURLs (after): %v", err)
	}

	if got := after.NeverHTTP - before.NeverHTTP; got != 0 {
		t.Fatalf("NeverHTTP delta = %d, want 0: %s is brand new and must not be reclassified out of Dead this early", got, brokenFromBirthName)
	}
	if got := after.Dead - before.Dead; got != 1 {
		t.Fatalf("Dead delta = %d, want 1: a day-old app with a backend-less 503 and no http_seen row is a real outage, not a bot", got)
	}

	found := false
	for _, a := range after.DeadApps {
		if a.Name == brokenFromBirthName {
			found = true
		}
	}
	if !found {
		t.Fatalf("dead_apps must name %s: it is young, never answered HTTP, and that is a live outage the owner must see", brokenFromBirthName)
	}
}

// TestOverviewLiveURLsNeverHTTPDegradesToDeadWhenNamespaceUnresolved documents
// the fail-loud rule this reclassification borrows from hasServedHTTP: if the
// join back to environments cannot resolve a namespace for the row (no
// matching environments row for rs.environment_id), overviewLiveURLs must
// NOT guess "never served" -- it must keep the row in Dead, exactly like a
// database error inside hasServedHTTP returns true rather than false. No
// fixture can orphan resource_snapshots.environment_id under the live
// foreign key, so this branch (the CASE WHEN e.namespace IS NULL THEN true
// clause in overviewLiveURLs) is pinned by code review and by its own doc
// comment rather than a runnable case; this test exists so the guarantee is
// discoverable from the test file, not only from the source comment.
func TestOverviewLiveURLsNeverHTTPDegradesToDeadWhenNamespaceUnresolved(t *testing.T) {
	t.Skip("degrade-to-dead branch guarded by the environment_id foreign key; no fixture can orphan it, see doc comment above")
}
