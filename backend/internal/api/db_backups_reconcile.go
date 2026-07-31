package api

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

const (
	backupReconcileInterval = 30 * time.Second

	volumeExportRetention   = 24 * time.Hour
	volumeExportSweepEvery  = 10 * time.Minute
	volumeExportSweepPrefix = "volexports/"
)

// maxConcurrentScheduledBackups caps how many scheduled backups may be
// Pending/Running at the same time, across every ServiceDatabaseV2 that
// opted in. postgresql-0 (namespace databases) runs under limits: cpu 500m /
// memory 1Gi and also hosts Keycloak, whose unavailability panics the
// console backend at startup. Ten opted-in databases share that one
// instance today, including a roughly 5.9 GB Keycloak dump; letting every
// due backup start in the same tick would launch that many concurrent
// pg_dump processes against a 500m CPU budget and could take Keycloak down
// with it. Keep this at 1 until postgresql-0 gets a dedicated
// backup-capacity budget; dueScheduledBackups still reaches every database
// over time, one at a time, longest-waiting first.
const maxConcurrentScheduledBackups = 1

// stuckBackupTimeout bounds how long a backup may sit in Pending/Running
// before failStuckBackups marks it Failed. reconcileBackups can only advance
// a row while its ActionSet is still readable: if the ActionSet is deleted or
// never recorded, Status returns an error and the row stays Running forever.
// With maxConcurrentScheduledBackups at 1, one such row would permanently
// consume the only slot and silently stop scheduled backups for every
// database. The bound is generous on purpose -- the largest opted-in dump is
// roughly 5.9 GB against a 500m CPU budget -- so a healthy backup finishes
// long before it, and a Failed row is recoverable (the next tick simply
// starts a new backup) whereas a stuck one is not.
const stuckBackupTimeout = 6 * time.Hour

// backupFrequencyIntervals maps a ServiceDatabaseV2's configured backup
// frequency to the cadence runScheduledBackups enforces. Keys use K10's
// @-prefixed form (see normalizeBackupFrequency in databases.go);
// backupIntervalForFrequency also accepts the bare word the console stores in
// some rows.
var backupFrequencyIntervals = map[string]time.Duration{
	"@hourly":  time.Hour,
	"@daily":   24 * time.Hour,
	"@weekly":  7 * 24 * time.Hour,
	"@monthly": 30 * 24 * time.Hour,
	"@yearly":  365 * 24 * time.Hour,
}

// backupIntervalForFrequency resolves the scheduled-backup cadence for a
// database's configured frequency. An unknown or absent frequency on an
// enabled database defaults to @daily: a database that advertises "backups
// enabled" must get backups on some cadence, never be silently skipped.
func backupIntervalForFrequency(freq string) time.Duration {
	f := strings.ToLower(strings.TrimSpace(freq))
	if f != "" && !strings.HasPrefix(f, "@") {
		f = "@" + f
	}
	if d, ok := backupFrequencyIntervals[f]; ok {
		return d
	}
	return backupFrequencyIntervals["@daily"]
}

// serviceDatabaseBackupFrequency pulls spec.backup.frequency from a
// ServiceDatabaseV2 snapshot's summary_json, or "" if absent/unparsable.
func serviceDatabaseBackupFrequency(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	spec, ok := summary["spec"].(map[string]any)
	if !ok {
		return ""
	}
	backup, ok := spec["backup"].(map[string]any)
	if !ok {
		return ""
	}
	freq, _ := backup["frequency"].(string)
	return freq
}

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
					h.failStuckBackups(ctx)
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

