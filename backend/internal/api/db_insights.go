package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dbInsightsDefaultShard is where every database created before sharding
// existed lives, mirroring the ServiceDatabaseV2 XRD default. A snapshot with
// no spec.shard is on the shared instance, not on no instance at all.
const dbInsightsDefaultShard = "shard-1"

// dbInsightsWindow is the lookback the insight endpoints report over. It
// matches the advisory engine's window so a growth figure on the page and the
// growth an advisory fired on are the same number.
const dbInsightsWindow = 7 * 24 * time.Hour

// dbInsightsFreshness bounds how stale a sample may be and still be served. A
// collector that stopped is a fact the console has to show, not a number to
// keep rendering as if it were current.
const dbInsightsFreshness = time.Hour

// serviceDatabaseShard pulls the shard a database was placed on out of its
// snapshot. Placement is written once at creation and never moves on its own,
// so an absent field means the pre-sharding default.
func serviceDatabaseShard(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return dbInsightsDefaultShard
	}
	if spec, ok := summary["spec"].(map[string]any); ok {
		if s, _ := spec["shard"].(string); s != "" {
			return s
		}
	}
	if s, _ := summary["shard"].(string); s != "" {
		return s
	}
	return dbInsightsDefaultShard
}

// serviceDatabaseTier pulls the quota tier the reconciler observed live on the
// CR. An absent tier is the XRD default, matching the quota watcher.
func serviceDatabaseTier(summaryRaw []byte) string {
	var summary struct {
		Tier string `json:"tier"`
	}
	if json.Unmarshal(summaryRaw, &summary) != nil || summary.Tier == "" {
		return "unlimited"
	}
	return summary.Tier
}

// dbInsightsTarget is a resolved database: which instance to read samples from
// and which logical database on it.
type dbInsightsTarget struct {
	Shard      string
	Datname    string
	Tier       string
	LimitBytes int64
}

// resolveInsightsTarget authorizes the caller and maps the console's resource
// name to the (shard, datname) pair the samples are keyed by.
//
// Read access is enough. Insights describe the caller's own data and expose no
// credential, and requiring write access would hide the page from exactly the
// people most likely to be asked why the application is slow.
func (h *Handler) resolveInsightsTarget(c *gin.Context) (uuid.UUID, uuid.UUID, dbInsightsTarget, bool) {
	var zero dbInsightsTarget
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return uuid.Nil, uuid.Nil, zero, false
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return uuid.Nil, uuid.Nil, zero, false
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return uuid.Nil, uuid.Nil, zero, false
	}

	var summaryRaw []byte
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		  WHERE project_id = $1 AND environment_id = $2
		    AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, c.Param("name")).Scan(&summaryRaw); err != nil {
		if err == pgx.ErrNoRows {
			respondNotFound(c)
			return uuid.Nil, uuid.Nil, zero, false
		}
		respondError(c, http.StatusInternalServerError, "failed to look up database")
		return uuid.Nil, uuid.Nil, zero, false
	}

	datname := serviceDatabaseDatname(summaryRaw)
	if datname == "" {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, zero, false
	}
	tier := serviceDatabaseTier(summaryRaw)
	return projectID, envID, dbInsightsTarget{
		Shard:      serviceDatabaseShard(summaryRaw),
		Datname:    datname,
		Tier:       tier,
		LimitBytes: databaseTierLimitBytes[tier],
	}, true
}

