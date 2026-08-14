package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// dbArchiveTickInterval is the poll period of the archive driver. Like the move
// worker it spends most of its life waiting on a Job, so polling faster buys
// nothing; unlike the move worker it has no cutover to time.
const dbArchiveTickInterval = 30 * time.Second

// dbArchiveNamespace is where the export, verify and repack Jobs run: the
// namespace the shards live in, so a Job reaches them without crossing the
// NetworkPolicy that only admits the namespace's own pods.
const dbArchiveNamespace = "databases"

// dbArchiveBucketResource is the console resource name of the environment's
// archive sink. One bucket per environment holds every archived table, keyed by
// database and table, so a tenant reads all of their history through a single
// set of credentials they already own.
const dbArchiveBucketResource = "dada-archive"

// dbArchiveDeleteBatch is how many rows one DELETE statement takes. Deleting a
// hundred million rows in one statement holds a single transaction open for the
// whole run, bloats the table with dead tuples faster than the archive frees
// space, and blocks the tenant's own writes on locks. Batches keep each
// statement short and let the worker stop between them.
const dbArchiveDeleteBatch = 20_000

// dbArchiveDeleteBudget bounds how long one tick spends deleting. The rest
// resumes on the next tick, which is what makes a rolling console pod harmless
// mid-delete.
const dbArchiveDeleteBudget = 4 * time.Minute

// dbArchiveRepackHeadroom is the free space pg_repack needs relative to the
// table it rewrites. pg_repack builds a full copy of the table and its indexes
// before swapping, so a shard with less headroom than the table's own size runs
// out of disk mid-rewrite -- on the exact volume the archive was called in to
// relieve. The margin above 1.0 covers the WAL the rewrite generates.
const dbArchiveRepackHeadroom = 1.3

// Archive phases. The order is the safety property: nothing is deleted before
// verify has passed, and verify is a separate phase precisely so that the
// delete can never be the step that discovers the export was short.
const (
	dbArchivePending = "pending"
	dbArchiveSink    = "sink"
	dbArchiveExport  = "export"
	dbArchiveVerify  = "verify"
	dbArchiveDelete  = "delete"
	dbArchiveRepack  = "repack"
	dbArchiveDone    = "done"
	dbArchiveFailed  = "failed"
)

// archiveRun is one archive of one table as the worker needs it.
type archiveRun struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	ResourceName  string
	Datname       string
	Shard         string
	Schema        string
	Table         string
	CutoffColumn  string
	Cutoff        time.Time
	Phase         string
	PlannedRows   int64
	ExportedRows  int64
	DeletedRows   int64
	BytesEstimate int64
	Bucket        string
	S3URI         string
	Auto          bool
}

// archiveJobs runs one Kubernetes Job per phase and reports whether it has
// finished. An interface because the phase machine has to be testable without a
// cluster.
//
// Ensure creates the named Job if it does not exist and reports whether it has
// succeeded. A Job that failed comes back as an error naming it, so the run
// stops with a pointer at the pod logs that explain why.
type archiveJobs interface {
	Ensure(ctx context.Context, name string, build func() *batchv1.Job) (bool, error)
}

// dbArchiveWorker drives every unfinished archive one step per tick.
type dbArchiveWorker struct {
	h    *Handler
	jobs archiveJobs
}

// StartDBArchiveWorker launches the archive driver. It needs shard admin
// credentials to plan and delete, and S3 credentials to export; without either
// there is no step it could take, so it does not start.
func (h *Handler) StartDBArchiveWorker(ctx context.Context) {
	if h.pool == nil || h.cfg == nil || h.s3creds == nil {
		return
	}
	if len(parseShardAdminDSNs(h.cfg.DBShardAdminDSNs)) == 0 {
		return
	}
	jobs := newClusterArchiveJobs()
	if jobs == nil {
		log.Printf("db-archive: not started, no in-cluster access to run export jobs")
		return
	}
	w := &dbArchiveWorker{h: h, jobs: jobs}
	log.Printf("db-archive: worker started interval=%s namespace=%s", dbArchiveTickInterval, dbArchiveNamespace)
	go func() {
		t := time.NewTicker(dbArchiveTickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBArchiveDrive, "db-archive", w.tick)
			}
		}
	}()
}

