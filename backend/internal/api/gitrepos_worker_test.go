package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedWorkerProjectEnv creates a throwaway project and prod environment for a
// ConnectGitRepo call. Cleanup cascades from the project.
func seedWorkerProjectEnv(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"gitrepo-worker-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "gitrepo-worker-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

func connectGitRepo(t *testing.T, h *Handler, projectID, envID uuid.UUID, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/projects/:projectId/environments/:envId/repos", func(c *gin.Context) {
		if claims != nil {
			auth.SetClaims(c, claims)
		}
		h.ConnectGitRepo(c)
	})
	rec := httptest.NewRecorder()
	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/repos"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// TestConnectGitRepo_WorkerKeepsPortZero pins the fix for the invented port: a
// repo declared as a worker (a bot, a queue consumer) must keep port 0 all the
// way into git_repos, because Service.Enabled is spec.Port > 0 downstream. Every
// live victim of the old behaviour — three apps, 860+ consecutive failed probes
// on a port nobody opened — got there through this handler defaulting to 8080.
func TestConnectGitRepo_WorkerKeepsPortZero(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	claims := godClaims(seedUser(t, pool))
	projectID, envID := seedWorkerProjectEnv(t, pool)
	appName := "wrk-" + uuid.NewString()[:8]

	rec := connectGitRepo(t, h, projectID, envID, claims,
		`{"repo_full_name":"acme/bot","app_name":"`+appName+`","worker":true}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var port int
	var worker bool
	if err := pool.QueryRow(context.Background(),
		`SELECT port, worker FROM git_repos WHERE project_id = $1 AND app_name = $2`,
		projectID, appName,
	).Scan(&port, &worker); err != nil {
		t.Fatalf("query git_repos: %v", err)
	}
	if port != 0 {
		t.Errorf("port = %d, want 0 — a worker with a port renders a Service nothing listens on", port)
	}
	if !worker {
		t.Errorf("worker = false, want true — the flag never reached the row")
	}
}

// TestConnectGitRepo_HTTPAppStillDefaultsPort guards the other side: an app that
// did not declare itself a worker keeps the 8080 default and the range check.
func TestConnectGitRepo_HTTPAppStillDefaultsPort(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	claims := godClaims(seedUser(t, pool))
	projectID, envID := seedWorkerProjectEnv(t, pool)
	appName := "web-" + uuid.NewString()[:8]

	rec := connectGitRepo(t, h, projectID, envID, claims,
		`{"repo_full_name":"acme/web","app_name":"`+appName+`"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var port int
	var worker bool
	if err := pool.QueryRow(context.Background(),
		`SELECT port, worker FROM git_repos WHERE project_id = $1 AND app_name = $2`,
		projectID, appName,
	).Scan(&port, &worker); err != nil {
		t.Fatalf("query git_repos: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
	if worker {
		t.Errorf("worker = true for an app that never asked for it")
	}

	bad := connectGitRepo(t, h, projectID, envID, claims,
		`{"repo_full_name":"acme/web2","app_name":"`+appName+`-2","port":70000}`)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("port 70000 status = %d, want 400; body=%s", bad.Code, bad.Body.String())
	}
}
