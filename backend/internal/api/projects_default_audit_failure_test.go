package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
)

// TestEnsureDefaultProject_NoUsernameIsAudited pins the case a dead signup left
// invisible: a token with no username can never derive a personal org, so the
// handler 400s and, before this test, walked away leaving nothing in
// audit_events -- indistinguishable from a user who never called the endpoint.
func TestEnsureDefaultProject_NoUsernameIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)

	claims := &auth.Claims{UserID: userID}

	rec := routeDatabaseCall(t, http.MethodPost, "/projects/default", "/projects/default",
		``, claims, h.EnsureDefaultProject)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	var reason string
	var status int
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'reason', (metadata->>'status')::int
		   FROM audit_events
		  WHERE action = 'EnsureDefaultProject' AND actor_id = $1 AND outcome = 'failure'
		  ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&reason, &status); err != nil {
		t.Fatalf("a token with no username was rejected but audit_events has no failure row for it -- the dead call is invisible: %v", err)
	}
	if reason != "no_username" {
		t.Errorf("metadata.reason = %q, want no_username", reason)
	}
	if status != http.StatusBadRequest {
		t.Errorf("metadata.status = %d, want %d", status, http.StatusBadRequest)
	}
}

// TestEnsureDefaultProject_CreateFailureIsAudited forces insertProject to fail
// on a real (non-unique-violation) error -- an oversized display_name blows the
// projects.display_name VARCHAR(255) column -- and checks the failure lands in
// audit_events with the error text attached, instead of vanishing like the
// f93095a3@keycloak.local signup did.
func TestEnsureDefaultProject_CreateFailureIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)

	username := strings.Repeat("a", 300)
	claims := &auth.Claims{UserID: userID, Username: username}
	slug := defaultProjectSlug(username)
	t.Cleanup(func() {
		dropSeededProjectsByName(pool, slug)
	})

	rec := routeDatabaseCall(t, http.MethodPost, "/projects/default", "/projects/default",
		``, claims, h.EnsureDefaultProject)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}

	var reason, errText string
	var status int
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'reason', (metadata->>'status')::int, metadata->>'error'
		   FROM audit_events
		  WHERE action = 'EnsureDefaultProject' AND actor_id = $1 AND outcome = 'failure'
		  ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&reason, &status, &errText); err != nil {
		t.Fatalf("insertProject failed but audit_events has no failure row for it -- a dead signup is then indistinguishable from a visitor who left: %v", err)
	}
	if reason != "create_failed" {
		t.Errorf("metadata.reason = %q, want create_failed", reason)
	}
	if status != http.StatusInternalServerError {
		t.Errorf("metadata.status = %d, want %d", status, http.StatusInternalServerError)
	}
	if errText == "" {
		t.Errorf("metadata.error is empty, want the underlying insertProject error text")
	}
}