// tick advances every unfinished run. One failing run must not stall the
// others, so a step's error is recorded on its own row and the loop continues.
func (w *dbArchiveWorker) tick(ctx context.Context) {
	runs, err := w.openRuns(ctx)
	if err != nil {
		log.Printf("db-archive: read runs: %v", err)
		return
	}
	for _, r := range runs {
		if err := w.step(ctx, r); err != nil {
			log.Printf("db-archive: %s.%s in %s failed in phase %s: %v",
				r.Schema, r.Table, r.Datname, r.Phase, err)
			w.fail(ctx, r, err)
		}
	}
}

// openRuns reads every archive that has not reached a terminal phase.
func (w *dbArchiveWorker) openRuns(ctx context.Context) ([]archiveRun, error) {
	rows, err := w.h.pool.Query(ctx, `
		SELECT id, project_id, environment_id, resource_name, datname, shard,
		       schema_name, table_name, cutoff_column, cutoff_date, phase,
		       planned_rows, exported_rows, deleted_rows, bytes_estimate, bucket, s3_uri, auto
		  FROM db_archive_runs
		 WHERE phase NOT IN ('done', 'failed')
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []archiveRun
	for rows.Next() {
		var r archiveRun
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.EnvironmentID, &r.ResourceName, &r.Datname,
			&r.Shard, &r.Schema, &r.Table, &r.CutoffColumn, &r.Cutoff, &r.Phase,
			&r.PlannedRows, &r.ExportedRows, &r.DeletedRows, &r.BytesEstimate,
			&r.Bucket, &r.S3URI, &r.Auto); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// fail records why a run stopped. Nothing in the failure path deletes: a run
// that dies at any phase leaves the table exactly as it found it, and rows it
// had already exported stay on S3 as a harmless duplicate of data that is still
// in the database.
func (w *dbArchiveWorker) fail(ctx context.Context, r archiveRun, cause error) {
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET phase = 'failed', error = $2, finished_at = NOW(), updated_at = NOW()
		  WHERE id = $1`, r.ID, cause.Error()); err != nil {
		log.Printf("db-archive: record failure for %s: %v", r.ID, err)
	}
}

