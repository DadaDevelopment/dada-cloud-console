package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// dbArchivePlanTimeout bounds the whole live read. The plan is computed on the
// tenant instance while the owner waits, and a shard that is already at its
// storage limit is usually a shard under load: the panel must fail rather than
// hold a connection open behind a slow scan.
const dbArchivePlanTimeout = 15 * time.Second

// dbArchiveHistogramSamplePercent is the TABLESAMPLE fraction used to bucket a
// table by month.
//
// The honest alternative is counting every row per month, which on the table
// that motivated this feature (29 GB, hundreds of millions of rows) is a scan
// measured in minutes, run against the instance the owner is already
// overloading. The histogram exists so a human can pick a cutoff date; a 1%
// system sample answers "roughly how much sits before March" to well within the
// precision that decision needs, and the exact row count is measured by the
// archive job itself before anything is deleted.
const dbArchiveHistogramSamplePercent = 1.0

// dbArchiveMinSampleRows is the table size below which the histogram is counted
// exactly instead of sampled. TABLESAMPLE draws whole pages, so on a small
// table it can miss entire months and report zero for a bucket that has rows.
const dbArchiveMinSampleRows = 1_000_000

// dbArchiveCutoffTypes are the column types a cutoff can be expressed in. Only
// wall-clock types qualify: the user picks a date, and a synthetic id has no
// defensible mapping onto one.
var dbArchiveCutoffTypes = map[string]bool{
	"timestamp with time zone":    true,
	"timestamp without time zone": true,
	"date":                        true,
}

// dbArchiveColumnPreference ranks column names that conventionally carry the
// insertion time. It only breaks ties between equally-qualified columns: a
// table with both created_at and updated_at must archive by the one that never
// moves, or rows would leave on a schedule nobody declared.
var dbArchiveColumnPreference = []string{
	"created_at", "inserted_at", "created", "inserted",
	"event_time", "event_at", "occurred_at", "recorded_at",
	"logged_at", "captured_at", "collected_at",
	"timestamp", "ts", "time", "date",
}

// archiveColumn is one candidate cutoff column as the tenant instance reports
// it.
type archiveColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	NotNull  bool   `json:"notNull"`
	Indexed  bool   `json:"indexed"`
	Position int    `json:"-"`
}

// archiveColumnScore ranks a candidate. Higher is better.
//
// Indexing dominates because it decides whether the archive job's delete can
// be done in date-ordered batches or only by scanning the whole table, and an
// unindexed cutoff on a table this size is the difference between a short
// maintenance window and an hour of one. NOT NULL comes next: a nullable
// cutoff column means some rows have no date at all, and those rows can never
// be proven old enough to leave.
func archiveColumnScore(c archiveColumn) int {
	score := 0
	if c.Indexed {
		score += 100
	}
	if c.NotNull {
		score += 50
	}
	for i, name := range dbArchiveColumnPreference {
		if strings.EqualFold(c.Name, name) {
			score += len(dbArchiveColumnPreference) - i
			break
		}
	}
	return score
}

// pickCutoffColumn chooses the column an archive cutoff is expressed in, or
// reports why the table cannot be archived by date. Pure, so the choice is
// testable without a tenant database.
//
// Ties break on column position rather than on map order, because the answer
// is shown to a user and then written into a job: the same table must always
// produce the same plan.
func pickCutoffColumn(cols []archiveColumn) (archiveColumn, string) {
	var candidates []archiveColumn
	for _, c := range cols {
		if dbArchiveCutoffTypes[c.Type] {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return archiveColumn{}, "no timestamp or date column: archiving by date needs one"
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := archiveColumnScore(candidates[i]), archiveColumnScore(candidates[j])
		if si != sj {
			return si > sj
		}
		return candidates[i].Position < candidates[j].Position
	})
	return candidates[0], ""
}

// archiveBucket is one month of a table, as the histogram reports it.
type archiveBucket struct {
	Month     time.Time `json:"month"`
	Rows      int64     `json:"rows"`
	Estimated bool      `json:"estimated"`
}

