package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dbArchiveHistoryLimit caps how many runs the history endpoint returns. An
// archive is a rare, deliberate act; a database with more finished runs than
// this has a story an operator should read in the table, not in a panel.
const dbArchiveHistoryLimit = 50

// startArchiveRequest is the caller's side of "archive this table up to this
// date". The cutoff is a date rather than a timestamp because that is what the
// plan endpoint buckets by and what the confirmation text can state without
// ambiguity about time zones.
type startArchiveRequest struct {
	Table  string      `json:"table"`
	Schema string      `json:"schema"`
	Cutoff string      `json:"cutoff"`
	Via    *archiveVia `json:"via,omitempty"`
}

// archiveRunView is one run as the console shows it.
type archiveRunView struct {
	ID            uuid.UUID      `json:"id"`
	Table         string         `json:"table"`
	Column        string         `json:"column"`
	Via           *archiveVia    `json:"via,omitempty"`
	Cutoff        string         `json:"cutoff"`
	Phase         string         `json:"phase"`
	PlannedRows   int64          `json:"plannedRows"`
	DeletedRows   int64          `json:"deletedRows"`
	BytesEstimate int64          `json:"bytesEstimate"`
	BytesFreed    int64          `json:"bytesFreed"`
	FreedHuman    string         `json:"freedHuman"`
	S3URI         string         `json:"s3Uri"`
	Manifest      map[string]any `json:"manifest"`
	Error         string         `json:"error,omitempty"`
	Auto          bool           `json:"auto"`
	RequestedBy   string         `json:"requestedBy,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	FinishedAt    *time.Time     `json:"finishedAt,omitempty"`
}

// StartDatabaseArchive queues an archive of one table's history to S3.
//
// It validates against the live table rather than trusting the caller's plan:
// the preview the owner read may be minutes old, and a column that has since
// been dropped or retyped must stop the run here, where a human is waiting for
// the answer, rather than in the worker. The endpoint itself exports nothing --
// it writes a pending run, and the archive worker drives the phases.
//
// A second request for a table that already has an open run is a conflict, not
// a second run: the partial unique index in the schema is what makes that
// guarantee hold across replicas, and answering 409 turns it into a sentence
// the console can show.
//
// @ID          startDatabaseArchive
// @Summary     Archive a table's history to S3 as Parquet
// @Description Queues an archive run for one table: rows strictly older than the cutoff date are exported to Parquet on the project's archive bucket, verified against the exported object, deleted from the table, and the space returned to the filesystem by rewriting the table. The database backup is untouched and still holds the archived rows. Returns the queued run; progress is reported by the archive-runs history endpoint.
// @Tags        database
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string             true "Project UUID"
// @Param       envId     path     string             true "Environment UUID"
// @Param       name      path     string             true "Database resource name"
// @Param       request   body     startArchiveRequest true "Table, schema and cutoff date"
// @Success     202       {object} archiveRunView
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/archive-runs [post]
func (h *Handler) StartDatabaseArchive(c *gin.Context) {
	claims, _ := auth.GetClaims(c)
	projectID, envID, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}

	var req startArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "request body must be JSON with table and cutoff")
		return
	}
	relname := strings.TrimSpace(req.Table)
	if relname == "" {
		respondError(c, http.StatusBadRequest, "table is required")
		return
	}
	schema := strings.TrimSpace(req.Schema)
	if schema == "" {
		schema = "public"
	}
	cutoff, err := time.Parse("2006-01-02", strings.TrimSpace(req.Cutoff))
	if err != nil {
		respondError(c, http.StatusBadRequest, "cutoff must be a date in YYYY-MM-DD form")
		return
	}
	if !cutoff.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
		respondError(c, http.StatusBadRequest, "cutoff must be in the past: archiving today's rows would remove data the application is still writing")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbArchivePlanTimeout)
	defer cancel()

	conn, err := h.connectToTenantDB(ctx, target.Shard, target.Datname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot reach the database instance right now")
		return
	}
	defer conn.Close(context.Background())

	cols, err := archiveColumnsOf(ctx, conn, schema, relname)
	if err != nil || len(cols) == 0 {
		respondNotFound(c)
		return
	}
	candidates, err := archiveBlockingChildren(ctx, conn, schema, relname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read the table's foreign keys right now")
		return
	}
	guards, err := archiveDeleteGuards(ctx, conn, schema, relname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read the table's delete rules right now")
		return
	}
	if len(guards) > 0 {
		respondError(c, http.StatusConflict, archiveDeleteGuardsReason(schema, relname, guards))
		return
	}
	via, columnName, reason := resolveArchiveCutoff(ctx, conn, schema, relname, cols, req.Via)
	if reason != "" {
		respondError(c, http.StatusBadRequest, reason)
		return
	}
	cctx, ccancel := context.WithTimeout(ctx, dbArchiveChildrenAPIBudget)
	children := archiveChildrenInWindow(cctx, conn, archiveRun{
		Schema:       schema,
		Table:        relname,
		CutoffColumn: columnName,
		ViaTable:     via.Table,
		ViaFK:        via.FK,
		ViaPK:        via.PK,
		Cutoff:       cutoff,
	}, candidates, dbArchiveAPIProbeBudget)
	ccancel()
	if blocking := archiveChildrenDecided(children); len(blocking) > 0 {
		respondError(c, http.StatusConflict, archiveChildrenReason(schema, relname, blocking))
		return
	}

	var id uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO db_archive_runs
		     (project_id, environment_id, resource_name, datname, shard,
		      schema_name, table_name, cutoff_column,
		      cutoff_via_table, cutoff_via_fk, cutoff_via_pk, cutoff_date, requested_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id`,
		projectID, envID, c.Param("name"), target.Datname, target.Shard,
		schema, relname, columnName,
		via.Table, via.FK, via.PK, cutoff, claims.Email).Scan(&id)
	if err != nil {
		if isArchiveUniqueViolation(err) {
			respondError(c, http.StatusConflict, "this table already has an archive in progress")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to queue the archive")
		return
	}

	view := archiveRunView{
		ID:        id,
		Table:     schema + "." + relname,
		Column:    columnName,
		Cutoff:    cutoff.Format("2006-01-02"),
		Phase:     dbArchivePending,
		Manifest:  map[string]any{},
		CreatedAt: time.Now().UTC(),
	}
	if via.Table != "" {
		view.Via = &via
	}
	c.JSON(http.StatusAccepted, view)
}

