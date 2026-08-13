package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSignupClosed is returned by ResolveUser when allowSignup is false and the
// Keycloak identity does not match any existing local user (by keycloak_sub,
// username, or email). It carries no PII by itself — callers log the identity
// separately — so it is safe to compare with errors.Is across package
// boundaries.
var ErrSignupClosed = errors.New("signup is closed")

// querier is the subset of pgxpool.Pool used by ResolveUser, so tests can pass a
// real pool or a thin fake without depending on the concrete type.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ querier = (*pgxpool.Pool)(nil)

// SignupAttribution carries the first-touch acquisition channel a visitor
// arrived through, read from the four dada_src/dada_med/dada_cmp/dada_ref
// cookies the marketing site sets on first page load. It rides the same
// INSERT statement as the users row it describes (see upsertBySub below),
// following the same house rule that produced the SignUp audit row: a
// caller-side follow-up write can silently miss a caller, so the value must
// be born with the row instead. The ON CONFLICT branch of upsertBySub never
// touches these columns, so a returning identity keeps whichever channel
// brought it in the first time, never whatever it is carrying on a later
// visit.
type SignupAttribution struct {
	Source   string
	Medium   string
	Campaign string
	Referrer string
}

// sanitizeSignupAttribution trims whitespace, drops non-printable runes, and
// caps each field to the width of the column it will be written into (64
// runes for source/medium/campaign, 255 for the raw referrer URL). It never
// fails: a malformed or oversized attribution cookie is cleaned up rather
// than rejected, because a bad cookie value must never block a signup.
func sanitizeSignupAttribution(a SignupAttribution) SignupAttribution {
	return SignupAttribution{
		Source:   cleanAttributionField(a.Source, 64),
		Medium:   cleanAttributionField(a.Medium, 64),
		Campaign: cleanAttributionField(a.Campaign, 64),
		Referrer: cleanAttributionField(a.Referrer, 255),
	}
}

// cleanAttributionField strips non-printable runes, trims surrounding
// whitespace, and truncates the result to maxLen runes.
func cleanAttributionField(s string, maxLen int) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	runes := []rune(cleaned)
	if len(runes) > maxLen {
		runes = runes[:maxLen]
	}
	return string(runes)
}

// nullableAttr turns an empty attribution field into a SQL NULL instead of an
// empty string, so `signup_medium IS NULL` means "nothing was ever observed"
// rather than "an empty value was recorded".
func nullableAttr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

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
//
// allowSignup is the single gate for whether a brand-new identity may
// provision a row at all. It lives inside this function rather than in a
// wrapper because this is the only door both router.go call sites walk
// through — a wrapper is a door a third caller could simply not use. When
// allowSignup is false, an identity that resolves to neither an existing
// keycloak_sub row nor a legacy username/email row gets ErrSignupClosed
// instead of a fresh users row: no INSERT, no audit_events row, nothing to
// undo. An identity that already has a row — either path — keeps resolving
// and refreshing normally regardless of this flag; the gate only blocks birth,
// never blocks a return visit.
//
// attr is the first-touch attribution for a fresh signup (see
// SignupAttribution). It is only ever consulted on the INSERT branch of
// upsertBySub — a returning identity ignores whatever attr the caller passes,
// because its channel was already decided the first time it showed up.
func ResolveUser(ctx context.Context, db querier, kc *KeycloakClaims, allowSignup bool, attr SignupAttribution) (id uuid.UUID, created bool, err error) {
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

	if !allowSignup {
		return resolveExistingOnly(ctx, db, kc.Subject, username, email, displayName)
	}

	// xmax = 0 is the standard Postgres tell for "this RETURNING row came from the
	// INSERT branch of the upsert, not the ON CONFLICT UPDATE branch" — a fresh
	// row has no prior transaction ID in its xmax.
	//
	// The signup audit row is written by the same statement, not by the caller.
	// Two router paths reach this function — the main resolver
	// (api/router.go) and optionalAuthResolver, which discards `created` — so a
	// caller-side side effect leaves accounts born on the second path with a
	// users row and no trace anywhere: mytake@yandex.ru registered with zero
	// audit_events and zero ux_events, one of the two organic signups of that
	// fortnight. Attaching the row to the INSERT branch here makes that
	// unrepresentable for any future caller, and the FK to users(id) is
	// satisfied because foreign keys are checked after the statement completes.
	const upsertBySub = `
		WITH upserted AS (
		    INSERT INTO users (keycloak_sub, username, email, display_name, password_hash,
		        signup_source, signup_medium, signup_campaign, signup_referrer)
		    VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8)
		    ON CONFLICT (keycloak_sub) DO UPDATE
		        SET email = EXCLUDED.email,
		            display_name = EXCLUDED.display_name,
		            updated_at = now()
		    RETURNING id, (xmax = 0) AS inserted, email
		), signup_event AS (
		    INSERT INTO audit_events (actor_id, action, resource_kind, resource_name, outcome, metadata)
		    SELECT id, 'SignUp', 'User', email, 'success', jsonb_build_object(
		        'source', 'provision',
		        'signup_source', $5::text,
		        'signup_medium', $6::text,
		        'signup_campaign', $7::text,
		        'signup_referrer', $8::text
		    )
		    FROM upserted
		    WHERE inserted
		)
		SELECT id, inserted FROM upserted`

	cleanAttr := sanitizeSignupAttribution(attr)
	err = db.QueryRow(ctx, upsertBySub, kc.Subject, username, email, displayName,
		nullableAttr(cleanAttr.Source), nullableAttr(cleanAttr.Medium),
		nullableAttr(cleanAttr.Campaign), nullableAttr(cleanAttr.Referrer),
	).Scan(&id, &created)
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

// resolveExistingOnly is the allowSignup=false path: it may only resolve an
// identity to a row that already exists, never create one. It tries the same
// two matches ResolveUser's signup path would (by keycloak_sub, then by the
// legacy username/email match), just as plain UPDATEs instead of an
// INSERT ... ON CONFLICT — so neither branch can ever produce a fresh row or
// a signup_event. Exhausting both is ErrSignupClosed, logged once so a closed
// door someone is knocking on stays visible without a users row to query for.
func resolveExistingOnly(ctx context.Context, db querier, sub, username, email, displayName string) (id uuid.UUID, created bool, err error) {
	const refreshBySub = `
		UPDATE users
		    SET email = $2,
		        display_name = $3,
		        updated_at = now()
		WHERE keycloak_sub = $1
		RETURNING id`
	if err = db.QueryRow(ctx, refreshBySub, sub, email, displayName).Scan(&id); err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("refresh existing keycloak user: %w", err)
	}

	const linkExisting = `
		UPDATE users
		    SET keycloak_sub = $1,
		        display_name = $2,
		        updated_at = now()
		WHERE username = $3 OR email = $4
		RETURNING id`
	if err = db.QueryRow(ctx, linkExisting, sub, displayName, username, email).Scan(&id); err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("link existing user to keycloak sub: %w", err)
	}

	log.Printf("auth: signup closed, denying unknown identity keycloak_sub=%s email=%s", sub, email)
	return uuid.Nil, false, ErrSignupClosed
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
