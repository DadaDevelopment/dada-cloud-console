package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestUpsertProject_NewProjectWithOwner_SetsOwnerID is the RED-proof case for
// the structural bug this fix closes: gitops-agent/internal/db/projects.go's
// INSERT never named the owner_id column at all, so a git-origin project
// could not get an owner no matter what a manifest said. A new project synced
// with a resolved owner must now land with owner_id populated.
func TestUpsertProject_NewProjectWithOwner_SetsOwnerID(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	ownerID := seedUser(t, ctx, pool, "owner@example.com", "owner-user")

	isNew, err := UpsertProject(ctx, pool, "owner-fix-new-project", "Client A", "client", "prod", nil, &ownerID)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if !isNew {
		t.Fatal("UpsertProject isNew = false, want true for a brand-new project")
	}

	got := readOwnerID(t, ctx, pool, "owner-fix-new-project")
	if got == nil || *got != ownerID {
		t.Fatalf("projects.owner_id = %v, want %s", got, ownerID)
	}
}

// TestUpsertProject_ConflictNeverClobbersExistingOwner guards the shared-data
// safety rule from the fix's spec: once a project has a real owner_id, a later
// sync (any manifest, any owner value including a *different* resolved user or
// none at all) must never overwrite it. M5 lives on shared prod rows and a
// stale/empty manifest re-sync must not un-assign a real owner.
func TestUpsertProject_ConflictNeverClobbersExistingOwner(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	firstOwner := seedUser(t, ctx, pool, "first@example.com", "first-user")
	secondOwner := seedUser(t, ctx, pool, "second@example.com", "second-user")

	if _, err := UpsertProject(ctx, pool, "owner-fix-existing-owner", "Client B", "client", "prod", nil, &firstOwner); err != nil {
		t.Fatalf("initial UpsertProject: %v", err)
	}

	isNew, err := UpsertProject(ctx, pool, "owner-fix-existing-owner", "Client B", "client", "prod", nil, &secondOwner)
	if err != nil {
		t.Fatalf("re-sync UpsertProject: %v", err)
	}
	if isNew {
		t.Fatal("UpsertProject isNew = true, want false on the second call (conflict path)")
	}

	got := readOwnerID(t, ctx, pool, "owner-fix-existing-owner")
	if got == nil || *got != firstOwner {
		t.Fatalf("projects.owner_id = %v, want unchanged first owner %s", got, firstOwner)
	}
}

// TestUpsertProject_ConflictHealsLegacyNullOwner confirms the COALESCE side of
// the same rule: a legacy row still NULL (like the pre-fix client-a-prod row)
// does get healed by a later sync that resolves an owner, since there is
// nothing real to clobber yet.
func TestUpsertProject_ConflictHealsLegacyNullOwner(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	if _, err := UpsertProject(ctx, pool, "owner-fix-legacy-null", "Client C", "client", "prod", nil, nil); err != nil {
		t.Fatalf("initial UpsertProject (no owner): %v", err)
	}
	if got := readOwnerID(t, ctx, pool, "owner-fix-legacy-null"); got != nil {
		t.Fatalf("baseline projects.owner_id = %v, want nil", got)
	}

	owner := seedUser(t, ctx, pool, "healed@example.com", "healed-user")
	if _, err := UpsertProject(ctx, pool, "owner-fix-legacy-null", "Client C", "client", "prod", nil, &owner); err != nil {
		t.Fatalf("healing UpsertProject: %v", err)
	}

	got := readOwnerID(t, ctx, pool, "owner-fix-legacy-null")
	if got == nil || *got != owner {
		t.Fatalf("projects.owner_id = %v, want healed owner %s", got, owner)
	}
}

// TestResolveUserIDByIdentity_MatchesEmailOrKeycloakSub covers the two
// identifiers a project.yaml owner field can plausibly carry.
func TestResolveUserIDByIdentity_MatchesEmailOrKeycloakSub(t *testing.T) {
	pool := requireTestPool(t)
	ctx := context.Background()
	applyMigrationsForReapTest(t, ctx, pool)

	byEmail := seedUser(t, ctx, pool, "byemail@example.com", "by-email-user")
	bySub := seedUserWithSub(t, ctx, pool, "bysub@example.com", "by-sub-user", "kc-sub-123")

	if id, ok, err := ResolveUserIDByIdentity(ctx, pool, "byemail@example.com"); err != nil || !ok || id != byEmail {
		t.Fatalf("resolve by email = (%s, %v, %v), want (%s, true, nil)", id, ok, err, byEmail)
	}
	if id, ok, err := ResolveUserIDByIdentity(ctx, pool, "kc-sub-123"); err != nil || !ok || id != bySub {
		t.Fatalf("resolve by keycloak_sub = (%s, %v, %v), want (%s, true, nil)", id, ok, err, bySub)
	}
	if _, ok, err := ResolveUserIDByIdentity(ctx, pool, "nobody@example.com"); err != nil || ok {
		t.Fatalf("resolve unknown identity: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if _, ok, err := ResolveUserIDByIdentity(ctx, pool, ""); err != nil || ok {
		t.Fatalf("resolve empty identity: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func requireTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, username string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name) VALUES ($1, $2, $3, 'x', $2)`,
		id, username, email)
	return id
}

func seedUserWithSub(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, username, keycloakSub string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name, keycloak_sub) VALUES ($1, $2, $3, 'x', $2, $4)`,
		id, username, email, keycloakSub)
	return id
}

func readOwnerID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	err := pool.QueryRow(ctx, `SELECT owner_id FROM projects WHERE name = $1`, name).Scan(&id)
	if err != nil {
		t.Fatalf("read owner_id for %s: %v", name, err)
	}
	return id
}
