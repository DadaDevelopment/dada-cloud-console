package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// archiveDeleteGuard is one rule the tenant put on the table that decides what a
// DELETE does, instead of letting it delete.
type archiveDeleteGuard struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// archiveDeleteGuards lists the tenant's own guards that stand between a run and
// its delete phase: enabled BEFORE-DELETE and INSTEAD-OF-DELETE triggers, and
// ON DELETE DO INSTEAD rewrite rules.
//
// These are the two ways a table can be append-only without saying so in its
// schema. A BEFORE-DELETE trigger that raises turns every delete batch into an
// error after the export already happened; a DO INSTEAD rule is worse, because
// the delete reports success and removes nothing, so the run would call a table
// archived while every row is still there.
//
// The one real case this was found on refused with "v0.4 history is append-only
// (SQLSTATE P0001)" after exporting 5.6 million rows to Parquet -- the whole
// export was work nobody could use. AFTER-DELETE triggers are deliberately not
// reported: an audit trigger is the common shape there, and refusing on it
// would block tables that archive perfectly well.
func archiveDeleteGuards(ctx context.Context, conn *pgx.Conn, schema, table string) ([]archiveDeleteGuard, error) {
	rows, err := conn.Query(ctx, `
		SELECT t.tgname,
		       CASE WHEN (t.tgtype & 64) > 0 THEN 'instead of delete trigger'
		            ELSE 'before delete trigger' END
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid = t.tgrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2
		   AND NOT t.tgisinternal
		   AND t.tgenabled <> 'D'
		   AND (t.tgtype & 8) > 0
		   AND (t.tgtype & 66) > 0
		UNION ALL
		SELECT r.rulename, 'on delete do instead rule'
		  FROM pg_rewrite r
		  JOIN pg_class c ON c.oid = r.ev_class
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2
		   AND r.ev_type = '4' AND r.is_instead
		 ORDER BY 1`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []archiveDeleteGuard{}
	for rows.Next() {
		var g archiveDeleteGuard
		if err := rows.Scan(&g.Name, &g.Kind); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// archiveDeleteGuardsReason is the sentence the console shows instead of a run
// that would export rows the delete phase is not allowed to remove. Pure.
func archiveDeleteGuardsReason(schema, table string, guards []archiveDeleteGuard) string {
	if len(guards) == 0 {
		return ""
	}
	names := make([]string, 0, len(guards))
	for _, g := range guards {
		names = append(names, fmt.Sprintf("%s (%s)", g.Name, g.Kind))
	}
	return fmt.Sprintf(
		"%s.%s cannot be archived: %s decides what a delete on this table does, so the rows would be exported and then kept. Drop or disable it if these rows are meant to be removable.",
		schema, table, strings.Join(names, ", "))
}