// setPhase persists the step the run has reached.
func (w *dbArchiveWorker) setPhase(ctx context.Context, r archiveRun, phase string) error {
	_, err := w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET phase = $2, error = '', updated_at = NOW() WHERE id = $1`, r.ID, phase)
	return err
}

// step runs the one action the run's current phase calls for. Each phase ends
// by persisting the next one, so a console that dies between steps resumes
// where it was.
func (w *dbArchiveWorker) step(ctx context.Context, r archiveRun) error {
	switch r.Phase {
	case dbArchivePending:
		return w.plan(ctx, r)
	case dbArchiveSink:
		return w.sink(ctx, r)
	case dbArchiveExport:
		return w.export(ctx, r)
	case dbArchiveVerify:
		return w.verify(ctx, r)
	case dbArchiveDelete:
		return w.deleteRows(ctx, r)
	case dbArchiveRepack:
		return w.repack(ctx, r)
	default:
		return fmt.Errorf("unknown phase %q", r.Phase)
	}
}

// plan measures exactly what the run will take, on the tenant instance, at the
// moment the work starts rather than when the button was pressed.
//
// The count is exact and is the number the verify phase holds the export to.
// Rows written after this point are newer than the cutoff by definition, so a
// table that keeps taking inserts does not invalidate it.
func (w *dbArchiveWorker) plan(ctx context.Context, r archiveRun) error {
	conn, err := w.h.connectToTenantDB(ctx, r.Shard, r.Datname)
	if err != nil {
		return fmt.Errorf("connect to %s on %s: %w", r.Datname, r.Shard, err)
	}
	defer conn.Close(context.Background())

	qualified := pgx.Identifier{r.Schema, r.Table}.Sanitize()
	var totalRows, totalBytes int64
	if err := conn.QueryRow(ctx,
		`SELECT GREATEST(c.reltuples, 0)::bigint, pg_total_relation_size(c.oid)
		   FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')`,
		r.Schema, r.Table).Scan(&totalRows, &totalBytes); err != nil {
		return fmt.Errorf("table %s.%s is gone: %w", r.Schema, r.Table, err)
	}

	cols, err := archiveColumnsOf(ctx, conn, r.Schema, r.Table)
	if err != nil {
		return fmt.Errorf("read columns of %s.%s: %w", r.Schema, r.Table, err)
	}
	if !archiveColumnUsable(cols, r.CutoffColumn) {
		return fmt.Errorf("column %q is not a timestamp or date column on %s.%s",
			r.CutoffColumn, r.Schema, r.Table)
	}

	rows, err := archiveRowsBefore(ctx, conn, qualified, r.CutoffColumn, r.Cutoff)
	if err != nil {
		return fmt.Errorf("count rows before the cutoff: %w", err)
	}
	bytes := estimateArchiveBytes(rows, totalRows, totalBytes)

	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET planned_rows = $2, bytes_estimate = $3, updated_at = NOW() WHERE id = $1`,
		r.ID, rows, bytes); err != nil {
		return err
	}
	if rows == 0 {
		r.PlannedRows = 0
		return w.finish(ctx, r, 0, map[string]any{"reason": "no rows older than the cutoff"})
	}
	r.PlannedRows = rows
	return w.setPhase(ctx, r, dbArchiveSink)
}

// sink makes sure the environment has an archive bucket with usable
// credentials, ordering one the first time a tenant archives anything.
//
// The run waits here rather than failing while the bucket provisions: bucket
// creation is an asynchronous operation through the same queue every other
// resource uses, and it takes minutes.
func (w *dbArchiveWorker) sink(ctx context.Context, r archiveRun) error {
	var exists int
	if err := w.h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM resource_snapshots
		  WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		r.ProjectID, r.EnvironmentID, dbArchiveBucketResource).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return w.orderArchiveBucket(ctx, r)
	}
	creds, err := w.h.s3creds.Resolve(ctx, dbArchiveBucketResource)
	if err != nil {
		if isS3CredentialsPending(err) {
			return nil
		}
		return fmt.Errorf("read archive bucket credentials: %w", err)
	}
	uri := archiveObjectURI(creds.BucketName, r)
	_, err = w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET bucket = $2, s3_uri = $3, phase = 'export', error = '', updated_at = NOW()
		  WHERE id = $1`, r.ID, creds.BucketName, uri)
	return err
}

// orderArchiveBucket enqueues the one CreateS3Bucket the environment needs, and
// only that one: a run that ticks every 30 seconds must not queue a bucket per
// tick while the first is still provisioning.
func (w *dbArchiveWorker) orderArchiveBucket(ctx context.Context, r archiveRun) error {
	payload, err := json.Marshal(map[string]any{
		"name":        dbArchiveBucketResource,
		"bucket_name": archiveBucketName(r.ProjectID),
		"region":      "ru1",
		"description": "DADA database archive",
	})
	if err != nil {
		return err
	}
	tag, err := w.h.pool.Exec(ctx, `
		INSERT INTO operations (project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT $1, $2, 'CreateS3Bucket', 'S3Bucket', $3, 'Created', $4
		 WHERE NOT EXISTS (
		     SELECT 1 FROM operations
		      WHERE project_id = $1 AND environment_id = $2
		        AND action = 'CreateS3Bucket' AND resource_name = $3
		        AND status IN ('Created', 'Reconciling')
		 )`, r.ProjectID, r.EnvironmentID, dbArchiveBucketResource, payload)
	if err != nil {
		return fmt.Errorf("order the archive bucket: %w", err)
	}
	if tag.RowsAffected() > 0 {
		log.Printf("db-archive: ordered archive bucket for project %s", r.ProjectID)
	}
	return nil
}

// export writes the rows older than the cutoff to Parquet on S3.
//
// It writes nothing back to the database: an export that dies halfway leaves a
// partial object that the verify phase rejects and the next export overwrites.
func (w *dbArchiveWorker) export(ctx context.Context, r archiveRun) error {
	creds, conn, err := w.jobInputs(ctx, r)
	if err != nil {
		return err
	}
	name := archiveJobName(r.ID, dbArchiveExport)
	done, err := w.jobs.Ensure(ctx, name, func() *batchv1.Job {
		return archiveExportJob(name, r, creds, conn, w.exportImage())
	})
	if err != nil || !done {
		return err
	}
	return w.setPhase(ctx, r, dbArchiveVerify)
}

