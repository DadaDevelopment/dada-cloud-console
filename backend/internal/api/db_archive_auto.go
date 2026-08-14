package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// dbArchiveAutoTier is the only tier archived without a human pressing
// anything.
//
// The free tier has no upgrade path that costs the owner nothing and no support
// contract behind it, so a free database that hits its limit would otherwise sit
// read-only until someone noticed. On paid tiers the same operation exists as a
// button: those owners can buy their way out, and deleting a paying customer's
// rows on a timer is not a decision this worker gets to make.
const dbArchiveAutoTier = "free"

// dbArchiveAutoTarget is the fraction of the quota an automatic archive aims to
// leave the database at. Archiving exactly down to the limit would put the
// database back over it within a day of normal writes, and each archive costs a
// full table rewrite -- so the cutoff is chosen to buy real headroom.
const dbArchiveAutoTarget = 0.60

// dbArchiveAutoMinBytes is the smallest reclaim worth a rewrite. Below it the
// archive would move a handful of rows, hold an exclusive lock at the swap, and
// leave the database still over quota.
const dbArchiveAutoMinBytes = int64(64) << 20

// dbArchiveAutoTimeout bounds the live inspection an automatic archive does
// before it queues anything. It runs against a shard that is by definition
// under storage pressure, so it must give up rather than hold a connection.
const dbArchiveAutoTimeout = 30 * time.Second

// maybeAutoArchive queues an archive for a free-tier database that has run out
// of storage, choosing the table and the cutoff itself.
//
// It queues at most one run and only ever for the largest archivable table: an
// automatic action that touched several tables at once would be much harder for
// an owner to understand after the fact, and the largest table is almost always
// where an append-only history sits. Everything it queues is subject to the same
// verify-before-delete phases a manual run goes through.
//
// Failures here are logged and dropped. The quota ladder is the thing that
// protects the shard; the archive is the thing that helps the owner, and an
// unreachable tenant instance must not stop the former from proceeding.
func (w *dbQuotaWatcher) maybeAutoArchive(ctx context.Context, d managedDatabase, shard string, size, limit int64) {
	if d.Tier != dbArchiveAutoTier || limit <= 0 || shard == "" {
		return
	}
	want := size - int64(float64(limit)*dbArchiveAutoTarget)
	if want < dbArchiveAutoMinBytes {
		return
	}

	var open int
	if err := w.h.pool.QueryRow(ctx,
		`SELECT count(*) FROM db_archive_runs
		  WHERE datname = $1 AND phase NOT IN ('done', 'failed')`,
		d.Datname).Scan(&open); err != nil || open > 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, dbArchiveAutoTimeout)
	defer cancel()

	conn, err := w.h.connectToTenantDB(ctx, shard, d.Datname)
	if err != nil {
		log.Printf("db-archive: auto plan for %s unreachable: %v", d.Datname, err)
		return
	}
	defer conn.Close(context.Background())

	schema, table, totalRows, totalBytes, err := largestArchivableTable(ctx, conn)
	if err != nil || table == "" {
		return
	}
	cols, err := archiveColumnsOf(ctx, conn, schema, table)
	if err != nil {
		return
	}
	column, reason := pickCutoffColumn(cols)
	if reason != "" {
		log.Printf("db-archive: auto plan for %s.%s in %s skipped: %s", schema, table, d.Datname, reason)
		return
	}

	qualified := pgx.Identifier{schema, table}.Sanitize()
	buckets, _, err := archiveHistogram(ctx, conn, qualified, column.Name, totalRows)
	if err != nil {
		return
	}
	cutoff, ok := autoArchiveCutoff(buckets, totalRows, totalBytes, want)
	if !ok {
		log.Printf("db-archive: auto plan for %s.%s in %s found no cutoff that frees %s while keeping a month of history",
			schema, table, d.Datname, humanBytes(want))
		return
	}

	var id string
	if err := w.h.pool.QueryRow(ctx,
		`INSERT INTO db_archive_runs
		     (project_id, environment_id, resource_name, datname, shard,
		      schema_name, table_name, cutoff_column, cutoff_date, auto)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE)
		 RETURNING id`,
		d.ProjectID, d.EnvironmentID, d.Name, d.Datname, shard,
		schema, table, column.Name, cutoff).Scan(&id); err != nil {
		if !isArchiveUniqueViolation(err) {
			log.Printf("db-archive: auto queue for %s.%s in %s failed: %v", schema, table, d.Datname, err)
		}
		return
	}
	log.Printf("db-archive: queued auto run %s for %s.%s in %s cutoff=%s target=%s",
		id, schema, table, d.Datname, cutoff.Format("2006-01-02"), humanBytes(want))
}

// autoArchiveCutoff picks the oldest cutoff that frees the wanted bytes.
//
// It walks the months from oldest to newest and stops at the first one whose
// accumulated rows are worth enough, so the archive removes the least history
// that solves the problem rather than everything it is allowed to. The newest
// bucket is never a candidate: the month currently being written to is the one
// the application is reading back, and an automatic action must not touch it.
// Pure.
func autoArchiveCutoff(buckets []archiveBucket, totalRows, totalBytes, wantBytes int64) (time.Time, bool) {
	if len(buckets) < 2 || wantBytes <= 0 {
		return time.Time{}, false
	}
	var rows int64
	for i, b := range buckets {
		if i == len(buckets)-1 {
			break
		}
		rows += b.Rows
		if estimateArchiveBytes(rows, totalRows, totalBytes) >= wantBytes {
			return buckets[i+1].Month.UTC().Truncate(24 * time.Hour), true
		}
	}
	return time.Time{}, false
}

// largestArchivableTable is the ordinary table holding the most bytes in the
// tenant database, indexes included, since that is what the quota measures.
//
// Partitioned parents and inheritance children are excluded: archiving out of a
// partitioned table is a matter of detaching partitions, which frees the space
// without a rewrite and is a different feature.
func largestArchivableTable(ctx context.Context, conn *pgx.Conn) (string, string, int64, int64, error) {
	var schema, table string
	var rows, bytes int64
	err := conn.QueryRow(ctx,
		`SELECT n.nspname, c.relname, GREATEST(c.reltuples, 0)::bigint,
		        pg_total_relation_size(c.oid)
		   FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'r'
		    AND NOT c.relispartition
		    AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
		  ORDER BY pg_total_relation_size(c.oid) DESC
		  LIMIT 1`).Scan(&schema, &table, &rows, &bytes)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", "", 0, 0, nil
		}
		return "", "", 0, 0, fmt.Errorf("find the largest table: %w", err)
	}
	return schema, table, rows, bytes, nil
}
