package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The SignUp audit row is written by the same statement that creates the users
// row (backend/internal/auth/provision.go), so it exists before the account has
// done anything at all. Every reader that treats an audit row as evidence of a
// product action must therefore exclude it, or registration alone would read as
// activity: classifyFirstVisit would call a genuinely first visit a "return",
// and /admin/overview would count a customer who has done nothing as active.
//
// These run against a real database because the guarantee lives in the SQL.
func signupNotActivityPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping signup-is-not-activity test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedSignupOnlyUser creates a user carrying nothing but its own SignUp row,
// and removes both when the test ends. Tests share the production database, so
// a skipped cleanup leaves a fake account in every funnel query.
func seedSignupOnlyUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (keycloak_sub, username, email, display_name, password_hash)
		 VALUES ($1, $2, $3, $2, '') RETURNING id`,
		"signup-activity-test-"+suffix,
		"signup-activity-"+suffix,
		"signup-activity-"+suffix+"@signup-activity.test",
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE actor_id = $1`, id); err != nil {
			t.Errorf("cleanup audit_events for %s: %v", id, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup user %s: %v", id, err)
		}
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_events (actor_id, action, resource_kind, resource_name, outcome, metadata)
		 VALUES ($1, 'SignUp', 'User', 'signup-activity-`+suffix+`@signup-activity.test', 'success',
		         jsonb_build_object('source', 'provision'))`,
		id,
	); err != nil {
		t.Fatalf("seed SignUp row: %v", err)
	}
	return id
}

// An account whose only history is its own registration is opening its first
// visit, not returning.
func TestClassifyFirstVisit_SignupRowIsNotPriorHistory(t *testing.T) {
	pool := signupNotActivityPool(t)
	id := seedSignupOnlyUser(t, pool)

	h := &Handler{pool: pool}
	if got := h.classifyFirstVisit(context.Background(), id, time.Now()); got != auditVisitFirst {
		t.Fatalf("visit = %q, want %q — the signup row was mistaken for earlier activity", got, auditVisitFirst)
	}
}

// Registering is not using the product: a customer with nothing but a SignUp
// row must not inflate the 48h active count on /admin/overview.
func TestOverviewUsers_SignupAloneIsNotActive(t *testing.T) {
	pool := signupNotActivityPool(t)
	ctx := context.Background()
	h := &Handler{pool: pool}

	before, err := h.overviewUsers(ctx)
	if err != nil {
		t.Fatalf("overviewUsers before: %v", err)
	}

	id := seedSignupOnlyUser(t, pool)

	after, err := h.overviewUsers(ctx)
	if err != nil {
		t.Fatalf("overviewUsers after: %v", err)
	}
	if after.Active48h != before.Active48h {
		t.Fatalf("Active48h moved %d -> %d after seeding user %s, whose only row is its own SignUp",
			before.Active48h, after.Active48h, id)
	}
}
