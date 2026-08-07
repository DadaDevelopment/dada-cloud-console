package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// dbTableSeriesPoints bounds how many size samples the growth sparkline gets.
// The collector samples relations every third tick, so a week is roughly 480
// points; the page needs a shape, not every sample.
const dbTableSeriesPoints = 200

// dbTableQueryLimit bounds how many statements are attributed to one table.
const dbTableQueryLimit = 20

// dbTableSchema resolves the schema a table page was opened for. The console
// links tables by name alone because collected tables are almost always in
// public; an explicit ?schema= wins when a database uses several.
func dbTableSchema(c *gin.Context) string {
	if s := strings.TrimSpace(c.Query("schema")); s != "" {
		return s
	}
	return "public"
}

// GetDatabaseTable returns one table as a first-class console object: its size
// history, its indexes and how much each is actually used, the statements that
// touch it, and the advisories the platform already raised against it.
//
// Statement attribution is a text match on the normalized query against the
// table name. pg_stat_statements does not record which relations a statement
// touched, so the alternative is no per-table queries at all; the match is
// reported as such rather than presented as an execution plan.
//
// @ID          getDatabaseTable
// @Summary     One table: growth, indexes, queries and findings
// @Description Returns everything the platform knows about a single table of a managed PostgreSQL database: size history over the last seven days, heap and index split, dead tuples, sequential versus index scans, every index with its size and scan count, the heaviest queries whose normalized text mentions the table, and the advisories raised against it. Reads collected samples, never the tenant instance.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       name      path     string true  "Database resource name"
// @Param       table     path     string true  "Table name"
// @Param       schema    query    string false "Schema name, defaults to public"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/tables/{table} [get]
func (h *Handler) GetDatabaseTable(c *gin.Context) {
	_, _, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}
	schema := dbTableSchema(c)
	relname := strings.TrimSpace(c.Param("table"))
	if relname == "" {
		respondNotFound(c)
		return
	}
	since := time.Now().Add(-dbInsightsWindow)
	ctx := c.Request.Context()

	series, latest, first, err := h.dbTableSeries(c, target, schema, relname, since)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read table samples")
		return
	}
	if latest == nil {
		respondNotFound(c)
		return
	}

	indexes, err := h.dbTableIndexes(c, target, schema, relname, since)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read indexes")
		return
	}
	queries, err := h.dbTableQueries(c, target, relname, since)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read queries")
		return
	}
	advisories, err := h.dbTableAdvisories(ctx, target, schema, relname)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read advisories")
		return
	}

	table := gin.H{
		"schema":            schema,
		"name":              relname,
		"totalBytes":        latest.total,
		"heapBytes":         latest.heap,
		"indexBytes":        latest.index,
		"rowsEstimate":      rowsEstimateValue(latest.rows),
		"liveRows":          latest.live,
		"deadRows":          latest.dead,
		"lastAutovacuum":    latest.autovacuum,
		"lastAutoanalyze":   latest.autoanalyze,
		"collectedAt":       latest.at,
		"sampleStale":       time.Since(latest.at) > dbInsightsFreshness,
		"windowHours":       0,
		"growthBytes":       nil,
		"insertedRows":      nil,
		"deletedRows":       nil,
		"appendOnly":        nil,
		"seqScans":          nil,
		"indexScans":        nil,
		"cacheHitRatio":     nil,
		"bytesReadFromDisk": nil,
	}
	if first != nil {
		table["windowHours"] = int(latest.at.Sub(first.at).Hours())
		if latest.ins >= first.ins && latest.del >= first.del {
			table["growthBytes"] = latest.total - first.total
			table["insertedRows"] = latest.ins - first.ins
			table["deletedRows"] = latest.del - first.del
			table["appendOnly"] = latest.del == first.del
		}
		if latest.seqScan >= first.seqScan && latest.idxScan >= first.idxScan {
			table["seqScans"] = latest.seqScan - first.seqScan
			table["indexScans"] = latest.idxScan - first.idxScan
		}
		reads, hits := latest.read-first.read, latest.hit-first.hit
		if reads >= 0 && hits >= 0 && reads+hits > 0 {
			table["cacheHitRatio"] = float64(hits) / float64(reads+hits)
			table["bytesReadFromDisk"] = reads * 8192
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"table":      table,
		"series":     series,
		"indexes":    indexes,
		"queries":    queries,
		"advisories": advisories,
	})
}

