package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDemoExpiryFor_OnlyPlatformStarters pins the blast radius of the reaper:
// the only apps that ever get a deletion deadline are the three showroom
// templates the console itself offers. A user repository whose name merely
// looks like a starter must come back nil, because a wrong true here deletes
// somebody's real application.
func TestDemoExpiryFor_OnlyPlatformStarters(t *testing.T) {
	h := &Handler{cfg: &config.Config{DemoAppTTLHours: 24}}

	for _, repo := range []string{
		"DadaDevelopment/dada-nextjs-starter",
		"dadadevelopment/DADA-FASTAPI-STARTER",
		"DadaDevelopment/dada-static-starter",
	} {
		got := h.demoExpiryFor(repo)
		if got == nil {
			t.Fatalf("demoExpiryFor(%q) = nil, want a deadline", repo)
		}
		if d := time.Until(*got); d < 23*time.Hour || d > 25*time.Hour {
			t.Fatalf("demoExpiryFor(%q) deadline in %v, want ~24h", repo, d)
		}
	}

	for _, repo := range []string{
		"acme/dada-nextjs-starter",
		"DadaDevelopment/dada-nextjs-starter-fork",
		"DadaDevelopment/console",
		"",
	} {
		if got := h.demoExpiryFor(repo); got != nil {
			t.Fatalf("demoExpiryFor(%q) = %v, want nil", repo, got)
		}
	}
}

// TestDemoExpiryFor_TTLZeroDisables pins the kill switch. DEMO_APP_TTL_HOURS=0
// must mean "no automatic deletion at all", not "delete immediately" and not
// "fall back to the default" -- the latter is exactly how a previous switch
// (the box pool's) silently stopped existing.
func TestDemoExpiryFor_TTLZeroDisables(t *testing.T) {
	h := &Handler{cfg: &config.Config{DemoAppTTLHours: 0}}
	if got := h.demoExpiryFor("DadaDevelopment/dada-nextjs-starter"); got != nil {
		t.Fatalf("demoExpiryFor with TTL 0 = %v, want nil", got)
	}
	if h.demoAppTTL() != 0 {
		t.Fatalf("demoAppTTL with 0 hours = %v, want 0", h.demoAppTTL())
	}
}

// seedDemoRepo creates a throwaway project/environment/git_repos row carrying
// the given deletion deadline and returns the ids the reaper works on.
func seedDemoRepo(t *testing.T, pool *pgxpool.Pool, appName string, expires *time.Time) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"demo-reap-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })
	t.Cleanup(func() { dropSeededAudit(pool, "App", appName) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "demo-reap-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, provider, repo_full_name, clone_url,
		    webhook_secret, production_branch, root_dir, auto_deploy, port, replicas, profile,
		    demo_expires_at)
		 VALUES ($1, $2, $3, 'github', 'DadaDevelopment/dada-nextjs-starter',
		         'https://github.com/DadaDevelopment/dada-nextjs-starter.git',
		         $4, 'main', '.', true, 3000, 1, 'small', $5)`,
		projectID, envID, appName, "secret-"+suffix, expires,
	); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	return projectID, envID
}

func countDeleteOps(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, appName string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations
		  WHERE project_id = $1 AND action = 'DeleteApp' AND resource_name = $2`,
		projectID, appName).Scan(&n); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	return n
}

func demoDeadline(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, appName string) *time.Time {
	t.Helper()
	var deadline *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT demo_expires_at FROM git_repos WHERE project_id = $1 AND app_name = $2`,
		projectID, appName).Scan(&deadline); err != nil {
		t.Fatalf("read demo_expires_at: %v", err)
	}
	return deadline
}

// TestReapExpiredDemoApps_EnqueuesOnce is the guarantee that keeps the showroom
// from turning into a landfill without turning into a double-delete storm: the
// expired demo gets exactly one DeleteApp operation, and because the deadline is
// cleared in the same transaction, the next tick -- which fires long before
// gitops-agent finishes the cascade -- enqueues nothing.
func TestReapExpiredDemoApps_EnqueuesOnce(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DemoAppTTLHours: 24}}
	appName := "demo-" + uuid.NewString()[:8]
	past := time.Now().Add(-time.Hour)
	projectID, _ := seedDemoRepo(t, pool, appName, &past)

	h.reapExpiredDemoApps(context.Background())

	if got := countDeleteOps(t, pool, projectID, appName); got != 1 {
		t.Fatalf("after first tick: %d DeleteApp operations, want 1", got)
	}
	if d := demoDeadline(t, pool, projectID, appName); d != nil {
		t.Fatalf("deadline after reap = %v, want NULL", d)
	}

	h.reapExpiredDemoApps(context.Background())

	if got := countDeleteOps(t, pool, projectID, appName); got != 1 {
		t.Fatalf("after second tick: %d DeleteApp operations, want still 1", got)
	}

	var reason string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'reason' FROM audit_events
		  WHERE resource_kind = 'App' AND resource_name = $1 AND action = 'DeleteApp'`,
		appName).Scan(&reason); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if reason != "demo_expired" {
		t.Fatalf("audit reason = %q, want demo_expired", reason)
	}
}

