package db

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
