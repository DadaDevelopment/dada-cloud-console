package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UpsertSnapshot inserts or updates a resource_snapshots row.
func UpsertSnapshot(
	ctx context.Context,
	pool *pgxpool.Pool,
	projectID uuid.UUID,
	environmentID *uuid.UUID,
	kind, name, phase string,
	summaryJSON json.RawMessage,
	lastSyncedAt time.Time,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO resource_snapshots
		    (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project_id, environment_id, kind, name)
		DO UPDATE SET
		    phase = EXCLUDED.phase,
		    summary_json = EXCLUDED.summary_json,
		    last_synced_at = EXCLUDED.last_synced_at
	`, projectID, environmentID, kind, name, phase, summaryJSON, lastSyncedAt)
	return err
}

// UpdateLiveStatus mirrors live runtime state onto an EXISTING snapshot of the
// given kind: it sets phase and merges summaryPatch into summary_json (jsonb
// concat, so durable keys like summary_json.desired survive). It only touches
// rows that already exist, so it never resurrects a resource the console
// removed. Returns the number of rows updated (0 = no managing snapshot).
func UpdateLiveStatus(
	ctx context.Context,
	pool *pgxpool.Pool,
	environmentID uuid.UUID,
	kind, name, phase string,
	summaryPatch json.RawMessage,
) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET phase          = $1,
		    summary_json   = COALESCE(summary_json, '{}'::jsonb) || $2::jsonb,
		    last_synced_at = now()
		WHERE environment_id = $3 AND kind = $4 AND name = $5
	`, phase, summaryPatch, environmentID, kind, name)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PrimaryHostnameInfo is the address the console shows for an app, plus why it
// is not serving yet when it is not.
type PrimaryHostnameInfo struct {
	Hostname string
	Status   string
	Reason   string
}

// PrimaryHostname returns the address an app answers on, picking the same way
// the k8s status reconciler does: an active hostname beats a pending one, a
// tenant's own domain beats the platform surrogate, ties break oldest-first.
// Without this a published VM app carries no url in its snapshot, so the console
// shows a live site as an app with no address — the k8s side has had it since
// the beginning, and parity is the whole point of the VM publish path.
func PrimaryHostname(ctx context.Context, pool *pgxpool.Pool, environmentID uuid.UUID, appName string) (PrimaryHostnameInfo, error) {
	var hostname, status string
	var reason *string
	err := pool.QueryRow(ctx, `
		SELECT hostname, status, status_reason
		FROM domain_hostnames
		WHERE environment_id = $1 AND app_name = $2
		ORDER BY (status = 'active') DESC, managed ASC, created_at ASC
		LIMIT 1
	`, environmentID, appName).Scan(&hostname, &status, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrimaryHostnameInfo{}, nil
	}
	if err != nil {
		return PrimaryHostnameInfo{}, fmt.Errorf("primary hostname: %w", err)
	}
	info := PrimaryHostnameInfo{Hostname: hostname, Status: normalizeHostnameStatus(status)}
	if reason != nil {
		info.Reason = *reason
	}
	return info, nil
}

// normalizeHostnameStatus maps domain_hostnames.status onto the console's
// url_status contract, folding anything unrecognized into "unknown" instead of
// letting it leak through unmapped.
func normalizeHostnameStatus(status string) string {
	switch status {
	case "active", "pending", "failed":
		return status
	default:
		return "unknown"
	}
}

// GetSnapshotSummary returns summary_json for a snapshot.
func GetSnapshotSummary(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, environmentID *uuid.UUID, kind, name string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id=$1 AND environment_id=$2 AND kind=$3 AND name=$4`,
		projectID, environmentID, kind, name,
	).Scan(&raw)
	return raw, err
}
