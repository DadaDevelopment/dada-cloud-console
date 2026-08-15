package api

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// archiveViaCutoffAlias is the column the export adds to a derived archive so
// the Parquet object carries the timestamp its rows were cut on.
//
// Without it the archive of a child table would hold no time at all: the cutoff
// column lives on the parent, and the verify step -- the one thing standing
// between an export and a delete -- would have nothing to hold "no row at or
// after the cutoff leaked in" against. The name is prefixed so it cannot
// collide with a column the child already has.
const archiveViaCutoffAlias = "_archive_cutoff_at"

// archiveIsDerived reports whether the run cuts on a parent table's column
// reached through a foreign key, rather than on a column of its own. Pure.
func archiveIsDerived(r archiveRun) bool {
	return r.ViaTable != "" && r.ViaFK != "" && r.ViaPK != ""
}

// archiveCutoffColumnInArchive names the column the exported Parquet carries the
// cutoff timestamp in, which for a derived run is the aliased parent column.
// Pure.
func archiveCutoffColumnInArchive(r archiveRun) string {
	if archiveIsDerived(r) {
		return archiveViaCutoffAlias
	}
	return r.CutoffColumn
}

// archiveWhereSQL is THE predicate of a run: the plan counts it, the delete
// removes it and the tail recount decides on it. One builder, because three
// phases that each spelled the predicate themselves could disagree about which
// rows the archive covers, and the phase that would discover the disagreement
// is the one that deletes.
//
// The cutoff is passed as $1. alias is how the archived table is named in the
// enclosing statement. Pure.
func archiveWhereSQL(r archiveRun, alias string) string {
	col := pgx.Identifier{r.CutoffColumn}.Sanitize()
	if !archiveIsDerived(r) {
		return fmt.Sprintf(`%s.%s < $1`, alias, col)
	}
	return fmt.Sprintf(
		`EXISTS (SELECT 1 FROM %s p WHERE p.%s = %s.%s AND p.%s < $1)`,
		pgx.Identifier{r.Schema, r.ViaTable}.Sanitize(),
		pgx.Identifier{r.ViaPK}.Sanitize(),
		alias, pgx.Identifier{r.ViaFK}.Sanitize(),
		col)
}

// archiveExportSelectSQL is the DuckDB SELECT the export copies to Parquet.
//
// A derived run joins the parent and carries its timestamp into the archive
// under archiveViaCutoffAlias. A child row whose foreign key points at nothing
// is neither archived nor deleted: an inner join is the conservative reading of
// "older than the cutoff" for a row whose age nothing can establish. Pure.
func archiveExportSelectSQL(r archiveRun) string {
	child := duckDBIdentifier(r.Schema) + "." + duckDBIdentifier(r.Table)
	cutoff := r.Cutoff.Format("2006-01-02")
	col := duckDBIdentifier(r.CutoffColumn)
	if !archiveIsDerived(r) {
		return fmt.Sprintf("SELECT * FROM src.%s WHERE %s < DATE '%s'", child, col, cutoff)
	}
	return fmt.Sprintf(
		"SELECT c.*, p.%s AS %s FROM src.%s c JOIN src.%s p ON p.%s = c.%s WHERE p.%s < DATE '%s'",
		col, duckDBIdentifier(archiveViaCutoffAlias),
		child,
		duckDBIdentifier(r.Schema)+"."+duckDBIdentifier(r.ViaTable),
		duckDBIdentifier(r.ViaPK), duckDBIdentifier(r.ViaFK),
		col, cutoff)
}

// archiveVia is one way a table without a time column of its own can still be
// cut: through this foreign key, on that column of the parent it points at.
type archiveVia struct {
	Table  string `json:"table"`
	FK     string `json:"fk"`
	PK     string `json:"pk"`
	Column string `json:"column"`
}

// archiveViaOf reads a run's derived cutoff back as a candidate. Pure.
func archiveViaOf(r archiveRun) archiveVia {
	return archiveVia{Table: r.ViaTable, FK: r.ViaFK, PK: r.ViaPK, Column: r.CutoffColumn}
}

