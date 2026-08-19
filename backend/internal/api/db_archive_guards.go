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
	// Enabled is pg_trigger.tgenabled as found. The delete phase suspends a
	// trigger guard for the length of one batch and must put it back in the
	// state the tenant had it in, not in the default one: a trigger left at "O"
	// that was declared ENABLE ALWAYS would stop firing under logical
	// replication, and nothing in the tenant's schema would say why. Empty for
	// a rule, which is not suspendable.
	Enabled string `json:"-"`
}

// archiveGuardTrigger is the Kind of a guard the delete phase can suspend.
const (
	archiveGuardBeforeDelete    = "before delete trigger"
	archiveGuardInsteadOfDelete = "instead of delete trigger"
	archiveGuardRule            = "on delete do instead rule"
)

// archiveGuardSuspendable reports whether the delete phase can take this guard
// out of the way for the length of one batch.
//
// Triggers can: ALTER TABLE ... DISABLE TRIGGER names exactly one of them and
// leaves the foreign keys (internal triggers) enforcing. A DO INSTEAD rule
// cannot: rules are rewrite rules, there is no per-statement suspension for
// them, and dropping one is a schema change the platform has no business
// making. Pure.
func archiveGuardSuspendable(g archiveDeleteGuard) bool {
	return g.Kind != archiveGuardRule
}

// archiveSuspendableGuards and archiveBlockingGuards split what the delete
// phase can work around from what still refuses the run. Pure.
func archiveSuspendableGuards(guards []archiveDeleteGuard) []archiveDeleteGuard {
	out := []archiveDeleteGuard{}
	for _, g := range guards {
		if archiveGuardSuspendable(g) {
			out = append(out, g)
		}
	}
	return out
}

func archiveBlockingGuards(guards []archiveDeleteGuard) []archiveDeleteGuard {
	out := []archiveDeleteGuard{}
	for _, g := range guards {
		if !archiveGuardSuspendable(g) {
			out = append(out, g)
		}
	}
	return out
}

// archiveGuardSuspendSQL and archiveGuardRestoreSQL are the two halves of one
// batch's suspension.
//
// Restore is not "ENABLE TRIGGER": that word means tgenabled = 'O', and a
// trigger the tenant declared ENABLE ALWAYS or ENABLE REPLICA would come back
// weaker than it went in. Pure.
func archiveGuardSuspendSQL(schema, table string, g archiveDeleteGuard) string {
	return fmt.Sprintf("ALTER TABLE %s DISABLE TRIGGER %s",
		pgx.Identifier{schema, table}.Sanitize(), pgx.Identifier{g.Name}.Sanitize())
}

func archiveGuardRestoreSQL(schema, table string, g archiveDeleteGuard) string {
	word := "ENABLE"
	switch g.Enabled {
	case "A":
		word = "ENABLE ALWAYS"
	case "R":
		word = "ENABLE REPLICA"
	}
	return fmt.Sprintf("ALTER TABLE %s %s TRIGGER %s",
		pgx.Identifier{schema, table}.Sanitize(), word, pgx.Identifier{g.Name}.Sanitize())
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
		            ELSE 'before delete trigger' END,
		       t.tgenabled::text
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid = t.tgrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2
		   AND NOT t.tgisinternal
		   AND t.tgenabled <> 'D'
		   AND (t.tgtype & 8) > 0
		   AND (t.tgtype & 66) > 0
		UNION ALL
		SELECT r.rulename, 'on delete do instead rule', ''
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
		if err := rows.Scan(&g.Name, &g.Kind, &g.Enabled); err != nil {
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
		"%s.%s cannot be archived: %s decides what a delete on this table does, so the rows would be exported and then kept. A rewrite rule cannot be suspended for one statement the way a trigger can, so this one has to be dropped by whoever owns the schema.",
		schema, table, strings.Join(names, ", "))
}
