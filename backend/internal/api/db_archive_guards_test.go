package api

import (
	"context"
	"strings"
	"testing"

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