// verify reads the archive back and refuses to continue unless it holds exactly
// the rows that were counted and nothing newer than the cutoff.
//
// This is the step that earns the right to delete. It is a separate Job reading
// the object from S3 rather than a flag the exporter sets, because the question
// is not "did the export command return zero" but "is the data actually there,
// readable, and the right data".
func (w *dbArchiveWorker) verify(ctx context.Context, r archiveRun) error {
	creds, _, err := w.jobInputs(ctx, r)
	if err != nil {
		return err
	}
	name := archiveJobName(r.ID, dbArchiveVerify)
	done, err := w.jobs.Ensure(ctx, name, func() *batchv1.Job {
		return archiveVerifyJob(name, r, creds, w.exportImage())
	})
	if err != nil || !done {
		return err
	}
	_, err = w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET exported_rows = planned_rows, phase = 'delete', error = '', updated_at = NOW()
		  WHERE id = $1`, r.ID)
	return err
}

// deleteRows removes what the archive now holds, in batches, within a time
// budget. Rows are matched by the same predicate the export used, so a row
// written after the plan is neither exported nor deleted.
func (w *dbArchiveWorker) deleteRows(ctx context.Context, r archiveRun) error {
	conn, err := w.h.connectToTenantDB(ctx, r.Shard, r.Datname)
	if err != nil {
		return fmt.Errorf("connect to %s on %s: %w", r.Datname, r.Shard, err)
	}
	defer conn.Close(context.Background())

	qualified := pgx.Identifier{r.Schema, r.Table}.Sanitize()
	col := pgx.Identifier{r.CutoffColumn}.Sanitize()
	sql := fmt.Sprintf(`
		WITH doomed AS (
		    SELECT ctid FROM %[1]s WHERE %[2]s < $1 LIMIT %[3]d FOR UPDATE SKIP LOCKED
		)
		DELETE FROM %[1]s WHERE ctid IN (SELECT ctid FROM doomed)`,
		qualified, col, dbArchiveDeleteBatch)

	deadline := time.Now().Add(dbArchiveDeleteBudget)
	deleted := r.DeletedRows
	for time.Now().Before(deadline) {
		tag, err := conn.Exec(ctx, sql, r.Cutoff)
		if err != nil {
			return fmt.Errorf("delete archived rows: %w", err)
		}
		if tag.RowsAffected() == 0 {
			break
		}
		deleted += tag.RowsAffected()
	}
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs SET deleted_rows = $2, updated_at = NOW() WHERE id = $1`,
		r.ID, deleted); err != nil {
		return err
	}
	var left int64
	if err := conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*) FROM %s WHERE %s < $1`, qualified, col), r.Cutoff).Scan(&left); err != nil {
		return err
	}
	if left > 0 {
		return nil
	}
	return w.setPhase(ctx, r, dbArchiveRepack)
}

// repack returns the freed space to the filesystem.
//
// A plain DELETE frees nothing a tenant can see: the pages stay in the
// relation, and the quota this whole feature exists to satisfy is measured on
// the relation's size. pg_repack rewrites it without holding an exclusive lock
// for the duration, which is what keeps the unavailability window to the swap
// rather than the rewrite.
//
// The guard runs first and fails closed: pg_repack needs room for a second copy
// of the table, and running it on a volume that lacks that room turns a storage
// problem into a full disk on a shared instance.
func (w *dbArchiveWorker) repack(ctx context.Context, r archiveRun) error {
	conn, err := w.h.connectToTenantDB(ctx, r.Shard, r.Datname)
	if err != nil {
		return fmt.Errorf("connect to %s on %s: %w", r.Datname, r.Shard, err)
	}
	defer conn.Close(context.Background())

	qualified := pgx.Identifier{r.Schema, r.Table}.Sanitize()
	var tableBytes int64
	if err := conn.QueryRow(ctx,
		`SELECT pg_total_relation_size($1::regclass)`, qualified).Scan(&tableBytes); err != nil {
		return fmt.Errorf("read the table size: %w", err)
	}
	free, err := w.shardFreeBytes(ctx, r.Shard)
	if err != nil {
		return err
	}
	if !repackHasHeadroom(free, tableBytes) {
		return fmt.Errorf(
			"not enough free space on shard %s to repack %s.%s: %s free, %s needed. The rows are archived and deleted; an operator has to reclaim the space",
			r.Shard, r.Schema, r.Table, humanBytes(free), humanBytes(repackNeededBytes(tableBytes)))
	}

	pg, err := w.shardConn(r.Shard, r.Datname)
	if err != nil {
		return err
	}
	name := archiveJobName(r.ID, dbArchiveRepack)
	done, err := w.jobs.Ensure(ctx, name, func() *batchv1.Job {
		return archiveRepackJob(name, r, pg, w.repackImage())
	})
	if err != nil || !done {
		return err
	}

	var afterBytes int64
	if err := conn.QueryRow(ctx,
		`SELECT pg_total_relation_size($1::regclass)`, qualified).Scan(&afterBytes); err != nil {
		return err
	}
	return w.finish(ctx, r, archiveFreedBytes(tableBytes, afterBytes), nil)
}

// archiveFreedBytes is what the repack actually returned. A table that grew
// during the run freed nothing rather than a negative amount. Pure.
func archiveFreedBytes(before, after int64) int64 {
	if before <= after {
		return 0
	}
	return before - after
}

// finish writes the manifest and closes the run. The manifest is what the
// tenant is shown and what a support conversation is conducted against: where
// the data went, what it covers, and the one line that reads it back.
func (w *dbArchiveWorker) finish(ctx context.Context, r archiveRun, freed int64, extra map[string]any) error {
	manifest := archiveManifest(r, freed)
	for k, v := range extra {
		manifest[k] = v
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_archive_runs
		    SET phase = 'done', error = '', bytes_freed = $2, manifest = $3,
		        finished_at = NOW(), updated_at = NOW()
		  WHERE id = $1`, r.ID, freed, raw); err != nil {
		return err
	}
	if r.PlannedRows > 0 {
		w.notifyArchiveDone(ctx, r, freed)
	}
	return nil
}

