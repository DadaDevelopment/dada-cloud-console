package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedGuardedTables builds the three shapes the guard has to tell apart: a
// table an append-only trigger vetoes, a table whose only trigger runs after
// the delete, and a table with no rules at all.
func seedGuardedTables(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	ctx := context.Background()
	schema := "archive_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	q := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+q); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA `+q+` CASCADE`)
	})
	stmts := []string{
		`CREATE FUNCTION ` + q + `.reject_history() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RAISE EXCEPTION 'history is append-only'; END $$`,
		`CREATE FUNCTION ` + q + `.note_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RETURN OLD; END $$`,
		`CREATE TABLE ` + q + `.history (id BIGINT PRIMARY KEY, observed_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE ` + q + `.audited (id BIGINT PRIMARY KEY, observed_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE ` + q + `.plain (id BIGINT PRIMARY KEY, observed_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TRIGGER trg_history_append_only BEFORE DELETE OR UPDATE ON ` + q + `.history
		   FOR EACH ROW EXECUTE FUNCTION ` + q + `.reject_history()`,
		`CREATE TRIGGER trg_audited_note AFTER DELETE ON ` + q + `.audited
		   FOR EACH ROW EXECUTE FUNCTION ` + q + `.note_delete()`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return schema
}

// TestAppendOnlyTableIsRefusedBeforeTheExport pins the second ordering failure
// the real customer database produced: the run exported 5.6 million rows and
// only then learned the tenant's own trigger forbids deleting any of them.
func TestAppendOnlyTableIsRefusedBeforeTheExport(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedGuardedTables(t, conn)

	guards, err := archiveDeleteGuards(ctx, conn, schema, "history")
	if err != nil {
		t.Fatalf("read delete guards: %v", err)
	}
	if len(guards) != 1 || guards[0].Name != "trg_history_append_only" {
		t.Fatalf("delete guards = %+v, want the append-only trigger", guards)
	}
	if msg := archiveDeleteGuardsReason(schema, "history", guards); !strings.Contains(msg, "trg_history_append_only") {
		t.Fatalf("the refusal does not name the trigger to drop: %s", msg)
	}

	after, err := archiveDeleteGuards(ctx, conn, schema, "audited")
	if err != nil {
		t.Fatalf("read guards of the audited table: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("an after-delete trigger must not block an archive, got %+v", after)
	}

	if _, err := conn.Exec(ctx,
		`ALTER TABLE `+pgx.Identifier{schema, "history"}.Sanitize()+` DISABLE TRIGGER trg_history_append_only`); err != nil {
		t.Fatalf("disable the trigger: %v", err)
	}
	disabled, err := archiveDeleteGuards(ctx, conn, schema, "history")
	if err != nil {
		t.Fatalf("read guards after disabling: %v", err)
	}
	if len(disabled) != 0 {
		t.Fatalf("a disabled trigger cannot refuse a delete, got %+v", disabled)
	}

	plain, err := archiveDeleteGuards(ctx, conn, schema, "plain")
	if err != nil {
		t.Fatalf("read guards of the plain table: %v", err)
	}
	if len(plain) != 0 {
		t.Fatalf("a table with no rules must not be blocked, got %+v", plain)
	}
}

// TestDoInsteadRuleIsRefused covers the quieter half of the guard: a rewrite
// rule that makes DELETE succeed while removing nothing, which would let a run
// report a table archived with every row still in place.
func TestDoInsteadRuleIsRefused(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedGuardedTables(t, conn)

	if _, err := conn.Exec(ctx,
		`CREATE RULE keep_everything AS ON DELETE TO `+pgx.Identifier{schema, "plain"}.Sanitize()+` DO INSTEAD NOTHING`); err != nil {
		t.Fatalf("create the rule: %v", err)
	}
	guards, err := archiveDeleteGuards(ctx, conn, schema, "plain")
	if err != nil {
		t.Fatalf("read delete guards: %v", err)
	}
	if len(guards) != 1 || guards[0].Name != "keep_everything" {
		t.Fatalf("delete guards = %+v, want the do-instead rule", guards)
	}
	if msg := archiveDeleteGuardsReason(schema, "plain", guards); !strings.Contains(msg, "keep_everything") {
		t.Fatalf("the refusal does not name the rule: %s", msg)
	}
}

// TestSuspendedGuardDeletesAndRestores is the whole feature in one test: rows a
// BEFORE DELETE trigger refuses are removed anyway, and the trigger is back
// guarding the table the moment the batch commits.
func TestSuspendedGuardDeletesAndRestores(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedGuardedTables(t, conn)
	hist := pgx.Identifier{schema, "history"}.Sanitize()

	if _, err := conn.Exec(ctx, `INSERT INTO `+hist+` (id, observed_at) VALUES
		(1, '2026-01-01'), (2, '2026-01-02'), (3, '2026-09-01')`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM `+hist+` WHERE id = 1`); err == nil {
		t.Fatal("the trigger let a plain delete through; the test proves nothing")
	}

	guards, err := archiveDeleteGuards(ctx, conn, schema, "history")
	if err != nil {
		t.Fatalf("read delete guards: %v", err)
	}
	if len(archiveBlockingGuards(guards)) != 0 {
		t.Fatalf("a trigger guard must be suspendable, got %+v", guards)
	}

	run := archiveRun{
		Schema: schema, Table: "history", CutoffColumn: "observed_at",
		Cutoff: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	n, err := archiveDeleteBatch(ctx, conn, run, archiveDeleteSQL(run), archiveSuspendableGuards(guards))
	if err != nil {
		t.Fatalf("delete with the guard suspended: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d rows, want the 2 rows before the cutoff", n)
	}

	var left int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+hist).Scan(&left); err != nil {
		t.Fatalf("count what is left: %v", err)
	}
	if left != 1 {
		t.Fatalf("%d rows left, want only the one after the cutoff", left)
	}

	var enabled string
	if err := conn.QueryRow(ctx,
		`SELECT tgenabled::text FROM pg_trigger WHERE tgname = 'trg_history_append_only'`).Scan(&enabled); err != nil {
		t.Fatalf("read the trigger back: %v", err)
	}
	if enabled != "O" {
		t.Fatalf("tgenabled = %q after the batch, want the trigger restored to O", enabled)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM `+hist+` WHERE id = 3`); err == nil {
		t.Fatal("the trigger did not come back: a plain delete still succeeds")
	}
}

// TestSuspendedGuardKeepsEnableAlways pins the detail a naive restore loses:
// ENABLE TRIGGER means tgenabled = 'O', so an ALWAYS trigger put back that way
// would silently stop firing under logical replication.
func TestSuspendedGuardKeepsEnableAlways(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedGuardedTables(t, conn)
	hist := pgx.Identifier{schema, "history"}.Sanitize()

	if _, err := conn.Exec(ctx, `ALTER TABLE `+hist+` ENABLE ALWAYS TRIGGER trg_history_append_only`); err != nil {
		t.Fatalf("declare the trigger ALWAYS: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO `+hist+` (id, observed_at) VALUES (1, '2026-01-01')`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	guards, err := archiveDeleteGuards(ctx, conn, schema, "history")
	if err != nil {
		t.Fatalf("read delete guards: %v", err)
	}
	run := archiveRun{
		Schema: schema, Table: "history", CutoffColumn: "observed_at",
		Cutoff: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := archiveDeleteBatch(ctx, conn, run, archiveDeleteSQL(run), archiveSuspendableGuards(guards)); err != nil {
		t.Fatalf("delete with the guard suspended: %v", err)
	}

	var enabled string
	if err := conn.QueryRow(ctx,
		`SELECT tgenabled::text FROM pg_trigger WHERE tgname = 'trg_history_append_only'`).Scan(&enabled); err != nil {
		t.Fatalf("read the trigger back: %v", err)
	}
	if enabled != "A" {
		t.Fatalf("tgenabled = %q, want A: the ALWAYS declaration must survive the batch", enabled)
	}
}

// TestDoInsteadRuleStillRefuses keeps the half of the guard that has no
// suspension: a rewrite rule cannot be taken out of the way for one statement.
func TestDoInsteadRuleStillRefuses(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedGuardedTables(t, conn)

	if _, err := conn.Exec(ctx,
		`CREATE RULE keep_everything AS ON DELETE TO `+pgx.Identifier{schema, "plain"}.Sanitize()+` DO INSTEAD NOTHING`); err != nil {
		t.Fatalf("create the rule: %v", err)
	}
	guards, err := archiveDeleteGuards(ctx, conn, schema, "plain")
	if err != nil {
		t.Fatalf("read delete guards: %v", err)
	}
	if len(archiveBlockingGuards(guards)) != 1 {
		t.Fatalf("a do-instead rule must still refuse the run, got %+v", guards)
	}
	if len(archiveSuspendableGuards(guards)) != 0 {
		t.Fatalf("a rule must never be reported as suspendable, got %+v", guards)
	}
}
