package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedNamedProject seeds a project whose slug and display name differ, which is
// the shape every real project has: the console shows "Agent Runtime", the
// address is "agents".
func seedNamedProject(t *testing.T, pool *pgxpool.Pool, name, displayName string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $2, 'dada') RETURNING id`,
		name, displayName,
	).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, id) })
	return id
}

func TestNormalizeName_FoldsCaseAndSeparators(t *testing.T) {
	for _, in := range []string{"Agent Runtime", "agent-runtime", "AgentRuntime", " Agent_Runtime "} {
		if got := normalizeName(in); got != "agentruntime" {
			t.Errorf("normalizeName(%q) = %q, want agentruntime", in, got)
		}
	}
	if got := normalizeName("  "); got != "" {
		t.Errorf("normalizeName of blank = %q, want empty", got)
	}
}

// TestResolveByName_AcceptsTheNameTheConsoleShows is the regression for the
// address a caller can actually read.
//
// The project switcher, the breadcrumb and every screenshot show display_name
// ("Agent Runtime"); the slug ("agents") appears nowhere in the UI. Resolving
// only the slug meant the name on screen was the one name that 404'd, and the
// 404 named no candidates, so the caller's only way forward was the
// listProjects walk this endpoint exists to remove.
func TestResolveByName_AcceptsTheNameTheConsoleShows(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	slug := "rdn-agents-" + suffix
	display := "RDN Agent Runtime " + suffix

	id := seedNamedProject(t, pool, slug, display)
	h := &Handler{pool: pool}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	ctx := context.Background()

	for _, asked := range []string{
		slug,
		display,
		normalizeName(display),
	} {
		got, err := h.visibleProjectsByName(ctx, claims, asked)
		if err != nil {
			t.Fatalf("visibleProjectsByName(%q): %v", asked, err)
		}
		if len(got) != 1 || got[0].ID != id {
			t.Fatalf("asking for %q resolved to %d projects %v, want just %s — the name on screen must be an address",
				asked, len(got), got, slug)
		}
	}
}

// TestResolveByName_SlugBeatsDisplayName keeps an exact address exact.
//
// Dozens of projects carry the display_name "Default". If a display-name match
// ranked equal with a slug match, a caller naming a project by its slug would
// get a 409 listing strangers instead of the project they addressed.
func TestResolveByName_SlugBeatsDisplayName(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	shared := "rdn-collide-" + suffix

	wanted := seedNamedProject(t, pool, shared, "RDN wanted "+suffix)
	seedNamedProject(t, pool, "rdn-other-"+suffix, shared)

	h := &Handler{pool: pool}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}

	got, err := h.visibleProjectsByName(context.Background(), claims, shared)
	if err != nil {
		t.Fatalf("visibleProjectsByName: %v", err)
	}
	if len(got) != 1 || got[0].ID != wanted {
		t.Fatalf("slug %q resolved to %v, want only the project whose name it is", shared, got)
	}
}

// TestNameCandidates_NamesWhatItCouldSee closes the dead end: a project-level
// miss used to answer a bare {"error":"not found"}.
func TestNameCandidates_NamesWhatItCouldSee(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	slug := "rdn-near-" + suffix

	id := seedNamedProject(t, pool, slug, "RDN Near "+suffix)
	h := &Handler{pool: pool}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	ctx := context.Background()

	if got, err := h.visibleProjectsByName(ctx, claims, slug+"-typo"); err != nil || len(got) != 0 {
		t.Fatalf("a wrong name must not resolve: got %v, err %v", got, err)
	}

	near, err := h.nameCandidates(ctx, claims, slug+"-typo")
	if err != nil {
		t.Fatalf("nameCandidates: %v", err)
	}
	found := false
	for _, p := range near {
		if p.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("miss on %q suggested %v, want %s among them — a 404 with no candidates sends the caller back to listing everything",
			slug+"-typo", near, slug)
	}
}
