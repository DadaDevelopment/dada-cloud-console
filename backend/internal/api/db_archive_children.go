package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// archiveChildRef is one table that still points at the rows a run wants to
// delete, and the foreign key it points with.
type archiveChildRef struct {
	Table   string `json:"table"`
	Column  string `json:"column"`
	Rows    int64  `json:"rows"`
	Cascade bool   `json:"cascade"`
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
func archiveBlockingChildren(ctx context.Context, conn *pgx.Conn, schema, table string) ([]archiveChildRef, error) {
	rows, err := conn.Query(ctx, `
		SELECT ch.relname, a.attname,
		       GREATEST(ch.reltuples, 0)::bigint,
		       con.confdeltype = 'c'
		  FROM pg_constraint con
		  JOIN pg_class ch ON ch.oid = con.conrelid
		  JOIN pg_class pa ON pa.oid = con.confrelid
		  JOIN pg_namespace pn ON pn.oid = pa.relnamespace
		  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
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
		if err := rows.Scan(&ref.Table, &ref.Column, &ref.Rows, &ref.Cascade); err != nil {
			return nil, err
		}
		if ref.Rows <= 0 {
			continue
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// archiveChildrenReason is the sentence the console shows instead of a run that
// would die in its delete phase. Pure.
func archiveChildrenReason(schema, table string, refs []archiveChildRef) string {
	if len(refs) == 0 {
		return ""
	}
	names := make([]string, 0, len(refs))
	cascade := false
	for _, ref := range refs {
		names = append(names, fmt.Sprintf("%s.%s (%s)", schema, ref.Table, ref.Column))
		cascade = cascade || ref.Cascade
	}
	msg := fmt.Sprintf(
		"%s.%s cannot be archived yet: %s still point at its rows, so every delete would be refused by the foreign key. Archive those tables first.",
		schema, table, strings.Join(names, ", "))
	if cascade {
		msg += " One of them deletes on cascade, which would remove rows no archive holds."
	}
	return msg
}
