package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestEnsureDefaultProject_ProvisioningIsAudited pins the audit row on the very
// first server-side thing a new user causes. The console calls this endpoint on
// first load, so for everyone who never types a project slug it is where their
// workspace is born -- and it used to write only an operations row, leaving
// audit_events with nothing before the user's first app. On prod that was 10 of
// 16 project creations in 30 days invisible to the funnel.
func TestEnsureDefaultProject_ProvisioningIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)

	username := "audituser" + uuid.NewString()[:8]
	claims := &auth.Claims{UserID: userID, Username: username}
	slug := defaultProjectSlug(username)
	t.Cleanup(func() {
		dropSeededProjectsByName(pool, slug)
	})

	rec := routeDatabaseCall(t, http.MethodPost, "/projects/default", "/projects/default",
		``, claims, h.EnsureDefaultProject)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var projectID, envID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT p.id, (SELECT e.id FROM environments e WHERE e.project_id = p.id ORDER BY e.name LIMIT 1)
		   FROM projects p WHERE p.name = $1`, slug,
	).Scan(&projectID, &envID); err != nil {
		t.Fatalf("read provisioned project: %v", err)
	}

	var actor uuid.UUID
	var auditEnv *uuid.UUID
	var trigger *string
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_id, environment_id, metadata->>'trigger'
		   FROM audit_events
		  WHERE action = 'CreateProject' AND project_id = $1`, projectID,
	).Scan(&actor, &auditEnv, &trigger); err != nil {
		t.Fatalf("a default project was provisioned but audit_events has no CreateProject row for it — signup is then invisible until the user's first app: %v", err)
	}
	if actor != userID {
		t.Errorf("actor_id = %s, want %s", actor, userID)
	}
	if auditEnv == nil || *auditEnv != envID {
		t.Errorf("environment_id = %v, want %s", auditEnv, envID)
	}
	if trigger == nil || *trigger != "default_project" {
		t.Errorf("metadata.trigger = %v, want default_project — auto-provisioning must stay distinguishable from an explicit create", trigger)
	}
}

// A second call provisions nothing, so it must not invent a second birth event.
func TestEnsureDefaultProject_ReuseIsNotAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)

	username := "audituser" + uuid.NewString()[:8]
	claims := &auth.Claims{UserID: userID, Username: username}
	slug := defaultProjectSlug(username)
	t.Cleanup(func() {
		dropSeededProjectsByName(pool, slug)
	})

	if rec := routeDatabaseCall(t, http.MethodPost, "/projects/default", "/projects/default",
		``, claims, h.EnsureDefaultProject); rec.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var projectID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM projects WHERE name = $1`, slug).Scan(&projectID); err != nil {
		t.Fatalf("read provisioned project: %v", err)
	}

	claims2 := &auth.Claims{UserID: userID, Username: username, Groups: []string{"/projects/" + projectID.String() + "/Owner"}}
	if rec := routeDatabaseCall(t, http.MethodPost, "/projects/default", "/projects/default",
		``, claims2, h.EnsureDefaultProject); rec.Code != http.StatusOK {
		t.Fatalf("second call status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'CreateProject' AND project_id = $1`,
		projectID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("CreateProject audit rows = %d, want 1 — an idempotent call must not look like a second project", n)
	}
}
