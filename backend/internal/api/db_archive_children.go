package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// archiveChildRef is one table that still points at the rows a run wants to
// delete, and the foreign key it points with.
// ParentColumn is the referenced column on the archived table; it is what lets
// a candidate be re-counted against the rows a run would actually delete rather
// than against the child's whole population. Estimated marks a count that came
// from the catalog because the exact one did not finish in time, so a refusal
// carrying it is a precaution rather than a measurement.
type archiveChildRef struct {
	Table        string `json:"table"`
	Column       string `json:"column"`
	Rows         int64  `json:"rows"`
	Cascade      bool   `json:"cascade"`
	ParentColumn string `json:"-"`
	Estimated    bool   `json:"estimated,omitempty"`
}

// archiveBlockingChildren lists the referencing tables that stand between a run
// and its delete phase.
//
// A parent table cannot be archived while a child still references its rows:
// the export and the verify both succeed, and then every delete batch fails on
// the foreign key -- the run dies after writing an archive nobody asked for.
// The one real case this was found on lost twelve minutes and a 4 GB Parquet
// object to that ordering before anything said the word "children".
//
// A cascade key blocks too, and for a worse reason: the delete would succeed
// and take the child's rows with it, and those rows were never exported to any
// archive. Only children that actually hold rows are reported, so a table with
// an empty audit child is not declared unarchivable over nothing.
//
// The counts here are the child's whole population, which answers "could this
// key ever block" and not "does it block this run". Callers that know the
// cutoff must narrow the list with archiveChildrenInWindow before refusing.
func archiveBlockingChildren(ctx context.Context, conn *pgx.Conn, schema, table string) ([]archiveChildRef, error) {
	rows, err := conn.Query(ctx, `
		SELECT ch.relname, a.attname, af.attname,
		       GREATEST(ch.reltuples, 0)::bigint,
		       con.confdeltype = 'c'
		  FROM pg_constraint con
		  JOIN pg_class ch ON ch.oid = con.conrelid
		  JOIN pg_class pa ON pa.oid = con.confrelid
		  JOIN pg_namespace pn ON pn.oid = pa.relnamespace
		  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
		  JOIN pg_attribute af ON af.attrelid = con.confrelid AND af.attnum = con.confkey[1]
		 WHERE con.contype = 'f'
		   AND pn.nspname = $1 AND pa.relname = $2
		   AND ch.oid <> pa.oid
		 ORDER BY ch.relname, a.attname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []archiveChildRef{}
	for rows.Next() {
		var ref archiveChildRef
		if err := rows.Scan(&ref.Table, &ref.Column, &ref.ParentColumn, &ref.Rows, &ref.Cascade); err != nil {
			return nil, err
		}
		if ref.Rows <= 0 {
			continue
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// archiveChildrenInWindow keeps only the children that hold rows pointing at
// what this run would delete, counting them instead of guessing.
//
// The catalog count answers a different question than the run asks. A child
// keeps referencing the parent's recent rows forever, so a table that has been
// archived down to nothing older than the cutoff still reports millions of
// rows, and the run is refused over rows it would never touch. On the first
// customer database to fill a shard this was not a corner case: after the
// oldest rows were archived away, two of the three keys blocking a 5 GB table
// held exactly zero rows in the window, and a key holding three million rows
// blocked a 1 GB table it could no longer collide with.
//
// The decision and the number are two different questions, and only the first
// one has to be exact. Whether the key blocks is asked with EXISTS, which the
// planner can answer from the first matching row: on the 5 GB table this fix
// was written for, that is 130 ms against 2.5 s for the count, and the count
// was the half that kept missing its deadline and marking every key Estimated.
//
// The count runs only for a key that already blocks, purely so the refusal can
// name a size. A count that does not finish in time leaves the catalog estimate
// in place, marked Estimated, which weakens the sentence and not the verdict.
// An EXISTS that fails keeps the candidate blocking: refusing on an unfinished
// measurement is the safe direction, because the alternative is a run that
// exports gigabytes and dies on the foreign key.
func archiveChildrenInWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, refs []archiveChildRef) []archiveChildRef {
	out := []archiveChildRef{}
	for _, ref := range refs {
		blocks, err := archiveChildBlocksWindow(ctx, conn, r, ref)
		if err == nil && !blocks {
			continue
		}
		if err != nil {
			ref.Estimated = true
			out = append(out, ref)
			continue
		}
		if exact, err := archiveChildRowsInWindow(ctx, conn, r, ref); err == nil {
			ref.Rows = exact
		} else {
			ref.Estimated = true
		}
		out = append(out, ref)
	}
	return out
}

// archiveChildBlocksWindow answers whether any child row points at a row this
// run would delete. It carries the same predicate as the count and the delete,
// and stops at the first hit.
func archiveChildBlocksWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, ref archiveChildRef) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, dbArchiveChildProbeTimeout)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT EXISTS (SELECT 1 FROM %s c JOIN %s tgt ON tgt.%s = c.%s WHERE %s)`,
		pgx.Identifier{r.Schema, ref.Table}.Sanitize(),
		pgx.Identifier{r.Schema, r.Table}.Sanitize(),
		pgx.Identifier{ref.ParentColumn}.Sanitize(),
		pgx.Identifier{ref.Column}.Sanitize(),
		archiveWhereSQL(r, "tgt"))

	var blocks bool
	if err := conn.QueryRow(ctx, query, r.Cutoff).Scan(&blocks); err != nil {
		return false, err
	}
	return blocks, nil
}

// archiveChildRowsInWindow counts the child rows whose parent this run would
// delete. The predicate is archiveWhereSQL so that the count and the delete
// cannot disagree about which rows the archive covers.
func archiveChildRowsInWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, ref archiveChildRef) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, dbArchiveChildCountTimeout)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT count(*) FROM %s c JOIN %s tgt ON tgt.%s = c.%s WHERE %s`,
		pgx.Identifier{r.Schema, ref.Table}.Sanitize(),
		pgx.Identifier{r.Schema, r.Table}.Sanitize(),
		pgx.Identifier{ref.ParentColumn}.Sanitize(),
		pgx.Identifier{ref.Column}.Sanitize(),
		archiveWhereSQL(r, "tgt"))

	var n int64
	if err := conn.QueryRow(ctx, query, r.Cutoff).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// archiveChildrenReason is the sentence the console shows instead of a run that
// would die in its delete phase. Pure.
func archiveChildrenReason(schema, table string, refs []archiveChildRef) string {
	if len(refs) == 0 {
		return ""
	}
	names := make([]string, 0, len(refs))
	cascade := false
	estimated := false
	for _, ref := range refs {
		names = append(names, fmt.Sprintf("%s.%s (%s, %d rows)", schema, ref.Table, ref.Column, ref.Rows))
		cascade = cascade || ref.Cascade
		estimated = estimated || ref.Estimated
	}
	msg := fmt.Sprintf(
		"%s.%s cannot be archived yet: %s still point at the rows this cutoff would delete, so every delete would be refused by the foreign key. Archive those tables first.",
		schema, table, strings.Join(names, ", "))
	if cascade {
		msg += " One of them deletes on cascade, which would remove rows no archive holds."
	}
	if estimated {
		msg += " At least one count is the table's total rather than a measurement, because counting the exact overlap took too long."
	}
	return msg
}
