package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/models"
)

// recordMovePlacement writes the shard a finished move landed on back into the
// database CR, through a SetDatabaseShard operation the gitops-agent applies.
//
// Without it the only trace of a completed move is the db_moves override the
// router reads, and that override is a cutover primitive: the CR would keep
// naming the shard the data left, so the composition points Kasten backups and
// the admin ProviderConfig at an instance that no longer holds the database.
//
// It never fails a move. The data is already on the target and clients are
// already routed there by the time this runs; a CR that lags behind is worth a
// log line and a retry on the next move, not a rollback of a cutover that
// succeeded.
func (h *Handler) recordMovePlacement(ctx context.Context, datname, shard string) {
	var (
		projectID uuid.UUID
		envID     uuid.UUID
		name      string
		appRef    string
	)
	err := h.pool.QueryRow(ctx,
		`SELECT project_id, environment_id, name, COALESCE(summary_json->'spec'->>'appRef', '')
		 FROM resource_snapshots
		 WHERE kind = 'ServiceDatabaseV2' AND summary_json->'spec'->>'database' = $1
		 ORDER BY updated_at DESC
		 LIMIT 1`, datname).Scan(&projectID, &envID, &name, &appRef)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("db-move: %s has no ServiceDatabaseV2 snapshot, its CR keeps the old shard", datname)
		return
	}
	if err != nil {
		log.Printf("db-move: look up the CR for %s: %v", datname, err)
		return
	}

	payload, err := json.Marshal(models.SetDatabaseShardPayload{Name: name, AppRef: appRef, Shard: shard})
	if err != nil {
		log.Printf("db-move: encode shard payload for %s: %v", datname, err)
		return
	}
	var opID uuid.UUID
	err = h.pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 SELECT $1, $2, $3, 'SetDatabaseShard', 'ServiceDatabaseV2', $4, 'Created', $5
		 WHERE NOT EXISTS (
		   SELECT 1 FROM operations
		   WHERE environment_id = $3 AND resource_kind = 'ServiceDatabaseV2' AND resource_name = $4
		     AND action = 'SetDatabaseShard' AND status IN ('Created', 'Reconciling')
		 )
		 RETURNING id`,
		systemDeployActorID, projectID, envID, name, payload,
	).Scan(&opID)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		log.Printf("db-move: enqueue shard patch for %s: %v", datname, err)
		return
	}
	log.Printf("db-move: %s CR follows the data to %s op=%s", datname, shard, opID)
}
