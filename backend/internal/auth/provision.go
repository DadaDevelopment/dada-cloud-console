package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the subset of pgxpool.Pool used by ResolveUser, so tests can pass a
// real pool or a thin fake without depending on the concrete type.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ querier = (*pgxpool.Pool)(nil)

// ResolveUser maps a verified Keycloak identity to a local users.id, creating or
// updating the backing row as needed. It returns the local user id that all the
// actor_id / user_id foreign keys reference.
//
// Collision rule (chosen for least surprise, no crashes):
//  1. Primary path: upsert keyed on keycloak_sub. If a row already carries this
//     sub, its email/display_name are refreshed.
//  2. If that insert hits a username/email UNIQUE violation, a *legacy local*
//     user already owns that username or email (created before SSO). We link by
//     stamping keycloak_sub onto that existing row (and refreshing email/display
//     name), instead of failing. This makes the local account SSO-backed.
//
// password_hash is set to the empty string for OIDC-provisioned rows. The login handler's
// bcrypt compare against an empty hash always fails, so these rows can never log
// in via /auth/login — exactly the intent (login is via Keycloak in this mode).
// ResolveUser returns the resolved local user id and whether this call is the
// row's first-ever provisioning (a fresh INSERT, not a refresh of an existing
// row or a legacy-account link). Callers use the created flag to fire
// signup-only side effects exactly once.
func ResolveUser(ctx context.Context, db querier, kc *KeycloakClaims) (id uuid.UUID, created bool, err error) {
	if kc == nil || kc.Subject == "" {
		return uuid.Nil, false, fmt.Errorf("keycloak claims missing subject")
	}

	username := kc.PreferredUsername
	if username == "" {
		username = kc.Subject
	}
	email := kc.Email
	if email == "" {
		// users.email is NOT NULL UNIQUE; synthesize a stable, unique placeholder
		// from the sub so provisioning never violates the constraint.
		email = kc.Subject + "@keycloak.local"
	}
	displayName := kc.Name
	if displayName == "" {
		displayName = username
	}

	// xmax = 0 is the standard Postgres tell for "this RETURNING row came from the
	// INSERT branch of the upsert, not the ON CONFLICT UPDATE branch" — a fresh
	// row has no prior transaction ID in its xmax.
	const upsertBySub = `
		INSERT INTO users (keycloak_sub, username, email, display_name, password_hash)
		VALUES ($1, $2, $3, $4, '')
		ON CONFLICT (keycloak_sub) DO UPDATE
		    SET email = EXCLUDED.email,
		        display_name = EXCLUDED.display_name,
		        updated_at = now()
		RETURNING id, (xmax = 0) AS inserted`

	err = db.QueryRow(ctx, upsertBySub, kc.Subject, username, email, displayName).Scan(&id, &created)
	if err == nil {
		return id, created, nil
	}

	// A username/email UNIQUE collision means a legacy local row already exists.
	// Link it to this Keycloak identity instead of crashing. This is never a
	// fresh signup — the row predates this Keycloak identity.
	if isUniqueViolation(err) {
		const linkExisting = `
			UPDATE users
			    SET keycloak_sub = $1,
			        display_name = $2,
			        updated_at = now()
			WHERE username = $3 OR email = $4
			RETURNING id`
		if lerr := db.QueryRow(ctx, linkExisting, kc.Subject, displayName, username, email).Scan(&id); lerr != nil {
			return uuid.Nil, false, fmt.Errorf("link existing user to keycloak sub: %w", lerr)
		}
		return id, false, nil
	}

	return uuid.Nil, false, fmt.Errorf("resolve keycloak user: %w", err)
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), without importing pgconn into call sites.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps a *pgconn.PgError; matching on the SQLSTATE string keeps this
	// dependency-light and covers both the wrapped and string-rendered forms.
	type sqlStater interface{ SQLState() string }
	if pgErr, ok := err.(sqlStater); ok {
		return pgErr.SQLState() == "23505"
	}
	return strings.Contains(err.Error(), "23505")
}