// estimateArchiveBytes converts a row count into the bytes an archive would
// return to the filesystem.
//
// It is proportional to the whole relation, indexes included, because deleting
// a row removes its index entries too and the rewrite rebuilds both. It is an
// estimate and is labelled as one everywhere it surfaces: rows are not uniform,
// and a table whose old rows are narrower than its new ones will free less than
// this says. Pure.
func estimateArchiveBytes(rows, totalRows, totalBytes int64) int64 {
	if rows <= 0 || totalRows <= 0 || totalBytes <= 0 {
		return 0
	}
	if rows >= totalRows {
		return totalBytes
	}
	return int64(float64(totalBytes) * (float64(rows) / float64(totalRows)))
}

// GetDatabaseArchivePlan answers "what would archiving this table by date
// actually do", before anything is moved or deleted.
//
// It is a live read for the same reason the activity panel is: the answer is a
// distribution over the table's own data, and no stored sample carries it. The
// endpoint never writes -- it selects a cutoff column, buckets the table by
// month, and, when the caller names a cutoff, counts what sits before it. The
// deletion is a separate, explicit act.
//
// @ID          getDatabaseArchivePlan
// @Summary     Preview archiving a table's history to S3
// @Description Computes what archiving old rows of one table would free: picks the timestamp column a cutoff can be expressed in, buckets the table by month so a cutoff date can be chosen, and — when the cutoff query parameter is given — reports how many rows sit before it and roughly how many bytes they occupy including indexes. Live read against the tenant instance; nothing is exported or deleted.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       name      path     string true  "Database resource name"
// @Param       table     path     string true  "Table name"
// @Param       schema    query    string false "Schema name, defaults to public"
// @Param       cutoff    query    string false "Cutoff date (YYYY-MM-DD): rows strictly older than this are counted"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/tables/{table}/archive-plan [get]
func (h *Handler) GetDatabaseArchivePlan(c *gin.Context) {
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
	var cutoff *time.Time
	if raw := strings.TrimSpace(c.Query("cutoff")); raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			respondError(c, http.StatusBadRequest, "cutoff must be a date in YYYY-MM-DD form")
			return
		}
		cutoff = &t
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbArchivePlanTimeout)
	defer cancel()

	conn, err := h.connectToTenantDB(ctx, target.Shard, target.Datname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot reach the database instance right now")
		return
	}
	defer conn.Close(context.Background())

	qualified := pgx.Identifier{schema, relname}.Sanitize()

	var totalRows, totalBytes int64
	if err := conn.QueryRow(ctx,
		`SELECT GREATEST(c.reltuples, 0)::bigint, pg_total_relation_size(c.oid)
		   FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')`,
		schema, relname).Scan(&totalRows, &totalBytes); err != nil {
		respondNotFound(c)
		return
	}

	cols, err := archiveColumnsOf(ctx, conn, schema, relname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read the table definition right now")
		return
	}

	children, err := archiveBlockingChildren(ctx, conn, schema, relname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read the table's foreign keys right now")
		return
	}
	if len(children) > 0 {
		c.JSON(http.StatusOK, gin.H{
			"table":           schema + "." + relname,
			"archivable":      false,
			"reason":          archiveChildrenReason(schema, relname, children),
			"blockedBy":       children,
			"columns":         cols,
			"totalRows":       totalRows,
			"totalBytes":      totalBytes,
			"totalBytesHuman": humanBytes(totalBytes),
		})
		return
	}

	column, reason := pickCutoffColumn(cols)
	if reason != "" {
		vias, viaErr := archiveViaCandidates(ctx, conn, schema, relname)
		if viaErr != nil {
			vias = []archiveVia{}
		}
		c.JSON(http.StatusOK, gin.H{
			"table":           schema + "." + relname,
			"archivable":      false,
			"archivableVia":   len(vias) > 0,
			"reason":          reason,
			"via":             vias,
			"columns":         cols,
			"totalRows":       totalRows,
			"totalBytes":      totalBytes,
			"totalBytesHuman": humanBytes(totalBytes),
		})
		return
	}

	buckets, sampled, err := archiveHistogram(ctx, conn, qualified, column.Name, totalRows)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read the table's date distribution right now")
		return
	}

	out := gin.H{
		"table":           schema + "." + relname,
		"archivable":      true,
		"column":          column,
		"columns":         cols,
		"totalRows":       totalRows,
		"totalBytes":      totalBytes,
		"totalBytesHuman": humanBytes(totalBytes),
		"buckets":         buckets,
		"bucketsSampled":  sampled,
	}

	if cutoff != nil {
		rows, err := archiveRowsBefore(ctx, conn, qualified, column.Name, *cutoff)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "cannot count the rows before that date right now")
			return
		}
		bytes := estimateArchiveBytes(rows, totalRows, totalBytes)
		out["cutoff"] = cutoff.Format("2006-01-02")
		out["cutoffRows"] = rows
		out["cutoffBytesEstimate"] = bytes
		out["cutoffBytesEstimateHuman"] = humanBytes(bytes)
		out["remainingRows"] = totalRows - rows
	}
	c.JSON(http.StatusOK, out)
}

