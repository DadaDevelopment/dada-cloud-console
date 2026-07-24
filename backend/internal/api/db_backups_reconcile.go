package api

import (
	"context"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

const (
	backupReconcileInterval = 30 * time.Second
	scheduledBackupEvery    = 24 * time.Hour

	volumeExportRetention   = 24 * time.Hour
	volumeExportSweepEvery  = 10 * time.Minute
	volumeExportSweepPrefix = "volexports/"
)

// lastVolumeExportSweep throttles sweepVolumeExports to at most one run per
// volumeExportSweepEvery. It is process-local (not shared across replicas):
// the advisory lock already serializes each tick within a pod, but the sweep
// itself is idempotent, so each pod sweeping independently every 10 minutes
// is harmless.
var lastVolumeExportSweep time.Time

// StartBackupReconciler launches the background loop that advances in-flight
// backup/restore ActionSets, enforces retention, and (opt-in) takes scheduled
// backups. It is a no-op when Kanister access is not configured (off-cluster),
// so tests and local dev never spawn it. Call once from main after NewHandler.
func (h *Handler) StartBackupReconciler(ctx context.Context) {
	if h.kanister == nil || !h.kanister.Enabled() {
		return
	}
	go func() {
		ticker := time.NewTicker(backupReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyBackupReconcile, "backup-reconcile", func(ctx context.Context) {
					h.reconcileBackups(ctx)
					h.reconcileRestores(ctx)
					h.expireBackups(ctx)
					h.sweepVolumeExports(ctx)
					if h.cfg.DBBackupScheduleEnabled {
						h.runScheduledBackups(ctx)
					}
				})
			}
		}
	}()
}

// reconcileBackups advances Pending/Running backups by reading their ActionSet.
func (h *Handler) reconcileBackups(ctx context.Context) {
	rows, err := h.pool.Query(ctx,
		`SELECT id, action_set FROM db_backups
		 WHERE status IN ('Pending', 'Running') AND action_set IS NOT NULL`)
	if err != nil {
		return
	}
	type item struct {
		id uuid.UUID
		as string
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.as) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		st, err := h.kanister.Status(ctx, h.cfg.DBBackupNamespace, it.as)
		if err != nil {
			continue
		}
		switch st.State {
		case cloudtask.KanisterComplete:
			_, _ = h.pool.Exec(ctx,
				`UPDATE db_backups SET status = 'Ready', updated_at = NOW() WHERE id = $1`, it.id)
		case cloudtask.KanisterFailed:
			_, _ = h.pool.Exec(ctx,
				`UPDATE db_backups SET status = 'Failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
				it.id, nonEmpty(st.Error, "backup ActionSet failed"))
		}
	}
}

// reconcileRestores advances non-terminal RestoreServiceDatabase operations by
// reading the ActionSet labelled with the operation id.
func (h *Handler) reconcileRestores(ctx context.Context) {
	rows, err := h.pool.Query(ctx,
		`SELECT id FROM operations
		 WHERE action = 'RestoreServiceDatabase' AND status NOT IN ('Ready', 'Failed', 'Cancelled')`)
	if err != nil {
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		st, found, err := h.kanister.StatusByLabel(ctx, h.cfg.DBBackupNamespace, "dada.io/operation-id", id.String())
		if err != nil || !found {
			continue
		}
		switch st.State {
		case cloudtask.KanisterComplete:
			_, _ = h.pool.Exec(ctx,
				`UPDATE operations SET status = 'Ready', updated_at = NOW() WHERE id = $1`, id)
		case cloudtask.KanisterFailed:
			_, _ = h.pool.Exec(ctx,
				`UPDATE operations SET status = 'Failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
				id, nonEmpty(st.Error, "restore ActionSet failed"))
		}
	}
}

// expireBackups deletes the S3 artifacts of Ready backups past their retention
// window and marks the rows Deleted (best-effort — a failed delete just leaves
// the object, retried next pass since the row stays Ready until issued).
func (h *Handler) expireBackups(ctx context.Context) {
	rows, err := h.pool.Query(ctx,
		`SELECT id, dump_path FROM db_backups
		 WHERE status = 'Ready' AND expires_at IS NOT NULL AND expires_at < NOW()
		 LIMIT 20`)
	if err != nil {
		return
	}
	type item struct {
		id       uuid.UUID
		dumpPath string
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.dumpPath) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		_, err := h.kanister.CreateDelete(ctx, cloudtask.KanisterActionSpec{
			Namespace:   h.cfg.DBBackupNamespace,
			StatefulSet: h.cfg.DBBackupStatefulSet,
			Profile:     h.cfg.DBBackupProfile,
			Blueprint:   h.cfg.DBBackupBlueprint,
			DumpPath:    it.dumpPath,
		})
		if err != nil {
			continue
		}
		_, _ = h.pool.Exec(ctx,
			`UPDATE db_backups SET status = 'Deleted', updated_at = NOW() WHERE id = $1`, it.id)
	}
}

// sweepVolumeExports deletes volume-export tarballs older than
// volumeExportRetention from the dump bucket. Volume exports (unlike DB
// backups) have no row to track expiry against, so this walks the S3 prefix
// directly instead of reading from Postgres. Throttled to at most once per
// volumeExportSweepEvery per pod; a no-op when the presigner is disabled.
func (h *Handler) sweepVolumeExports(ctx context.Context) {
	if !h.dbBackupPresigner.Enabled() {
		return
	}
	if !lastVolumeExportSweep.IsZero() && time.Since(lastVolumeExportSweep) < volumeExportSweepEvery {
		return
	}
	lastVolumeExportSweep = time.Now()

	deleted, err := h.dbBackupPresigner.DeleteOldObjects(ctx, volumeExportSweepPrefix, volumeExportRetention)
	if err != nil {
		log.Printf("volume-export sweep: %v (deleted %d before error)", err, deleted)
		return
	}
	if deleted > 0 {
		log.Printf("volume-export sweep: deleted %d expired object(s)", deleted)
	}
}

// runScheduledBackups takes a scheduled backup for each managed database whose
// most recent backup is older than the schedule interval (or has none).
func (h *Handler) runScheduledBackups(ctx context.Context) {
	rows, err := h.pool.Query(ctx,
		`SELECT rs.project_id, rs.environment_id, rs.name, rs.summary_json
		 FROM resource_snapshots rs
		 WHERE rs.kind = 'ServiceDatabaseV2'
		   AND NOT EXISTS (
		     SELECT 1 FROM db_backups b
		     WHERE b.project_id = rs.project_id AND b.environment_id = rs.environment_id
		       AND b.resource_name = rs.name
		       AND b.status IN ('Pending', 'Running', 'Ready')
		       AND b.created_at > NOW() - INTERVAL '24 hours'
		   )`)
	if err != nil {
		return
	}
	type item struct {
		projectID uuid.UUID
		envID     uuid.UUID
		name      string
		database  string
	}
	var items []item
	for rows.Next() {
		var it item
		var summary []byte
		if rows.Scan(&it.projectID, &it.envID, &it.name, &summary) != nil {
			continue
		}
		it.database = serviceDatabaseName(summary)
		if it.database == "" {
			it.database = it.name
		}
		items = append(items, it)
	}
	rows.Close()

	for _, it := range items {
		if _, err := h.startDBBackup(ctx, it.projectID, it.envID, it.name, it.database, models.DBBackupKindScheduled, nil); err != nil {
			log.Printf("scheduled backup for %s failed to start: %v", it.name, err)
		}
	}
	_ = scheduledBackupEvery
}

func nonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