// dbTableSample is one collected row of pg_stat_user_tables for this table.
type dbTableSample struct {
	at                      time.Time
	total, heap, index      int64
	rows, live, dead        int64
	ins, del                int64
	seqScan, idxScan        int64
	read, hit               int64
	autovacuum, autoanalyze *time.Time
}

// dbTableSeries reads every sample of one table in the window and returns the
// sparkline points plus the newest and oldest sample. Deltas are taken between
// those two rather than summed across the series, matching the advisory engine.
func (h *Handler) dbTableSeries(c *gin.Context, target dbInsightsTarget, schema, relname string, since time.Time) ([]gin.H, *dbTableSample, *dbTableSample, error) {
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT collected_at, total_bytes, heap_bytes, index_bytes, rows_estimate,
		       n_live_tup, n_dead_tup, n_tup_ins, n_tup_del,
		       seq_scan, idx_scan, heap_blks_read, heap_blks_hit,
		       last_autovacuum, last_autoanalyze
		  FROM db_stat_tables
		 WHERE shard = $1 AND datname = $2 AND schemaname = $3 AND relname = $4
		   AND collected_at >= $5
		 ORDER BY collected_at`,
		target.Shard, target.Datname, schema, relname, since)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var samples []dbTableSample
	for rows.Next() {
		var s dbTableSample
		if err := rows.Scan(&s.at, &s.total, &s.heap, &s.index, &s.rows,
			&s.live, &s.dead, &s.ins, &s.del,
			&s.seqScan, &s.idxScan, &s.read, &s.hit,
			&s.autovacuum, &s.autoanalyze); err != nil {
			return nil, nil, nil, err
		}
		samples = append(samples, s)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	if len(samples) == 0 {
		return []gin.H{}, nil, nil, nil
	}

	step := 1
	if len(samples) > dbTableSeriesPoints {
		step = (len(samples) + dbTableSeriesPoints - 1) / dbTableSeriesPoints
	}
	series := make([]gin.H, 0, len(samples)/step+1)
	for i := 0; i < len(samples); i += step {
		series = append(series, gin.H{
			"at":         samples[i].at,
			"totalBytes": samples[i].total,
		})
	}
	last := samples[len(samples)-1]
	if series[len(series)-1]["at"] != last.at {
		series = append(series, gin.H{"at": last.at, "totalBytes": last.total})
	}
	firstSample := samples[0]
	if len(samples) == 1 {
		return series, &last, nil, nil
	}
	return series, &last, &firstSample, nil
}

// dbTableIndexes reports every index on the table with its size and how often
// it was actually used inside the window. An index that was never scanned is
// the single most common finding on real tenant databases, so the scan count is
// a first-class column rather than a detail.
func (h *Handler) dbTableIndexes(c *gin.Context, target dbInsightsTarget, schema, relname string, since time.Time) ([]gin.H, error) {
	rows, err := h.pool.Query(c.Request.Context(), `
		WITH latest AS (
			SELECT DISTINCT ON (indexrelname) indexrelname, collected_at,
			       size_bytes, idx_scan, is_unique, is_primary
			  FROM db_stat_indexes
			 WHERE shard = $1 AND datname = $2 AND schemaname = $3 AND relname = $4
			 ORDER BY indexrelname, collected_at DESC
		), oldest AS (
			SELECT DISTINCT ON (indexrelname) indexrelname, collected_at, idx_scan
			  FROM db_stat_indexes
			 WHERE shard = $1 AND datname = $2 AND schemaname = $3 AND relname = $4
			   AND collected_at >= $5
			 ORDER BY indexrelname, collected_at
		)
		SELECT l.indexrelname, l.size_bytes, l.idx_scan, l.is_unique, l.is_primary,
		       o.idx_scan, o.collected_at, l.collected_at
		  FROM latest l LEFT JOIN oldest o USING (indexrelname)
		 ORDER BY l.size_bytes DESC`,
		target.Shard, target.Datname, schema, relname, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var (
			name                 string
			size, scans          int64
			unique, primary      bool
			firstScans           *int64
			firstAt, lastAt      time.Time
			firstAtPtr           *time.Time
			windowHours          int
			scansInWindow        *int64
			neverScannedInWindow bool
		)
		if err := rows.Scan(&name, &size, &scans, &unique, &primary,
			&firstScans, &firstAtPtr, &lastAt); err != nil {
			return nil, err
		}
		if firstAtPtr != nil {
			firstAt = *firstAtPtr
			windowHours = int(lastAt.Sub(firstAt).Hours())
		}
		if firstScans != nil && scans >= *firstScans {
			delta := scans - *firstScans
			scansInWindow = &delta
			neverScannedInWindow = delta == 0
		}
		out = append(out, gin.H{
			"name":          name,
			"sizeBytes":     size,
			"totalScans":    scans,
			"scansInWindow": scansInWindow,
			"neverScanned":  neverScannedInWindow,
			"windowHours":   windowHours,
			"isUnique":      unique,
			"isPrimary":     primary,
		})
	}
	return out, rows.Err()
}

// dbTableQueries attributes statements to the table by matching the table name
// inside the normalized query text. pg_stat_statements carries no relation
// list, so this is a text match and the response says so via matchedBy.
func (h *Handler) dbTableQueries(c *gin.Context, target dbInsightsTarget, relname string, since time.Time) ([]gin.H, error) {
	pattern := "%" + strings.ReplaceAll(strings.ReplaceAll(relname, "\\", "\\\\"), "%", "\\%") + "%"
	rows, err := h.pool.Query(c.Request.Context(), `
		WITH latest AS (
			SELECT DISTINCT ON (queryid) queryid, query_sample, calls, total_exec_ms, mean_exec_ms
			  FROM db_stat_statements
			 WHERE shard = $1 AND datname = $2 AND query_sample ILIKE $3
			 ORDER BY queryid, collected_at DESC
		), oldest AS (
			SELECT DISTINCT ON (queryid) queryid, calls, total_exec_ms
			  FROM db_stat_statements
			 WHERE shard = $1 AND datname = $2 AND query_sample ILIKE $3
			   AND collected_at >= $4
			 ORDER BY queryid, collected_at
		)
		SELECT l.queryid, l.query_sample, l.mean_exec_ms,
		       l.calls, l.total_exec_ms, o.calls, o.total_exec_ms
		  FROM latest l LEFT JOIN oldest o USING (queryid)
		 ORDER BY l.total_exec_ms DESC
		 LIMIT $5`,
		target.Shard, target.Datname, pattern, since, dbTableQueryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var (
			queryID    int64
			sample     string
			meanMs     float64
			lastCalls  int64
			lastTotal  float64
			firstCalls *int64
			firstTotal *float64
			calls      int64
			totalMs    float64
		)
		if err := rows.Scan(&queryID, &sample, &meanMs, &lastCalls, &lastTotal, &firstCalls, &firstTotal); err != nil {
			return nil, err
		}
		calls, totalMs = lastCalls, lastTotal
		if firstCalls != nil && firstTotal != nil && lastCalls >= *firstCalls && lastTotal >= *firstTotal {
			if d := lastCalls - *firstCalls; d > 0 {
				calls = d
				totalMs = lastTotal - *firstTotal
			}
		}
		out = append(out, gin.H{
			"queryId":   queryID,
			"query":     sample,
			"meanMs":    meanMs,
			"calls":     calls,
			"totalMs":   totalMs,
			"matchedBy": "text",
		})
	}
	return out, rows.Err()
}

// dbTableAdvisories returns the findings whose subject is this table, including
// the index-level ones: an unused index is reported against "table.index", and
// the owner looking at the table is exactly who should see it.
func (h *Handler) dbTableAdvisories(ctx context.Context, target dbInsightsTarget, schema, relname string) ([]gin.H, error) {
	qualified := fmt.Sprintf("%s.%s", schema, relname)
	rows, err := h.pool.Query(ctx, `
		SELECT code, subject, severity, detail, suggested_sql, evidence,
		       first_seen_at, last_seen_at
		  FROM db_advisories
		 WHERE shard = $1 AND datname = $2
		   AND (subject = $3 OR subject = $4 OR subject LIKE $3 || '.%' OR subject LIKE $4 || '.%')
		 ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, code`,
		target.Shard, target.Datname, qualified, relname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var (
			code, subject, severity, detail, sql string
			evidenceRaw                          []byte
			firstSeen, lastSeen                  time.Time
		)
		if err := rows.Scan(&code, &subject, &severity, &detail, &sql, &evidenceRaw, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		var evidence map[string]any
		_ = json.Unmarshal(evidenceRaw, &evidence)
		out = append(out, gin.H{
			"code":         code,
			"subject":      subject,
			"severity":     severity,
			"detail":       detail,
			"suggestedSql": sql,
			"evidence":     evidence,
			"firstSeenAt":  firstSeen,
			"lastSeenAt":   lastSeen,
		})
	}
	return out, rows.Err()
}