// GetDatabaseInsights returns the headline numbers for one managed database.
//
// @ID          getDatabaseInsights
// @Summary     Database size, growth and cache efficiency
// @Description Returns the headline insight numbers for a managed PostgreSQL database: current size against the tier quota, growth over the last seven days, buffer cache hit ratio, connection count and how fresh the underlying sample is. Reads collected samples from the control plane, never the tenant instance. Returns collectedAt = null when no sample has been collected yet, which is what an installation without collector credentials looks like.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/insights [get]
func (h *Handler) GetDatabaseInsights(c *gin.Context) {
	_, envID, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var (
		collectedAt               time.Time
		sizeBytes                 int64
		blksRead, blksHit         int64
		numbackends               int32
		statsReset, instanceStart *time.Time
	)
	err := h.pool.QueryRow(ctx, `
		SELECT collected_at, size_bytes, blks_read, blks_hit, numbackends,
		       stats_reset, instance_start_at
		  FROM db_stat_databases
		 WHERE shard = $1 AND datname = $2
		 ORDER BY collected_at DESC
		 LIMIT 1`, target.Shard, target.Datname).
		Scan(&collectedAt, &sizeBytes, &blksRead, &blksHit, &numbackends, &statsReset, &instanceStart)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{
			"collectedAt":    nil,
			"tier":           target.Tier,
			"sizeLimitBytes": target.LimitBytes,
		})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read insights")
		return
	}

	var (
		firstAt     time.Time
		firstSize   int64
		firstRead   int64
		firstHit    int64
		growthBytes int64
		hitRatio    *float64
	)
	if err := h.pool.QueryRow(ctx, `
		SELECT collected_at, size_bytes, blks_read, blks_hit
		  FROM db_stat_databases
		 WHERE shard = $1 AND datname = $2 AND collected_at > $3
		   AND stats_reset IS NOT DISTINCT FROM $4
		 ORDER BY collected_at ASC
		 LIMIT 1`, target.Shard, target.Datname,
		time.Now().Add(-dbInsightsWindow), statsReset).
		Scan(&firstAt, &firstSize, &firstRead, &firstHit); err == nil {
		growthBytes = sizeBytes - firstSize
		if reads, hits := blksRead-firstRead, blksHit-firstHit; reads >= 0 && hits >= 0 && reads+hits > 0 {
			r := float64(hits) / float64(reads+hits)
			hitRatio = &r
		}
	}

	quotaState, graceUntil := h.databaseQuotaState(ctx, envID, c.Param("name"))

	c.JSON(http.StatusOK, gin.H{
		"collectedAt":     collectedAt,
		"stale":           time.Since(collectedAt) > dbInsightsFreshness,
		"shard":           target.Shard,
		"database":        target.Datname,
		"tier":            target.Tier,
		"sizeBytes":       sizeBytes,
		"sizeLimitBytes":  target.LimitBytes,
		"growthBytes7d":   growthBytes,
		"cacheHitRatio":   hitRatio,
		"connections":     numbackends,
		"instanceStartAt": instanceStart,
		"quotaState":      quotaState,
		"graceUntil":      graceUntil,
		"warnRatio":       dbQuotaWarnRatio,
	})
}

// ListDatabaseTables returns the table cards: one entry per collected table
// with its size, row estimate and growth over the window.
//
// @ID          listDatabaseTables
// @Summary     Per-table size, rows and growth
// @Description Lists the tables of a managed PostgreSQL database with heap and index size, estimated row count, cache hit ratio, growth over the last seven days and the last autovacuum/autoanalyze timestamps. Only tables above 1 MB are collected. Reads collected samples, never the tenant instance.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/tables [get]
func (h *Handler) ListDatabaseTables(c *gin.Context) {
	_, _, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), dbAdvisoryTableSQL+`
		 ORDER BY l.total_bytes DESC`,
		target.Shard, target.Datname, time.Now().Add(-dbInsightsWindow))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read tables")
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var (
			schemaName, relName                     string
			lastAt, firstAt                         time.Time
			total, heap, idxBytes, rowsEstimate     int64
			lastIns, lastDel, lastRead, lastHit     int64
			firstBytes                              int64
			firstIns, firstDel, firstRead, firstHit int64
			lastAnalyze                             *time.Time
		)
		if err := rows.Scan(&schemaName, &relName, &lastAt, &firstAt,
			&total, &heap, &idxBytes, &rowsEstimate,
			&lastIns, &lastDel, &lastRead, &lastHit, &lastAnalyze,
			&firstBytes, &firstIns, &firstDel, &firstRead, &firstHit); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read tables")
			return
		}
		entry := gin.H{
			"schema":          schemaName,
			"name":            relName,
			"totalBytes":      total,
			"heapBytes":       heap,
			"indexBytes":      idxBytes,
			"rowsEstimate":    rowsEstimate,
			"lastAutoanalyze": lastAnalyze,
			"windowHours":     int(lastAt.Sub(firstAt).Hours()),
		}
		if lastIns >= firstIns && lastDel >= firstDel {
			entry["growthBytes"] = total - firstBytes
			entry["insertedRows"] = lastIns - firstIns
			entry["deletedRows"] = lastDel - firstDel
			entry["appendOnly"] = lastDel == firstDel
		}
		if reads, hits := lastRead-firstRead, lastHit-firstHit; reads >= 0 && hits >= 0 && reads+hits > 0 {
			entry["cacheHitRatio"] = float64(hits) / float64(reads+hits)
			entry["bytesReadFromDisk"] = reads * 8192
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read tables")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tables": out})
}

