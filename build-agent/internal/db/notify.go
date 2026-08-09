package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Recipient sources, stored on every SendBuildNotification row so the question
// "who did we pick, and why" is answerable from the journal alone. The names
// match the alert ladder in backend/internal/api/app_health_watcher.go: the two
// paths mail the same people about the same apps, and two vocabularies for one
// answer make the rows uncomparable.
const (
	RecipientSourceOwner       = "owner"
	RecipientSourceMember      = "member"
	RecipientSourcePersonalOrg = "personal-org"
)

// isKeycloakLocalEmail reports whether email is one of the synthetic addresses
// stamped on a Keycloak identity that carries no email claim
// (<sub>@keycloak.local). Such an address is non-empty but is not a mailbox:
// mailing it "succeeds" into the void and writes a success row that lies. Every
// rung rejects it and falls through to the next candidate.
func isKeycloakLocalEmail(email string) bool {
	return strings.HasSuffix(strings.ToLower(email), "@keycloak.local")
}

// OwnerEmail resolves the notification recipient for a build and reports which
// rung of the ladder produced it: projects.owner_id, then a project_members row
// with role Owner/Admin, then the personal-org convention (projects.org_id
// equals a user's username). Returns ("", "", nil) only when no rung yields a
// real mailbox, so callers can record "nobody was reachable" as a fact rather
// than infer it.
//
// It used to be the first rung alone. That is exactly the bug the backend alert
// path already fixed once (P1-ALERT-OWNERLESS-DROP): a project whose owner_id is
// NULL — a Keycloak-only identity with no users row, an adopted project, a
// project created through a path that never stamped an owner — resolved to the
// empty string, and the build-result email was dropped in silence. The person
// whose deploy had just finished was told nothing, and the journal recorded
// "no_recipient" as if the customer genuinely had no address.
func OwnerEmail(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (email, source string, err error) {
	byOwnerID, err := queryEmail(ctx, pool,
		`SELECT u.email
		   FROM projects p
		   JOIN users u ON u.id = p.owner_id
		  WHERE p.id = $1`, projectID)
	if err != nil {
		return "", "", err
	}
	if byOwnerID != "" {
		return byOwnerID, RecipientSourceOwner, nil
	}

	byMember, err := queryEmail(ctx, pool,
		`SELECT u.email
		   FROM project_members pm
		   JOIN users u ON u.id = pm.user_id
		  WHERE pm.project_id = $1 AND pm.role IN ('Owner', 'Admin')
		    AND u.email <> '' AND lower(u.email) NOT LIKE '%@keycloak.local'
		  ORDER BY CASE pm.role WHEN 'Owner' THEN 0 WHEN 'Admin' THEN 1 ELSE 2 END,
		           pm.created_at ASC
		  LIMIT 1`, projectID)
	if err != nil {
		return "", "", err
	}
	if byMember != "" {
		return byMember, RecipientSourceMember, nil
	}

	byOrgUsername, err := queryEmail(ctx, pool,
		`SELECT u.email
		   FROM projects p
		   JOIN users u ON u.username = p.org_id
		  WHERE p.id = $1`, projectID)
	if err != nil {
		return "", "", err
	}
	if byOrgUsername != "" {
		return byOrgUsername, RecipientSourcePersonalOrg, nil
	}
	return "", "", nil
}

// queryEmail runs one rung of the ladder. A missing row is not an error — it is
// this rung answering "not me"; only a real database failure is returned, so a
// broken pool is never silently reported to the operator as "this customer has
// no address".
func queryEmail(ctx context.Context, pool *pgxpool.Pool, sql string, projectID uuid.UUID) (string, error) {
	var email string
	err := pool.QueryRow(ctx, sql, projectID).Scan(&email)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if isKeycloakLocalEmail(email) {
		return "", nil
	}
	return email, nil
}

// ManagedHostname returns the app's current managed default hostname for an
// environment, or "" when none is attached (datastore apps, or the default
// domain was skipped). Used to put a clickable app URL in the success email.
func ManagedHostname(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, appName string) (string, error) {
	var host string
	err := pool.QueryRow(ctx,
		`SELECT hostname
		   FROM domain_hostnames
		  WHERE environment_id = $1 AND app_name = $2 AND managed = true
		  ORDER BY created_at DESC
		  LIMIT 1`,
		envID, appName,
	).Scan(&host)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return host, nil
}

// RecordBuildNotify writes the outcome of one build-result email into
// audit_events, so "was this owner told their build failed" becomes a query.
//
// Until this existed the answer lived only in the agent's log line, and the
// build-agent pod restarts far more often than the gap between two reads of it:
// asked in retrospect whether three specific users were mailed about their
// stuck builds, nobody could tell, because the logs were already gone.
//
// The recipient address is deliberately not stored -- the project id already
// identifies whose build it was, and an address in a telemetry table is
// personal data (152-FZ). What is stored instead is recipientSource: which rung
// of the resolver ladder produced the address. That is the field that separates
// "the owner was told" from "the owner was unreachable and we fell through to a
// project member", and it was missing here while the backend alert path had it,
// so build notifications were the one channel whose rows could not answer even
// "who did we pick". Status and, on a failure, the transport error complete it.
//
// Best-effort by contract: a failure here is logged and swallowed, exactly like
// the deploy audit row. Notification bookkeeping must never break a build.
func RecordBuildNotify(ctx context.Context, pool *pgxpool.Pool, projectID, envID, buildID uuid.UUID, appName, buildStatus, recipientSource, failReason string, detail error) {
	if pool == nil {
		return
	}
	outcome := "success"
	meta := map[string]any{
		"build_status":     buildStatus,
		"build_id":         buildID.String(),
		"recipient_source": recipientSource,
	}
	if failReason != "" {
		outcome = "failure"
		meta["reason"] = failReason
		if detail != nil {
			errText := detail.Error()
			if runes := []rune(errText); len(runes) > buildNotifyErrorMaxLen {
				errText = string(runes[:buildNotifyErrorMaxLen])
			}
			meta["detail"] = errText
		}
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		payload = []byte("{}")
	}
	var env any
	if envID != uuid.Nil {
		env = envID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata)
		VALUES ($1, $2, $3, 'SendBuildNotification', 'Notification', $4, $5, $6)
	`, SystemUserID, projectID, env, appName, outcome, payload); err != nil {
		log.Warn().Err(err).Str("app", appName).Msg("deploy-notify: audit row insert failed")
	}
}

// buildNotifyErrorMaxLen bounds the transport error kept in audit metadata. The
// cut is rune-safe: slicing bytes can split a multi-byte rune and hand Postgres
// invalid UTF-8, failing the very row meant to record the failure.
const buildNotifyErrorMaxLen = 300