// resolveArchiveCutoff decides what a run will cut on: a column of the table
// itself, or a parent's column reached through a foreign key.
//
// The derived path exists because the tables that fill a shard are often the
// ones a cutoff could never select: on the first customer database to fill one,
// the largest table holds 10 GB of 30 and has no time column at all. Deriving
// is offered rather than assumed -- a caller-named join wins, a table with
// exactly one dated parent is unambiguous enough to take silently, and several
// dated parents is a question only the tenant can answer, so the answer is the
// list of choices instead of a guess.
func resolveArchiveCutoff(ctx context.Context, conn *pgx.Conn, schema, table string, cols []archiveColumn, requested *archiveVia) (archiveVia, string, string) {
	if requested != nil && requested.Table != "" {
		if requested.FK == "" || requested.PK == "" || requested.Column == "" {
			return archiveVia{}, "", "via needs table, fk, pk and column"
		}
		if err := validateArchiveVia(ctx, conn, schema, table, *requested); err != nil {
			return archiveVia{}, "", err.Error()
		}
		return *requested, requested.Column, ""
	}
	if column, reason := pickCutoffColumn(cols); reason == "" {
		return archiveVia{}, column.Name, ""
	} else {
		candidates, err := archiveViaCandidates(ctx, conn, schema, table)
		if err != nil {
			return archiveVia{}, "", reason
		}
		switch len(candidates) {
		case 0:
			return archiveVia{}, "", reason + ", and no foreign key leads to a table that has one"
		case 1:
			return candidates[0], candidates[0].Column, ""
		default:
			return archiveVia{}, "", reason + ": pick which parent dates these rows by passing via, one of " + describeArchiveVias(candidates)
		}
	}
}

