package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func derivedRun(schema string) archiveRun {
	return archiveRun{
		Schema:       schema,
		Table:        "event_matching_candidates",
		CutoffColumn: "as_of",
		ViaTable:     "event_matching_assessments",
		ViaFK:        "assessment_id",
		ViaPK:        "id",
		Cutoff:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestDerivedRunCarriesTheCutoffIntoTheArchive pins the one property that lets
// a derived run be verified at all: the exported Parquet has to hold the
// parent's timestamp, and verify has to read that alias rather than a column
// the child never had.
func TestDerivedRunCarriesTheCutoffIntoTheArchive(t *testing.T) {
	r := derivedRun("public")
	sql := archiveExportSelectSQL(r)
	if !strings.Contains(sql, `p."as_of" AS "_archive_cutoff_at"`) {
		t.Fatalf("export does not carry the parent timestamp into the archive: %s", sql)
	}
	if !strings.Contains(sql, `JOIN src."public"."event_matching_assessments" p ON p."id" = c."assessment_id"`) {
		t.Fatalf("export does not join the parent on the declared key: %s", sql)
	}
	if got := archiveCutoffColumnInArchive(r); got != archiveViaCutoffAlias {
		t.Fatalf("verify would read %q, want %q", got, archiveViaCutoffAlias)
	}

	plain := archiveRun{Schema: "public", Table: "events", CutoffColumn: "created_at", Cutoff: r.Cutoff}
	if got := archiveCutoffColumnInArchive(plain); got != "created_at" {
		t.Fatalf("a plain run must verify on its own column, got %q", got)
	}
	if strings.Contains(archiveExportSelectSQL(plain), "JOIN") {
		t.Fatalf("a plain run must not join anything: %s", archiveExportSelectSQL(plain))
	}
}

func testTenantConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping derived-cutoff DB integration test")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// seedDerivedTables builds the shape the feature was built for: a child table
// with no time column of its own, one dated parent, one undated parent, and a
// row whose foreign key points at nothing.
func seedDerivedTables(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	ctx := context.Background()
	schema := "archive_via_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	q := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+q); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA `+q+` CASCADE`)
	})
	stmts := []string{
		`CREATE TABLE ` + q + `.event_matching_assessments (id BIGINT PRIMARY KEY, as_of TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE ` + q + `.events (id BIGINT PRIMARY KEY, label TEXT)`,
		`CREATE TABLE ` + q + `.event_matching_candidates (
		     id BIGINT PRIMARY KEY,
		     assessment_id BIGINT REFERENCES ` + q + `.event_matching_assessments(id),
		     event_id BIGINT REFERENCES ` + q + `.events(id),
		     score DOUBLE PRECISION)`,
		`INSERT INTO ` + q + `.event_matching_assessments
		 SELECT i, TIMESTAMPTZ '2026-07-01 00:00:00+00' + (i || ' days')::interval FROM generate_series(1, 60) i`,
		`INSERT INTO ` + q + `.events SELECT i, 'e' || i FROM generate_series(1, 60) i`,
		`INSERT INTO ` + q + `.event_matching_candidates
		 SELECT i, i, i, 0.5 FROM generate_series(1, 60) i`,
		`INSERT INTO ` + q + `.event_matching_candidates VALUES (999, NULL, NULL, 0.5)`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return schema
}

// TestDerivedCutoffSelectsAndDeletesOnlyThePredicate is the whole derived path
// against a real Postgres: which parents can date the rows, that a join nobody
// declared is refused, and that the predicate the delete runs removes exactly
// the rows the plan counted - a row with no parent among them.
func TestDerivedCutoffSelectsAndDeletesOnlyThePredicate(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedDerivedTables(t, conn)

	cols, err := archiveColumnsOf(ctx, conn, schema, "event_matching_candidates")
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if _, reason := pickCutoffColumn(cols); reason == "" {
		t.Fatalf("the child table is supposed to have no cutoff column of its own")
	}

	vias, err := archiveViaCandidates(ctx, conn, schema, "event_matching_candidates")
	if err != nil {
		t.Fatalf("read via candidates: %v", err)
	}
	if len(vias) != 1 || vias[0].Table != "event_matching_assessments" ||
		vias[0].FK != "assessment_id" || vias[0].PK != "id" || vias[0].Column != "as_of" {
		t.Fatalf("via candidates = %+v, want only the dated parent", vias)
	}

	if err := validateArchiveVia(ctx, conn, schema, "event_matching_candidates", vias[0]); err != nil {
		t.Fatalf("the declared foreign key was refused: %v", err)
	}
	bogus := archiveVia{Table: "events", FK: "assessment_id", PK: "id", Column: "as_of"}
	if err := validateArchiveVia(ctx, conn, schema, "event_matching_candidates", bogus); err == nil {
		t.Fatalf("an undeclared join was accepted; it would delete rows on a relationship nobody declared")
	}

	r := derivedRun(schema)
	rows, err := archiveRowsMatching(ctx, conn, r)
	if err != nil {
		t.Fatalf("count rows before the cutoff: %v", err)
	}
	if rows != 30 {
		t.Fatalf("rows before 2026-08-01 = %d, want 30", rows)
	}

	tag, err := conn.Exec(ctx, archiveDeleteSQL(r), r.Cutoff)
	if err != nil {
		t.Fatalf("delete the archived rows: %v", err)
	}
	if tag.RowsAffected() != 30 {
		t.Fatalf("deleted %d rows, want 30", tag.RowsAffected())
	}
	var left, orphan int64
	if err := conn.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE assessment_id IS NULL)
		   FROM `+pgx.Identifier{schema, "event_matching_candidates"}.Sanitize()).Scan(&left, &orphan); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if left != 31 || orphan != 1 {
		t.Fatalf("after the delete: %d rows left with %d parentless, want 31 and 1", left, orphan)
	}
	if again, err := archiveRowsMatching(ctx, conn, r); err != nil || again != 0 {
		t.Fatalf("rows still matching the predicate = %d (err %v), want 0", again, err)
	}
}
