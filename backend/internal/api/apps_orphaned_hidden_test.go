package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedOrphanedApp inserts an App snapshot already soft-deleted by orphan-GC:
// phase='Orphaned' plus the orphaned_at stamp the sweep writes, which is what a
// row stranded by a git-history replay looks like in production.
func seedOrphanedApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Orphaned', jsonb_build_object('orphaned_at', now()))`,
		projectID, envID, name,
	); err != nil {
		t.Fatalf("seed orphaned app: %v", err)
	}
}

func newGetCtx(t *testing.T, params gin.Params, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = params
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

// TestListApps_HidesOrphanedSnapshot is the regression gate for the phantom app:
// orphan-GC soft-deletes an App snapshot it proved dead (no live pod, no app.yaml
// in git) and keeps the row until purge, and the console used to render that row
// as a working app. Live users hit it on 2026-08-08 and deleted the same app a
// second time.
func TestListApps_HidesOrphanedSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	live := "live-" + uuid.NewString()[:8]
	dead := "dead-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, live)
	seedOrphanedApp(t, pool, projectID, envID, dead)

	c, rec := newGetCtx(t, params(projectID, envID), claims)
	h.ListApps(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Apps []struct {
			Name  string `json:"name"`
			Phase string `json:"phase"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	var sawLive bool
	for _, a := range body.Apps {
		if a.Name == dead {
			t.Fatalf("orphaned snapshot %q is listed as an app (phase=%q) - a deleted app is being shown as working", dead, a.Phase)
		}
		if a.Name == live {
			sawLive = true
		}
	}
	if !sawLive {
		t.Fatalf("live app %q missing from the list - the filter hid a real app; got %+v", live, body.Apps)
	}
}

// TestCountResourceApps_SkipsOrphanedSnapshot guards the second consumer of the
// same soft-deleted row: quota counting. A phantom app charged against the org's
// app quota would block a real deploy the moment BILLING_ENABLED is turned on,
// and consumption would bill for it.
func TestCountResourceApps_SkipsOrphanedSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	orgID := "orphan-quota-" + uuid.NewString()[:8]
	projectID, envID := seedStorageCapFixture(t, pool, orgID)

	before, err := h.countResource(context.Background(), orgID, "apps")
	if err != nil {
		t.Fatalf("count apps before: %v", err)
	}
	seedOrphanedApp(t, pool, projectID, envID, "dead-"+uuid.NewString()[:8])
	after, err := h.countResource(context.Background(), orgID, "apps")
	if err != nil {
		t.Fatalf("count apps after: %v", err)
	}
	if after != before {
		t.Fatalf("orphaned snapshot changed the app count %d -> %d; a soft-deleted app must not consume quota", before, after)
	}

	seedApp(t, pool, projectID, envID, "live-"+uuid.NewString()[:8])
	withLive, err := h.countResource(context.Background(), orgID, "apps")
	if err != nil {
		t.Fatalf("count apps with live: %v", err)
	}
	if withLive != before+1 {
		t.Fatalf("live app count = %d, want %d; the filter swallowed a real app", withLive, before+1)
	}
}
