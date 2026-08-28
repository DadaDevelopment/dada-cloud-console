package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/google/uuid"
)

// TestResolveById_AProjectIdIsAnAddress is the regression for 2026-08-27:
// listAgents was called with the sandbox project id — the id this repo's own
// CLAUDE.md publishes, and the id every other tool hands back — and answered
// `no such project` with a null candidate list, because the resolver matched
// names only. The caller then had to guess the slug and then an environment,
// three calls to reach an empty list.
func TestResolveById_AProjectIdIsAnAddress(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	slug := "rbi-sandbox-" + suffix

	id := seedNamedProject(t, pool, slug, "RBI Sandbox "+suffix)
	h := &Handler{pool: pool}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}

	got, err := h.visibleProjects(context.Background(), claims, id.String())
	if err != nil {
		t.Fatalf("visibleProjects by id: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("asking for the project id resolved to %d projects %v, want just %s", len(got), got, slug)
	}
	if got[0].Name != slug {
		t.Errorf("name = %q, want %q — resolving by id must still answer with the address a human reads", got[0].Name, slug)
	}
}

// TestResolveById_ObeysTheSameVisibilityAsAName holds the other pole: an id is
// a cheaper way to write an address, not a way to reach past the caller's own
// visibility.
func TestResolveById_ObeysTheSameVisibilityAsAName(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	id := seedNamedProject(t, pool, "rbi-private-"+suffix, "RBI Private "+suffix)
	h := &Handler{pool: pool}

	stranger := &auth.Claims{UserID: uuid.New()}
	got, err := h.visibleProjects(context.Background(), stranger, id.String())
	if err != nil {
		t.Fatalf("visibleProjects by id: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a caller with no claim on the project resolved it by id: %v", got)
	}
}
