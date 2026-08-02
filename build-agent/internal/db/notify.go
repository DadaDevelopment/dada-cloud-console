package db

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// OwnerEmail resolves the notification recipient for a build: the email of the
// user who owns the build's project (projects.owner_id -> users.email). This
// join is exact for personal-org projects — verified live 2026-07-15 that all
// external signups resolve to a real address. Returns ("", nil) when the owner
// has no email or the row is missing, so callers treat a missing recipient as
// "skip notification", not an error.
func OwnerEmail(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (string, error) {
	var email string
	err := pool.QueryRow(ctx,
		`SELECT u.email
		   FROM projects p
		   JOIN users u ON u.id = p.owner_id
		  WHERE p.id = $1`,
		projectID,
	).Scan(&email)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
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
// personal data (152-FZ). Status and, on a failure, the transport error are
// what make the row worth reading.
//
// Best-effort by contract: a failure here is logged and swallowed, exactly like
// the deploy audit row. Notification bookkeeping must never break a build.
func RecordBuildNotify(ctx context.Context, pool *pgxpool.Pool, projectID, envID, buildID uuid.UUID, appName, buildStatus, failReason string, detail error) {
	if pool == nil {
		return
	}
	outcome := "success"
	meta := map[string]any{"build_status": buildStatus, "build_id": buildID.String()}
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