// describeArchiveVias renders the derived cutoffs a table offers as one line a
// console can show and a caller can copy into a via. Pure.
func describeArchiveVias(vias []archiveVia) string {
	out := make([]string, 0, len(vias))
	for _, v := range vias {
		out = append(out, v.FK+" -> "+v.Table+"."+v.PK+" on "+v.Column)
	}
	return strings.Join(out, "; ")
}

// ListDatabaseArchiveRuns returns one database's archive history, newest first.
//
// It is the only place a finished archive's manifest surfaces, and the manifest
// is the point: it tells the owner where the rows went and how to read them
// back. Open runs are in the same list so a panel can show progress without a
// second endpoint.
//
// @ID          listDatabaseArchiveRuns
// @Summary     List a database's archive runs
// @Description Returns archive runs for one managed database, newest first, including in-progress ones. A finished run carries the manifest describing where its Parquet lives and how to read it.
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
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/archive-runs [get]
func (h *Handler) ListDatabaseArchiveRuns(c *gin.Context) {
	projectID, envID, _, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, schema_name, table_name, cutoff_column,
		        cutoff_via_table, cutoff_via_fk, cutoff_via_pk, cutoff_date, phase,
		        planned_rows, deleted_rows, bytes_estimate, bytes_freed,
		        s3_uri, manifest, error, auto, requested_by, created_at, finished_at
		   FROM db_archive_runs
		  WHERE project_id = $1 AND environment_id = $2 AND resource_name = $3
		  ORDER BY created_at DESC
		  LIMIT $4`,
		projectID, envID, c.Param("name"), dbArchiveHistoryLimit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load archive history")
		return
	}
	defer rows.Close()

	out := []archiveRunView{}
	for rows.Next() {
		var (
			v          archiveRunView
			schema     string
			table      string
			via        archiveVia
			cutoff     time.Time
			manifest   []byte
			finishedAt *time.Time
		)
		if err := rows.Scan(&v.ID, &schema, &table, &v.Column,
			&via.Table, &via.FK, &via.PK, &cutoff, &v.Phase,
			&v.PlannedRows, &v.DeletedRows, &v.BytesEstimate, &v.BytesFreed,
			&v.S3URI, &manifest, &v.Error, &v.Auto, &v.RequestedBy,
			&v.CreatedAt, &finishedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read archive history")
			return
		}
		v.Table = schema + "." + table
		if via.Table != "" {
			via.Column = v.Column
			v.Via = &via
		}
		v.Cutoff = cutoff.Format("2006-01-02")
		v.FreedHuman = humanBytes(v.BytesFreed)
		v.Manifest = map[string]any{}
		if len(manifest) > 0 {
			_ = json.Unmarshal(manifest, &v.Manifest)
		}
		v.FinishedAt = finishedAt
		out = append(out, v)
	}
	if rows.Err() != nil {
		respondError(c, http.StatusInternalServerError, "failed to read archive history")
		return
	}

	c.JSON(http.StatusOK, gin.H{"runs": out})
}

// isArchiveUniqueViolation reports whether an insert lost a race against the
// partial unique index that allows one open run per table, which the API turns
// into 409 rather than 500. The SQLSTATE is matched on both the wrapped pgx
// error and its rendered form, since only the message survives some wrappings.
func isArchiveUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	type sqlStater interface{ SQLState() string }
	if pgErr, ok := err.(sqlStater); ok {
		return pgErr.SQLState() == "23505"
	}
	return strings.Contains(err.Error(), "23505")
}
