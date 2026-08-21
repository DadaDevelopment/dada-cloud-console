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

// searchBody is the shape Search returns.
type searchBody struct {
	Projects []searchProjectHit `json:"projects"`
	Apps     []searchAppHit     `json:"apps"`
}

// runSearch calls the handler with the given query string and claims.
func runSearch(t *testing.T, h *Handler, q string, claims *auth.Claims) (int, searchBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+q, nil)
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	h.Search(c)

	var body searchBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

// hasApp reports whether the hits contain an app of that name.
func hasApp(hits []searchAppHit, name string) bool {
	for _, a := range hits {
		if a.Name == name {
			return true
		}
	}
	return false
}

// TestSearch_FindsAppAcrossProjects is the gate for the reported failure: an
// admin knew an app's name ("agent-sync-hub", "telemost-bot") and had no way to
// reach it — the console only ever loads one project's app list at a time, and
// with 67 projects on the platform, most of them empty test leftovers, opening
// them one by one was the only path. The hit must carry the project and the
// environment so the console can link straight at the app page.
func TestSearch_FindsAppAcrossProjects(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	name := "telemostbot" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, name)

	code, body := runSearch(t, h, name[:12], claims)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !hasApp(body.Apps, name) {
		t.Fatalf("app %q missing from hits: %+v", name, body.Apps)
	}
	for _, a := range body.Apps {
		if a.Name != name {
			continue
		}
		if a.ProjectID != projectID.String() {
			t.Errorf("project_id = %q, want %q", a.ProjectID, projectID)
		}
		if a.EnvironmentID != envID.String() {
			t.Errorf("environment_id = %q, want %q", a.EnvironmentID, envID)
		}
		if a.Phase != "Ready" {
			t.Errorf("phase = %q, want Ready", a.Phase)
		}
	}
}

// TestSearch_HidesAppsOfInvisibleProjects proves search cannot widen access: a
// caller holding no role on the project and no org admin rights gets nothing,
// even with the exact app name.
func TestSearch_HidesAppsOfInvisibleProjects(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	name := "secret" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, name)

	stranger := &auth.Claims{UserID: seedUser(t, pool)}
	code, body := runSearch(t, h, name, stranger)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if hasApp(body.Apps, name) {
		t.Fatalf("app %q leaked to a caller with no access: %+v", name, body.Apps)
	}
	for _, p := range body.Projects {
		if p.ID == projectID.String() {
			t.Fatalf("project %q leaked to a caller with no access", p.ID)
		}
	}
}

// TestSearch_SkipsOrphanedApps keeps search consistent with the app list: an app
// soft-deleted by orphan-GC is a tombstone, and surfacing it would hand the user
// a link to a page for an app that no longer exists.
func TestSearch_SkipsOrphanedApps(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	stem := "ghost" + uuid.NewString()[:8]
	seedOrphanedApp(t, pool, projectID, envID, stem)

	code, body := runSearch(t, h, stem, claims)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if hasApp(body.Apps, stem) {
		t.Fatalf("orphaned app %q surfaced: %+v", stem, body.Apps)
	}
}

// TestSearch_EscapesLikeWildcards proves the query is matched literally: an
// unescaped "%" would turn any query containing it into "match everything",
// which for a platform admin means dumping every app on the platform.
func TestSearch_EscapesLikeWildcards(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	name := "wild" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, name)

	code, body := runSearch(t, h, "w%25d", claims)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if hasApp(body.Apps, name) {
		t.Fatalf("wildcard query matched %q; pattern was not escaped", name)
	}
}

// TestSearch_FindsGitConnectedAppBeforeFirstSync covers the window between
// connecting a repo and the first gitops sync: the app exists to the user but
// has no resource snapshot yet, so a snapshot-only search would answer "no such
// app" about an app the console itself just created.
func TestSearch_FindsGitConnectedAppBeforeFirstSync(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	name := "freshrepo" + uuid.NewString()[:8]
	seedGitRepoOnly(t, pool, projectID, envID, name)

	code, body := runSearch(t, h, name, claims)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !hasApp(body.Apps, name) {
		t.Fatalf("git-connected app %q missing from hits: %+v", name, body.Apps)
	}
}

// seedGitRepoOnly connects a repo without any resource snapshot: what an app
// looks like between "connect repo" and the first sync.
func seedGitRepoOnly(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, 'github', $4, $5)`,
		projectID, envID, name, "acme/"+name, "https://github.com/acme/"+name+".git",
	); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM git_repos WHERE project_id = $1 AND app_name = $2`, projectID, name)
	})
}