// notifyArchiveDone tells the owner what left the database and how to read it
// back.
//
// It is sent for manual runs too, not only automatic ones: the person who
// pressed the button is not necessarily the person who will go looking for the
// rows six months later, and the letter is the durable copy of the manifest.
// A send failure is logged and dropped -- the archive itself is finished, and
// re-running it to retry a letter would be far worse than a missing letter.
func (w *dbArchiveWorker) notifyArchiveDone(ctx context.Context, r archiveRun, freed int64) {
	if w.h.auditNotifier == nil {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, r.ProjectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		return
	}
	subject, body := notify.ComposeDatabaseArchiveDone(
		r.Datname, r.Schema+"."+r.Table, r.Cutoff.Format("02.01.2006"),
		r.PlannedRows, gigabytes(freed), r.S3URI, r.Auto,
		w.h.databasesConsoleLink(r.ProjectID))
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(r.ProjectID.String(), w.h.projectDisplayName(ctx, r.ProjectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("db-archive: done notice to %s failed for %s: %v", to, r.Datname, err)
		w.h.recordNotifySend(ctx, r.ProjectID, "DatabaseArchiveDone", r.ResourceName, source, err)
		return
	}
	w.h.recordNotifySend(ctx, r.ProjectID, "DatabaseArchiveDone", r.ResourceName, source, nil)
}

// jobInputs gathers what an export or verify Job needs: the bucket credentials
// and the admin connection to the one database being archived.
func (w *dbArchiveWorker) jobInputs(ctx context.Context, r archiveRun) (cloudtask.S3Credentials, archivePGConn, error) {
	creds, err := w.h.s3creds.Resolve(ctx, dbArchiveBucketResource)
	if err != nil {
		return cloudtask.S3Credentials{}, archivePGConn{}, fmt.Errorf("read archive bucket credentials: %w", err)
	}
	conn, err := w.shardConn(r.Shard, r.Datname)
	if err != nil {
		return cloudtask.S3Credentials{}, archivePGConn{}, err
	}
	return creds, conn, nil
}

// shardConn resolves a shard's admin credentials against one database, in the
// parts libpq reads from the environment.
func (w *dbArchiveWorker) shardConn(shard, datname string) (archivePGConn, error) {
	dsn, ok := parseShardAdminDSNs(w.h.cfg.DBShardAdminDSNs)[shard]
	if !ok {
		return archivePGConn{}, fmt.Errorf("no admin credentials for shard %q", shard)
	}
	cfg, err := configForDatabase(dsn, datname)
	if err != nil {
		return archivePGConn{}, err
	}
	return archivePGConn{
		Host:     cfg.Host,
		Port:     int(cfg.Port),
		User:     cfg.User,
		Password: cfg.Password,
		Database: datname,
	}, nil
}

// shardFreeBytes reads how much room the shard's volume has left.
//
// It fails rather than guesses. The one thing this number gates is a rewrite
// that doubles a table on disk, so "no measurement" has to mean "do not run",
// not "assume there is room".
func (w *dbArchiveWorker) shardFreeBytes(ctx context.Context, shard string) (int64, error) {
	if w.h.prometheus == nil {
		return 0, fmt.Errorf("cannot measure free space on shard %s: no metrics source", shard)
	}
	var host string
	if err := w.h.pool.QueryRow(ctx, `SELECT host FROM db_shards WHERE name = $1`, shard).Scan(&host); err != nil {
		return 0, fmt.Errorf("read address of shard %q: %w", shard, err)
	}
	pvc := archiveShardPVC(host)
	if pvc == "" {
		return 0, fmt.Errorf("cannot derive the volume of shard %s from host %q", shard, host)
	}
	query := fmt.Sprintf(
		`kubelet_volume_stats_available_bytes{namespace=%q,persistentvolumeclaim=%q}`,
		dbArchiveNamespace, pvc)
	samples, err := w.h.prometheus.QueryInstant(ctx, query, time.Time{}, "")
	if err != nil {
		return 0, fmt.Errorf("read free space of %s: %w", pvc, err)
	}
	if len(samples) == 0 {
		return 0, fmt.Errorf("no free-space sample for volume %s of shard %s", pvc, shard)
	}
	return int64(samples[0].Point.V), nil
}

// exportImage is the image carrying the DuckDB CLI that exports and verifies.
func (w *dbArchiveWorker) exportImage() string {
	if w.h.cfg != nil && w.h.cfg.DBArchiveExportImage != "" {
		return w.h.cfg.DBArchiveExportImage
	}
	return defaultDBArchiveExportImage
}

// repackImage is the image carrying pg_repack.
func (w *dbArchiveWorker) repackImage() string {
	if w.h.cfg != nil && w.h.cfg.DBArchiveRepackImage != "" {
		return w.h.cfg.DBArchiveRepackImage
	}
	return defaultDBArchiveRepackImage
}

// isS3CredentialsPending reports whether the bucket simply has not finished
// provisioning, which is a state to wait in rather than fail on.
func isS3CredentialsPending(err error) bool {
	return err != nil && strings.Contains(err.Error(), cloudtask.ErrS3CredentialsNotReady.Error())
}

// archiveColumnUsable reports whether the named column still exists on the
// table and can still carry a cutoff. A schema migration between the preview
// and the run must stop the run, not archive by a column that now means
// something else. Pure.
func archiveColumnUsable(cols []archiveColumn, name string) bool {
	for _, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return dbArchiveCutoffTypes[c.Type]
		}
	}
	return false
}

