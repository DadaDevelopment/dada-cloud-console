package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// These tests exercise the SIGNUP_ENABLED=false path against a real database
// (see provisionSignalPool in provision_signal_test.go): the guarantee is
// about what rows Postgres does and does not end up with, not about Go
// control flow.

func TestResolveUser_ClosedSignup_UnknownIdentityLeavesNoRows(t *testing.T) {
	pool := provisionSignalPool(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	sub := "signup-gate-unknown-" + suffix
	email := "signup-gate-unknown-" + suffix + "@dada-tuda.ru"
	kc := &KeycloakClaims{
		Subject:           sub,
		PreferredUsername: "signup-gate-unknown-" + suffix,
		Email:             email,
		Name:              "Signup Gate Unknown",
	}

	id, created, err := ResolveUser(ctx, pool, kc, false, SignupAttribution{})
	if !errors.Is(err, ErrSignupClosed) {
		t.Fatalf("err = %v, want ErrSignupClosed", err)
	}
	if created {
		t.Error("created must be false when signup is closed")
	}
	if id != uuid.Nil {
		t.Errorf("id = %v, want uuid.Nil", id)
	}

	var userCount int
	if qerr := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE keycloak_sub = $1`, sub).Scan(&userCount); qerr != nil {
		t.Fatalf("count users: %v", qerr)
	}
	if userCount != 0 {
		t.Errorf("users rows for %s = %d, want 0 — a closed-gate denial must never provision a row", sub, userCount)
	}

	var auditCount int
	if qerr := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE action = 'SignUp' AND resource_name = $1`,
		email,
	).Scan(&auditCount); qerr != nil {
		t.Fatalf("count audit_events: %v", qerr)
	}
	if auditCount != 0 {
		t.Errorf("SignUp audit rows for %s = %d, want 0", email, auditCount)
	}
}

func TestResolveUser_ClosedSignup_ExistingUserStillResolves(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	id, created, err := ResolveUser(ctx, pool, kc, true, SignupAttribution{})
	if err != nil || !created {
		t.Fatalf("seed ResolveUser: id=%v created=%v err=%v", id, created, err)
	}

	kc.Email = "updated-" + kc.Email
	kc.Name = "Updated Display Name"

	gotID, createdAgain, err := ResolveUser(ctx, pool, kc, false, SignupAttribution{})
	if err != nil {
		t.Fatalf("ResolveUser under closed signup: %v", err)
	}
	if gotID != id {
		t.Fatalf("id = %v, want the same existing user %v", gotID, id)
	}
	if createdAgain {
		t.Error("resolving an existing user under closed signup must not report created=true")
	}

	var email, displayName string
	if qerr := pool.QueryRow(ctx, `SELECT email, display_name FROM users WHERE id = $1`, id).Scan(&email, &displayName); qerr != nil {
		t.Fatalf("read updated user: %v", qerr)
	}
	if email != kc.Email || displayName != kc.Name {
		t.Errorf("user row = (%q, %q), want (%q, %q) — closed signup must still refresh an existing user",
			email, displayName, kc.Email, kc.Name)
	}
}

func TestResolveUser_ClosedSignup_LegacyRowLinks(t *testing.T) {
	pool := provisionSignalPool(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	username := "signup-gate-legacy-" + suffix
	email := "signup-gate-legacy-" + suffix + "@dada-tuda.ru"

	var legacyID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, display_name, password_hash)
		 VALUES ($1, $2, $3, '') RETURNING id`,
		username, email, "Legacy Row",
	).Scan(&legacyID); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_id = $1`, legacyID)
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, legacyID)
	})

	sub := "signup-gate-legacy-sub-" + suffix
	kc := &KeycloakClaims{
		Subject:           sub,
		PreferredUsername: username,
		Email:             email,
		Name:              "Legacy Row Linked",
	}

	id, created, err := ResolveUser(ctx, pool, kc, false, SignupAttribution{})
	if err != nil {
		t.Fatalf("ResolveUser linking legacy row: %v", err)
	}
	if id != legacyID {
		t.Fatalf("id = %v, want the legacy row %v", id, legacyID)
	}
	if created {
		t.Error("linking a legacy row under closed signup must not report created=true")
	}

	var gotSub string
	if qerr := pool.QueryRow(ctx, `SELECT keycloak_sub FROM users WHERE id = $1`, legacyID).Scan(&gotSub); qerr != nil {
		t.Fatalf("read linked user: %v", qerr)
	}
	if gotSub != sub {
		t.Errorf("keycloak_sub = %q, want %q", gotSub, sub)
	}
}
