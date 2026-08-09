package auth

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A signup that leaves no row anywhere is a signup nobody can measure.
// mytake@yandex.ru arrived with a users row, a real keycloak_sub, zero
// audit_events and zero ux_events — one of the two organic registrations of
// that fortnight, invisible to every funnel query. The cause was that the
// signup side effect lived in one of the two callers of ResolveUser, and the
// other one (optionalAuthResolver, backend/internal/api/router.go:117) throws
// the created flag away.
//
// These tests run against a real database because the guarantee is a property
// of the SQL statement, not of Go: the audit row must be born with the users
// row, and must not appear again when the same identity comes back.
func provisionSignalPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping signup-signal integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// throwawayClaims builds claims for an identity that has never existed, and
// removes every row it causes when the test ends. Tests share the production
// database, so a cleanup that is skipped leaves a fake signup in the funnel.
func throwawayClaims(t *testing.T, pool *pgxpool.Pool) *KeycloakClaims {
	t.Helper()
	suffix := uuid.NewString()[:8]
	sub := "provision-signal-test-" + suffix
	t.Cleanup(func() {
		ctx := context.Background()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE keycloak_sub = $1`, sub).Scan(&id); err != nil {
			return
		}
		if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE actor_id = $1`, id); err != nil {
			t.Errorf("cleanup audit_events for %s: %v", sub, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup user %s: %v", sub, err)
		}
	})
	return &KeycloakClaims{
		Subject:           sub,
		PreferredUsername: "provision-signal-" + suffix,
		Email:             "provision-signal-" + suffix + "@dada-tuda.ru",
		Name:              "Provision Signal Test",
	}
}

func signupRowCount(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE actor_id = $1 AND action = 'SignUp'`,
		actorID,
	).Scan(&n); err != nil {
		t.Fatalf("count SignUp rows: %v", err)
	}
	return n
}

// The first provisioning of an identity leaves a trace, whichever caller
// happened to trigger it — the row is written by the same statement that
// creates the user, so no caller can opt out of it.
func TestResolveUser_FreshSignupLeavesAuditRow(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)

	id, created, err := ResolveUser(context.Background(), pool, kc)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if !created {
		t.Fatalf("created = false for an identity that has never been provisioned")
	}

	if got := signupRowCount(t, pool, id); got != 1 {
		t.Fatalf("SignUp audit rows = %d, want 1 — the account was born invisible to every funnel query", got)
	}

	var kind, name, outcome, source string
	if err := pool.QueryRow(context.Background(),
		`SELECT resource_kind, resource_name, outcome, metadata->>'source'
		   FROM audit_events WHERE actor_id = $1 AND action = 'SignUp'`,
		id,
	).Scan(&kind, &name, &outcome, &source); err != nil {
		t.Fatalf("read SignUp row: %v", err)
	}
	if kind != "User" || name != kc.Email || outcome != "success" || source != "provision" {
		t.Errorf("SignUp row = (%q, %q, %q, %q), want (\"User\", %q, \"success\", \"provision\")",
			kind, name, outcome, source, kc.Email)
	}
}

// A returning identity is not a new signup. Without this the row would be
// written on every request and the registration count would follow traffic
// instead of people.
func TestResolveUser_ReturningIdentityWritesNoSecondRow(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	id, created, err := ResolveUser(ctx, pool, kc)
	if err != nil || !created {
		t.Fatalf("first ResolveUser: id=%v created=%v err=%v", id, created, err)
	}

	againID, createdAgain, err := ResolveUser(ctx, pool, kc)
	if err != nil {
		t.Fatalf("second ResolveUser: %v", err)
	}
	if againID != id {
		t.Fatalf("second call resolved to %v, want the same user %v", againID, id)
	}
	if createdAgain {
		t.Errorf("created = true on the second call for an identity that already exists")
	}
	if got := signupRowCount(t, pool, id); got != 1 {
		t.Errorf("SignUp audit rows after two calls = %d, want 1", got)
	}
}
