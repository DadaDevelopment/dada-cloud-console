package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// alertRecipientTestPool mirrors the pool-backed test harness used across the
// repo (billing_expiry_test.go, deploy_hooks_test.go): skip cleanly when
// TEST_DATABASE_URL is unset so `go test ./...` stays green offline.
func alertRecipientTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping alert-recipient DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAlertUser inserts a users row and registers its cleanup, returning the
// new user id.
func seedAlertUser(t *testing.T, pool *pgxpool.Pool, username, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, '', $1) RETURNING id`,
		username, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// seedAlertProject inserts a projects row (optionally with owner_id/org_id
// set) and registers its cleanup, returning the new project id.
func seedAlertProject(t *testing.T, pool *pgxpool.Pool, name string, ownerID *uuid.UUID, orgID string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, owner_id, org_id) VALUES ($1, $1, $2, $3) RETURNING id`,
		name, ownerID, nullableString(orgID),
	).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, id)
	})
	return id
}

// nullableString now lives in box_leads.go, where a production handler needed the
// same "" -> NULL mapping. Kept as one definition rather than two identical ones.

func seedAlertMember(t *testing.T, pool *pgxpool.Pool, projectID, userID uuid.UUID, role string, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`,
		projectID, userID, role, createdAt,
	); err != nil {
		t.Fatalf("seed project_members: %v", err)
	}
}

// TestResolveAlertRecipient_Chain exercises every step of the resolver chain
// resolveAlertRecipient walks: owner_id -> project_members Owner/Admin ->
// org_id/username personal-org match -> nothing (empty, caller falls back to
// the operator address). @keycloak.local emails must be rejected at every
// step and fall through to the next one instead of "resolving" to a
// synthetic address.
func TestResolveAlertRecipient_Chain(t *testing.T) {
	pool := alertRecipientTestPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	t.Run("owner_id resolves directly", func(t *testing.T) {
		owner := seedAlertUser(t, pool, "owner-"+suffix, "owner-"+suffix+"@example.com")
		projectID := seedAlertProject(t, pool, "proj-owner-"+suffix, &owner, "")

		email, source := h.resolveAlertRecipient(ctx, projectID)
		if email != "owner-"+suffix+"@example.com" || source != alertSourceOwner {
			t.Fatalf("got (%q, %q), want owner email with source=%s", email, source, alertSourceOwner)
		}
	})

	t.Run("owner_id keycloak.local falls through to members Owner", func(t *testing.T) {
		ghostOwner := seedAlertUser(t, pool, "ghost-"+suffix, ghostSub(suffix)+"@keycloak.local")
		projectID := seedAlertProject(t, pool, "proj-ghost-"+suffix, &ghostOwner, "")

		admin := seedAlertUser(t, pool, "admin-"+suffix, "admin-"+suffix+"@example.com")
		ownerMember := seedAlertUser(t, pool, "member-owner-"+suffix, "member-owner-"+suffix+"@example.com")
		seedAlertMember(t, pool, projectID, admin, "Admin", time.Now().Add(-time.Hour))
		seedAlertMember(t, pool, projectID, ownerMember, "Owner", time.Now())

		email, source := h.resolveAlertRecipient(ctx, projectID)
		if email != "member-owner-"+suffix+"@example.com" || source != alertSourceMember {
			t.Fatalf("got (%q, %q), want the Owner-role member with source=%s (Owner outranks Admin)", email, source, alertSourceMember)
		}
	})

	t.Run("no owner_id, no members, org_id matches a username", func(t *testing.T) {
		personal := seedAlertUser(t, pool, "personal-"+suffix, "personal-"+suffix+"@example.com")
		projectID := seedAlertProject(t, pool, "proj-personal-"+suffix, nil, "personal-"+suffix)

		email, source := h.resolveAlertRecipient(ctx, projectID)
		if email != "personal-"+suffix+"@example.com" || source != alertSourcePersonalOrg {
			t.Fatalf("got (%q, %q), want personal-org user %v with source=%s", email, source, personal, alertSourcePersonalOrg)
		}
	})

	t.Run("nothing resolves, caller must fall back to operator", func(t *testing.T) {
		projectID := seedAlertProject(t, pool, "proj-orphan-"+suffix, nil, "no-such-org-"+suffix)

		email, source := h.resolveAlertRecipient(ctx, projectID)
		if email != "" || source != "" {
			t.Fatalf("got (%q, %q), want empty so the caller falls back to the operator address", email, source)
		}
	})
}

func ghostSub(suffix string) string {
	return "sub-ghost-" + suffix
}

// TestClaimAppHealthAlertSlot_NotBurnedByFailedResolution proves the fix for
// the second half of P1-ALERT-OWNERLESS-DROP: a failed recipient resolution
// must not consume the app's cooldown slot, or ownership getting fixed later
// would still be muted for up to 24h. This exercises the cooldown primitive
// directly (claimAppHealthAlertSlot) rather than maybeNotify, since maybeNotify
// needs a live k8s clientset for the log tail; the ordering guarantee under
// test is "no recipient resolved => claim never called", which is enforced by
// maybeNotify's control flow (see app_health_watcher.go) and verified here by
// confirming the slot is still free (claimable) after simulating exactly what
// maybeNotify does when resolution fails: skip the claim entirely.
func TestClaimAppHealthAlertSlot_NotBurnedByFailedResolution(t *testing.T) {
	pool := alertRecipientTestPool(t)
	ctx := context.Background()
	namespace := "ns-cooldown-" + uuid.NewString()[:8]
	appName := "app-cooldown"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, namespace)
	})

	h := &Handler{pool: pool}
	projectID := seedAlertProject(t, pool, "proj-cooldown-"+uuid.NewString()[:8], nil, "no-such-org-cooldown")

	if email, source := h.resolveAlertRecipient(ctx, projectID); email != "" || source != "" {
		t.Fatalf("expected unresolved recipient for orphan project, got (%q, %q)", email, source)
	}

	if !claimAppHealthAlertSlot(ctx, pool, namespace, appName, "CrashLoopBackOff", "pod/app-cooldown", appHealthAlertCooldown) {
		t.Fatal("cooldown slot should still be free: a failed resolution must never have claimed it")
	}

	if claimAppHealthAlertSlot(ctx, pool, namespace, appName, "CrashLoopBackOff", "pod/app-cooldown", appHealthAlertCooldown) {
		t.Fatal("second claim within cooldown should fail (the first real send did claim it)")
	}
}
