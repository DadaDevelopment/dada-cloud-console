package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedGitRepoFixture creates a throwaway project/environment/git_repos row and
// returns the ids DisconnectGitRepo needs. Cleanup cascades from the project.
func seedGitRepoFixture(t *testing.T, pool *pgxpool.Pool, appName string) (projectID, envID, repoID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"gitrepo-audit-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "gitrepo-audit-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, provider, repo_full_name, clone_url,
		    webhook_secret, production_branch, root_dir, auto_deploy, port, replicas, profile)
		 VALUES ($1, $2, $3, 'github', 'acme/repo', 'https://github.com/acme/repo.git',
		         $4, 'main', '.', true, 8080, 1, 'small')
		 RETURNING id`,
		projectID, envID, appName, "secret-"+suffix,
	).Scan(&repoID); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	return projectID, envID, repoID
}

// disconnectGitRepo routes a DELETE through a real gin.Engine (not a bare
// gin.Context) so gin actually flushes the status header — c.Status() alone is
// lazy and only gets written by the engine's ServeHTTP, which a directly
// invoked handler never runs.
func disconnectGitRepo(t *testing.T, h *Handler, projectID, envID, repoID uuid.UUID, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/projects/:projectId/environments/:envId/repos/:repoId", func(c *gin.Context) {
		if claims != nil {
			auth.SetClaims(c, claims)
		}
		h.DisconnectGitRepo(c)
	})
	rec := httptest.NewRecorder()
	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/repos/" + repoID.String()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	r.ServeHTTP(rec, req)
	return rec
}

// TestDisconnectGitRepo_RecordsAuditRow is the slice's Verify line: unlinking a
// repo used to leave the audit chain silently truncated (ConnectGitRepo ->
// TriggerBuild -> nothing), making a disconnect indistinguishable from a user
// who gave up. This pins that a success row now lands with the app name and the
// git_repos row is actually gone.
func TestDisconnectGitRepo_RecordsAuditRow(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "app-" + uuid.NewString()[:8]
	projectID, envID, repoID := seedGitRepoFixture(t, pool, appName)

	rec := disconnectGitRepo(t, h, projectID, envID, repoID, claims)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM git_repos WHERE id = $1`, repoID,
	).Scan(&count); err != nil {
		t.Fatalf("query git_repos: %v", err)
	}
	if count != 0 {
		t.Fatalf("git_repos row still present after disconnect")
	}

	var action, resourceKind, resourceName, outcome string
	err := pool.QueryRow(context.Background(),
		`SELECT action, resource_kind, resource_name, outcome FROM audit_events
		 WHERE actor_id = $1 AND action = 'DisconnectGitRepo' AND project_id = $2
		 ORDER BY created_at DESC LIMIT 1`,
		userID, projectID,
	).Scan(&action, &resourceKind, &resourceName, &outcome)
	if err != nil {
		t.Fatalf("expected a DisconnectGitRepo audit row, got error: %v", err)
	}
	if resourceKind != "GitRepo" {
		t.Errorf("resource_kind = %q, want GitRepo", resourceKind)
	}
	if resourceName != appName {
		t.Errorf("resource_name = %q, want %q", resourceName, appName)
	}
	if outcome != auditOutcomeSuccess {
		t.Errorf("outcome = %q, want %q", outcome, auditOutcomeSuccess)
	}
}

// TestDisconnectGitRepo_UnknownRepo404NoAuditRow asserts a 404 (wrong project,
// wrong env, or unknown repo id) neither deletes anything nor writes an audit
// row — there is nothing to record because nothing happened.
func TestDisconnectGitRepo_UnknownRepo404NoAuditRow(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	projectID, envID, _ := seedGitRepoFixture(t, pool, "app-"+uuid.NewString()[:8])

	rec := disconnectGitRepo(t, h, projectID, envID, uuid.New(), claims)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE actor_id = $1 AND action = 'DisconnectGitRepo'`,
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("audit_events has %d DisconnectGitRepo rows for a 404 response, want 0", count)
	}
}
