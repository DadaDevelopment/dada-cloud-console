package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
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