// TestReapExpiredDemoApps_LeavesFutureAndOrdinaryApps pins that the reaper only
// touches rows past their deadline: an unexpired demo and an app with no
// deadline at all survive the tick untouched.
func TestReapExpiredDemoApps_LeavesFutureAndOrdinaryApps(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DemoAppTTLHours: 24}}

	futureApp := "demo-" + uuid.NewString()[:8]
	future := time.Now().Add(12 * time.Hour)
	futureProject, _ := seedDemoRepo(t, pool, futureApp, &future)

	plainApp := "app-" + uuid.NewString()[:8]
	plainProject, _ := seedDemoRepo(t, pool, plainApp, nil)

	h.reapExpiredDemoApps(context.Background())

	if got := countDeleteOps(t, pool, futureProject, futureApp); got != 0 {
		t.Fatalf("unexpired demo got %d DeleteApp operations, want 0", got)
	}
	if d := demoDeadline(t, pool, futureProject, futureApp); d == nil {
		t.Fatal("unexpired demo lost its deadline")
	}
	if got := countDeleteOps(t, pool, plainProject, plainApp); got != 0 {
		t.Fatalf("app without a deadline got %d DeleteApp operations, want 0", got)
	}
}

// TestKeepDemoApp_CancelsTheDeletion is the user's escape hatch: claiming a demo
// clears the deadline, so the very next reaper tick has nothing to delete even
// though the deadline had already passed.
func TestKeepDemoApp_CancelsTheDeletion(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DemoAppTTLHours: 24}}
	userID := seedUser(t, pool)
	appName := "demo-" + uuid.NewString()[:8]
	past := time.Now().Add(-time.Hour)
	projectID, envID := seedDemoRepo(t, pool, appName, &past)
	t.Cleanup(func() { dropSeededAudit(pool, "App", appName) })

	rec := keepDemoApp(t, h, projectID, envID, appName, godClaims(userID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if d := demoDeadline(t, pool, projectID, appName); d != nil {
		t.Fatalf("deadline after keep = %v, want NULL", d)
	}

	h.reapExpiredDemoApps(context.Background())

	if got := countDeleteOps(t, pool, projectID, appName); got != 0 {
		t.Fatalf("kept app got %d DeleteApp operations, want 0", got)
	}
}

// TestKeepDemoApp_UnknownAppIs404 pins that the endpoint cannot be used to probe
// which app names exist by returning 200 for everything.
func TestKeepDemoApp_UnknownAppIs404(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DemoAppTTLHours: 24}}
	userID := seedUser(t, pool)
	appName := "demo-" + uuid.NewString()[:8]
	past := time.Now().Add(-time.Hour)
	projectID, envID := seedDemoRepo(t, pool, appName, &past)

	rec := keepDemoApp(t, h, projectID, envID, "nope-"+uuid.NewString()[:8], godClaims(userID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// keepDemoApp routes the claim through a real gin.Engine so path params and the
// flushed status behave exactly as they do in production.
func keepDemoApp(t *testing.T, h *Handler, projectID, envID uuid.UUID, appName string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/projects/:projectId/environments/:envId/apps/:appName/keep", func(c *gin.Context) {
		if claims != nil {
			auth.SetClaims(c, claims)
		}
		h.KeepDemoApp(c)
	})
	rec := httptest.NewRecorder()
	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/keep"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	r.ServeHTTP(rec, req)
	return rec
}

// TestFillDemoExpiry_OnlyMarksMatchingApps pins the console-facing half: the
// deadline lands on the snapshot of the same name and nothing else, so an
// ordinary app never renders the "will be deleted" badge.
func TestFillDemoExpiry_OnlyMarksMatchingApps(t *testing.T) {
	deadline := time.Now().Add(3 * time.Hour)
	apps := []models.ResourceSnapshot{{Name: "demo"}, {Name: "real"}}
	rows := []GitRepoRow{
		{Name: "demo", DemoExpiresAt: &deadline},
		{Name: "real"},
	}

	FillDemoExpiry(apps, rows)

	if apps[0].DemoExpiresAt == nil || !apps[0].DemoExpiresAt.Equal(deadline) {
		t.Fatalf("demo app deadline = %v, want %v", apps[0].DemoExpiresAt, deadline)
	}
	if apps[1].DemoExpiresAt != nil {
		t.Fatalf("ordinary app deadline = %v, want nil", apps[1].DemoExpiresAt)
	}
}