// repackNeededBytes is the free space a repack of a table this size requires.
// Pure.
func repackNeededBytes(tableBytes int64) int64 {
	return int64(float64(tableBytes) * dbArchiveRepackHeadroom)
}

// repackHasHeadroom reports whether a rewrite fits. Unknown free space (zero or
// negative) never fits: the guard exists to fail closed. Pure.
func repackHasHeadroom(freeBytes, tableBytes int64) bool {
	if freeBytes <= 0 {
		return false
	}
	if tableBytes <= 0 {
		return true
	}
	return freeBytes >= repackNeededBytes(tableBytes)
}

// archiveShardPVC derives the volume claim behind a shard from the host its
// admin DSN points at.
//
// Both shards are single-instance StatefulSets whose data volume is
// data-<statefulset>-0, and the Service in front of them carries the
// StatefulSet's name. Deriving it beats storing it because the pair cannot then
// drift apart; an unrecognisable host returns empty, which the caller turns
// into a refusal to repack. Pure.
func archiveShardPVC(host string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(host), ".")
	if name == "" {
		return ""
	}
	return "data-" + name + "-0"
}

// archiveObjectPrefix is where one run's Parquet lands inside the bucket.
//
// It carries the database, the table and the cutoff so the layout is legible to
// a human browsing the bucket, and the run id so a repeated archive of the same
// table and cutoff never overwrites an earlier one. Pure.
func archiveObjectPrefix(r archiveRun) string {
	return fmt.Sprintf("db-archive/%s/%s.%s/%s-%s",
		r.Datname, r.Schema, r.Table, r.Cutoff.Format("2006-01-02"), shortArchiveID(r.ID))
}