// ListDatabaseQueries returns the heaviest normalized queries.
//
// @ID          listDatabaseQueries
// @Summary     Slowest and heaviest queries
// @Description Lists the heaviest normalized queries of a managed PostgreSQL database over the last seven days, by total execution time, with call count, mean duration, rows per call, physical reads and share of the database's execution time. Query text is the normalized form pg_stat_statements stores, with literal constants already replaced by placeholders. Reads collected samples, never the tenant instance.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/queries [get]
func (h *Handler) ListDatabaseQueries(c *gin.Context) {
	_, _, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), dbAdvisoryStatementSQL+`
		 ORDER BY l.total_exec_ms DESC`,
		target.Shard, target.Datname, time.Now().Add(-dbInsightsWindow))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read queries")
		return
	}
	defer rows.Close()

	type entry struct {
		QueryID     int64   `json:"queryId"`
		Sample      string  `json:"query"`
		MeanMs      float64 `json:"meanMs"`
		Calls       int64   `json:"calls"`
		TotalMs     float64 `json:"totalMs"`
		Share       float64 `json:"share"`
		RowsPerCall float64 `json:"rowsPerCall"`
	}
	var (
		list    []entry
		totalMs float64
	)
	for rows.Next() {
		var (
			e                     entry
			lastCalls, firstCalls int64
			lastTotal, firstTotal float64
		)
		if err := rows.Scan(&e.QueryID, &e.Sample, &e.MeanMs,
			&lastCalls, &lastTotal, &firstCalls, &firstTotal); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read queries")
			return
		}
		e.Calls = lastCalls - firstCalls
		e.TotalMs = lastTotal - firstTotal
		if e.Calls <= 0 || e.TotalMs <= 0 {
			e.Calls = lastCalls
			e.TotalMs = lastTotal
		}
		totalMs += e.TotalMs
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read queries")
		return
	}
	out := make([]entry, 0, len(list))
	for _, e := range list {
		if totalMs > 0 {
			e.Share = e.TotalMs / totalMs
		}
		out = append(out, e)
	}
	c.JSON(http.StatusOK, gin.H{"queries": out, "totalMs": totalMs})
}

// ListDatabaseAdvisories returns what the platform already worked out about
// this database, most severe first.
//
// @ID          listDatabaseAdvisories
// @Summary     What the platform found wrong in this database
// @Description Lists the advisories the platform derived for a managed PostgreSQL database: unused indexes, stale planner statistics, append-only tables with no retention, tables read off disk instead of cache, slow or dominant queries, and a storage-quota forecast. Each carries the evidence it fired on and, where the action is safe to spell out, the SQL to fix it. The SQL is the owner's to run: the platform never executes DDL against tenant data.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/advisories [get]
func (h *Handler) ListDatabaseAdvisories(c *gin.Context) {
	_, _, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT code, subject, severity, detail, suggested_sql, evidence,
		       first_seen_at, last_seen_at
		  FROM db_advisories
		 WHERE shard = $1 AND datname = $2
		 ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
		          code, subject`, target.Shard, target.Datname)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read advisories")
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var (
			code, subject, severity, detail, sql string
			evidence                             []byte
			firstSeen, lastSeen                  time.Time
		)
		if err := rows.Scan(&code, &subject, &severity, &detail, &sql, &evidence,
			&firstSeen, &lastSeen); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read advisories")
			return
		}
		var parsed map[string]any
		if json.Unmarshal(evidence, &parsed) != nil {
			parsed = map[string]any{}
		}
		out = append(out, gin.H{
			"code":         code,
			"subject":      subject,
			"severity":     severity,
			"detail":       detail,
			"suggestedSql": sql,
			"evidence":     parsed,
			"firstSeenAt":  firstSeen,
			"lastSeenAt":   lastSeen,
		})
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read advisories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"advisories": out})
}

// databaseQuotaState reports what the quota watcher has decided about one
// database: the enforcement state it is in and, when a grace window is open,
// the moment it closes.
//
// The console needs both to say anything useful about a database that is over
// its limit -- the size alone cannot distinguish "over quota, one day to fix
// it" from "already read-only". A database the watcher has never seen has no
// row, which is not an error: it reads as 'none', the same as a healthy one.
func (h *Handler) databaseQuotaState(ctx context.Context, envID uuid.UUID, name string) (string, *time.Time) {
	var (
		state      string
		graceUntil *time.Time
	)
	err := h.pool.QueryRow(ctx,
		`SELECT state, grace_until FROM db_quota_state
		  WHERE environment_id = $1 AND name = $2`,
		envID, name).Scan(&state, &graceUntil)
	if err != nil {
		return dbEnforcementNone, nil
	}
	if graceUntil != nil && graceUntil.Before(time.Now()) {
		graceUntil = nil
	}
	return state, graceUntil
}