// archiveColumnsOf lists the table's columns with the two properties that
// decide whether one can carry a cutoff: nullability, and whether any index
// leads with it. Only a LEADING index column helps -- an index on (tenant_id,
// created_at) cannot serve a range scan on created_at alone.
func archiveColumnsOf(ctx context.Context, conn *pgx.Conn, schema, relname string) ([]archiveColumn, error) {
	rows, err := conn.Query(ctx, `
		SELECT a.attname,
		       format_type(a.atttypid, a.atttypmod),
		       a.attnotnull,
		       EXISTS (
		         SELECT 1 FROM pg_index i
		          WHERE i.indrelid = a.attrelid
		            AND i.indisvalid
		            AND i.indkey[0] = a.attnum
		       ),
		       a.attnum
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2
		   AND a.attnum > 0 AND NOT a.attisdropped
		 ORDER BY a.attnum`, schema, relname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []archiveColumn
	for rows.Next() {
		var col archiveColumn
		if err := rows.Scan(&col.Name, &col.Type, &col.NotNull, &col.Indexed, &col.Position); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// archiveHistogram buckets the table by month of its cutoff column. It samples
// large tables and counts small ones exactly; the second return value says
// which happened, so the console can label the numbers honestly rather than
// present an estimate as a count.
func archiveHistogram(ctx context.Context, conn *pgx.Conn, qualified, column string, totalRows int64) ([]archiveBucket, bool, error) {
	col := pgx.Identifier{column}.Sanitize()
	sampled := totalRows >= dbArchiveMinSampleRows

	sql := fmt.Sprintf(
		`SELECT date_trunc('month', %[1]s)::timestamptz AS month, count(*)
		   FROM %[2]s
		  WHERE %[1]s IS NOT NULL
		  GROUP BY 1 ORDER BY 1`, col, qualified)
	scale := int64(1)
	if sampled {
		sql = fmt.Sprintf(
			`SELECT date_trunc('month', %[1]s)::timestamptz AS month, count(*)
			   FROM %[2]s TABLESAMPLE SYSTEM (%[3]f)
			  WHERE %[1]s IS NOT NULL
			  GROUP BY 1 ORDER BY 1`, col, qualified, dbArchiveHistogramSamplePercent)
		scale = int64(100 / dbArchiveHistogramSamplePercent)
	}

	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, sampled, err
	}
	defer rows.Close()

	buckets := []archiveBucket{}
	for rows.Next() {
		var b archiveBucket
		var n int64
		if err := rows.Scan(&b.Month, &n); err != nil {
			return nil, sampled, err
		}
		b.Rows = n * scale
		b.Estimated = sampled
		buckets = append(buckets, b)
	}
	return buckets, sampled, rows.Err()
}

// archiveRowsBefore counts exactly what a cutoff would take. This one is not
// sampled: it is the number the console shows next to a button that deletes
// data, and an estimate has no business there.
func archiveRowsBefore(ctx context.Context, conn *pgx.Conn, qualified, column string, cutoff time.Time) (int64, error) {
	col := pgx.Identifier{column}.Sanitize()
	var n int64
	err := conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*) FROM %s WHERE %s < $1`, qualified, col), cutoff).Scan(&n)
	return n, err
}