// archiveViaCandidates lists the single-column foreign keys of a table whose
// parent carries a column a cutoff can be expressed in.
//
// Only single-column keys are offered: a composite key would need the predicate
// to join on every part, and the tables this exists for do not have one.
// Ambiguity is deliberately not resolved here -- "which parent dates this row"
// is a question about the tenant's data model, and a table with three dated
// parents has three defensible answers that free different rows.
func archiveViaCandidates(ctx context.Context, conn *pgx.Conn, schema, table string) ([]archiveVia, error) {
	rows, err := conn.Query(ctx, `
		SELECT a.attname, pn.nspname, pc.relname, af.attname
		  FROM pg_constraint con
		  JOIN pg_class c ON c.oid = con.conrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_class pc ON pc.oid = con.confrelid
		  JOIN pg_namespace pn ON pn.oid = pc.relnamespace
		  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
		  JOIN pg_attribute af ON af.attrelid = con.confrelid AND af.attnum = con.confkey[1]
		 WHERE con.contype = 'f'
		   AND array_length(con.conkey, 1) = 1
		   AND n.nspname = $1 AND c.relname = $2
		 ORDER BY pc.relname, a.attname`, schema, table)
	if err != nil {
		return nil, err
	}
	type link struct{ fk, schema, table, pk string }
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.fk, &l.schema, &l.table, &l.pk); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := []archiveVia{}
	for _, l := range links {
		if l.schema != schema {
			continue
		}
		cols, err := archiveColumnsOf(ctx, conn, l.schema, l.table)
		if err != nil {
			return nil, err
		}
		col, reason := pickCutoffColumn(cols)
		if reason != "" {
			continue
		}
		out = append(out, archiveVia{Table: l.table, FK: l.fk, PK: l.pk, Column: col.Name})
	}
	return out, nil
}

// validateArchiveVia refuses a derived run whose join the tenant's schema does
// not actually declare.
//
// The check is against pg_constraint rather than against column names alone: a
// caller-supplied join that Postgres does not know about would let a request
// select, and then delete, rows on a relationship nobody declared -- and the
// archive would still have been written from the same join, so nothing later
// would catch it.
func validateArchiveVia(ctx context.Context, conn *pgx.Conn, schema, table string, via archiveVia) error {
	var ok bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		      FROM pg_constraint con
		      JOIN pg_class c ON c.oid = con.conrelid
		      JOIN pg_namespace n ON n.oid = c.relnamespace
		      JOIN pg_class pc ON pc.oid = con.confrelid
		      JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
		      JOIN pg_attribute af ON af.attrelid = con.confrelid AND af.attnum = con.confkey[1]
		     WHERE con.contype = 'f' AND array_length(con.conkey, 1) = 1
		       AND n.nspname = $1 AND c.relname = $2
		       AND pc.relname = $3 AND a.attname = $4 AND af.attname = $5)`,
		schema, table, via.Table, via.FK, via.PK).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s.%s has no foreign key %s -> %s.%s",
			schema, table, via.FK, via.Table, via.PK)
	}
	cols, err := archiveColumnsOf(ctx, conn, schema, via.Table)
	if err != nil {
		return err
	}
	if !archiveColumnUsable(cols, via.Column) {
		return fmt.Errorf("column %q is not a timestamp or date column on %s.%s",
			via.Column, schema, via.Table)
	}
	return nil
}

// archiveDeleteSQL is one batch of the delete: at most dbArchiveDeleteBatch of
// the rows the archive covers, skipping any the tenant currently holds locked.
//
// The rows are addressed by ctid so the batch is bounded without an ORDER BY on
// a column a derived run does not have. Pure.
func archiveDeleteSQL(r archiveRun) string {
	return fmt.Sprintf(`
		WITH doomed AS (
		    SELECT c.ctid FROM %[1]s c WHERE %[2]s LIMIT %[3]d FOR UPDATE SKIP LOCKED
		)
		DELETE FROM %[1]s WHERE ctid IN (SELECT ctid FROM doomed)`,
		pgx.Identifier{r.Schema, r.Table}.Sanitize(),
		archiveWhereSQL(r, "c"), dbArchiveDeleteBatch)
}

// archiveRowsMatching counts exactly what the run's predicate covers.
//
// Not sampled and not estimated: it is the number the verify phase holds the
// export to before anything is deleted.
func archiveRowsMatching(ctx context.Context, conn *pgx.Conn, r archiveRun) (int64, error) {
	var n int64
	err := conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*) FROM %s c WHERE %s`,
		pgx.Identifier{r.Schema, r.Table}.Sanitize(), archiveWhereSQL(r, "c")),
		r.Cutoff).Scan(&n)
	return n, err
}