// archiveObjectURI is the full s3:// URI of one run's Parquet file. Pure.
func archiveObjectURI(bucket string, r archiveRun) string {
	return fmt.Sprintf("s3://%s/%s/data.parquet", bucket, archiveObjectPrefix(r))
}

// archiveBucketName is the globally unique bucket a project's archives live in.
// Beget bucket names are global, so the project id is what keeps two tenants
// from colliding. Pure.
func archiveBucketName(projectID uuid.UUID) string {
	return "dada-archive-" + strings.ReplaceAll(projectID.String(), "-", "")[:12]
}

// archiveJobName derives a Job name from the run and phase, so a retried tick
// finds the Job it already created instead of starting a second export against
// the same table. Pure.
func archiveJobName(id uuid.UUID, phase string) string {
	return fmt.Sprintf("db-archive-%s-%s", shortArchiveID(id), phase)
}

// shortArchiveID is the run id in the 12 hex characters a Kubernetes object
// name can carry alongside its prefix. Pure.
func shortArchiveID(id uuid.UUID) string {
	short := strings.ReplaceAll(id.String(), "-", "")
	if len(short) > 12 {
		short = short[:12]
	}
	return short
}

// archiveManifest describes a finished archive in the terms its owner needs:
// what left, from when, where it is, and how to read it back. Pure.
func archiveManifest(r archiveRun, freed int64) map[string]any {
	return map[string]any{
		"database":     r.Datname,
		"table":        r.Schema + "." + r.Table,
		"column":       r.CutoffColumn,
		"cutoff":       r.Cutoff.Format("2006-01-02"),
		"rows":         r.PlannedRows,
		"format":       "parquet",
		"uri":          r.S3URI,
		"freed":        freed,
		"freedHuman":   humanBytes(freed),
		"readDuckDB":   fmt.Sprintf("SELECT * FROM read_parquet('%s');", r.S3URI),
		"readPandas":   fmt.Sprintf("pandas.read_parquet('%s')", r.S3URI),
		"backupIntact": true,
	}
}

// clusterArchiveJobs runs the phase Jobs through the in-cluster API.
type clusterArchiveJobs struct {
	cs kubernetes.Interface
}

// newClusterArchiveJobs builds the Job runner, or nil when the console is not
// running inside a cluster and therefore cannot run any of these phases.
func newClusterArchiveJobs() archiveJobs {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return &clusterArchiveJobs{cs: cs}
}

// Ensure creates the Job on first call and reports its outcome afterwards.
func (c *clusterArchiveJobs) Ensure(ctx context.Context, name string, build func() *batchv1.Job) (bool, error) {
	job, err := c.cs.BatchV1().Jobs(dbArchiveNamespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.cs.BatchV1().Jobs(dbArchiveNamespace).Create(ctx, build(), metav1.CreateOptions{}); err != nil {
			return false, fmt.Errorf("create job %s: %w", name, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read job %s: %w", name, err)
	}
	if job.Status.Succeeded > 0 {
		return true, nil
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return false, fmt.Errorf("job %s failed: %s, see its pod logs", name, cond.Reason)
		}
	}
	return false, nil
}