// failStuckBackups marks backups Failed once they have been Pending or
// Running for longer than stuckBackupTimeout. Such a row is unreachable for
// reconcileBackups -- its ActionSet is gone or was never recorded, so Status
// keeps erroring -- and it would otherwise hold a scheduled-backup slot
// forever.
func (h *Handler) failStuckBackups(ctx context.Context) {
	tag, err := h.pool.Exec(ctx,
		`UPDATE db_backups
		    SET status = 'Failed',
		        error_message = COALESCE(error_message, 'backup stuck without a readable ActionSet'),
		        updated_at = NOW()
		  WHERE status IN ('Pending', 'Running')
		    AND created_at < NOW() - $1::interval`,
		stuckBackupTimeout.String())
	if err != nil {
		log.Printf("failStuckBackups: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("failStuckBackups: marked %d stuck backup(s) Failed", n)
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

// scheduledBackupCandidate is one ServiceDatabaseV2 considered by
// runScheduledBackups on a given tick.
type scheduledBackupCandidate struct {
	projectID    uuid.UUID
	envID        uuid.UUID
	name         string
	database     string
	frequency    string
	lastBackupAt *time.Time
}

// dueScheduledBackups picks up to freeSlots candidates that are due for a
// scheduled backup (no backup yet, or the last one older than the
// candidate's configured frequency), longest-waiting first.
//
// Ordering matters once freeSlots is smaller than the due count: with
// maxConcurrentScheduledBackups capping concurrency to 1, a fixed or
// undefined iteration order would let whichever database happens to sort
// first in the query win every tick forever, starving the rest. Sorting by
// last_backup_at with NULLS FIRST (never-backed-up databases first), then
// by name to break ties deterministically, guarantees every candidate
// eventually reaches the front once the databases ahead of it get backed up.
func dueScheduledBackups(candidates []scheduledBackupCandidate, freeSlots int, now time.Time) []scheduledBackupCandidate {
	if freeSlots <= 0 {
		return nil
	}

	ordered := make([]scheduledBackupCandidate, len(candidates))
	copy(ordered, candidates)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].lastBackupAt, ordered[j].lastBackupAt
		switch {
		case a == nil && b == nil:
			return ordered[i].name < ordered[j].name
		case a == nil:
			return true
		case b == nil:
			return false
		case !a.Equal(*b):
			return a.Before(*b)
		default:
			return ordered[i].name < ordered[j].name
		}
	})

	var due []scheduledBackupCandidate
	for _, c := range ordered {
		if c.lastBackupAt != nil && now.Sub(*c.lastBackupAt) < backupIntervalForFrequency(c.frequency) {
			continue
		}
		due = append(due, c)
		if len(due) == freeSlots {
			break
		}
	}
	return due
}

// runScheduledBackups takes a scheduled backup for each ServiceDatabaseV2
// that has opted in (spec.backup.enabled) and whose most recent backup is
// older than its own configured frequency (or has none), up to
// maxConcurrentScheduledBackups minus whatever scheduled backups are already
// Pending/Running. Databases that never opted in are never touched here.
func (h *Handler) runScheduledBackups(ctx context.Context) {
	var inflight int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM db_backups WHERE status IN ('Pending', 'Running') AND kind = $1`,
		models.DBBackupKindScheduled,
	).Scan(&inflight); err != nil {
		return
	}
	freeSlots := maxConcurrentScheduledBackups - inflight
	if freeSlots <= 0 {
		return
	}

	rows, err := h.pool.Query(ctx,
		`SELECT rs.project_id, rs.environment_id, rs.name, rs.summary_json,
		        (SELECT MAX(b.created_at) FROM db_backups b
		         WHERE b.project_id = rs.project_id AND b.environment_id = rs.environment_id
		           AND b.resource_name = rs.name
		           AND b.status IN ('Pending', 'Running', 'Ready')) AS last_backup_at
		 FROM resource_snapshots rs
		 WHERE rs.kind = 'ServiceDatabaseV2'
		   AND rs.summary_json->'spec'->'backup'->>'enabled' = 'true'`)
	if err != nil {
		return
	}
	var candidates []scheduledBackupCandidate
	for rows.Next() {
		var c scheduledBackupCandidate
		var summary []byte
		if rows.Scan(&c.projectID, &c.envID, &c.name, &summary, &c.lastBackupAt) != nil {
			continue
		}
		c.database = serviceDatabaseName(summary)
		if c.database == "" {
			c.database = c.name
		}
		c.frequency = serviceDatabaseBackupFrequency(summary)
		candidates = append(candidates, c)
	}
	rows.Close()

	for _, c := range dueScheduledBackups(candidates, freeSlots, time.Now()) {
		if _, err := h.startDBBackup(ctx, c.projectID, c.envID, c.name, c.database, models.DBBackupKindScheduled, nil); err != nil {
			log.Printf("scheduled backup for %s failed to start: %v", c.name, err)
		}
	}
}

func nonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
