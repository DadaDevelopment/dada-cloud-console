package api

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedParentChild builds a parent with two referencing tables: one that holds
// rows and one that is empty, plus an unrelated table nobody points at.
func seedParentChild(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	ctx := context.Background()
	schema := "archive_fk_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	q := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+q); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA `+q+` CASCADE`)
	})
	stmts := []string{
		`CREATE TABLE ` + q + `.observations (id BIGINT PRIMARY KEY, observed_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE ` + q + `.assessments (id BIGINT PRIMARY KEY, observation_id BIGINT REFERENCES ` + q + `.observations(id))`,
		`CREATE TABLE ` + q + `.audit (id BIGINT PRIMARY KEY, observation_id BIGINT REFERENCES ` + q + `.observations(id) ON DELETE CASCADE)`,
		`CREATE TABLE ` + q + `.unrelated (id BIGINT PRIMARY KEY, observed_at TIMESTAMPTZ NOT NULL)`,
		`INSERT INTO ` + q + `.observations SELECT i, TIMESTAMPTZ '2026-07-01 00:00:00+00' FROM generate_series(1, 10) i`,
		`INSERT INTO ` + q + `.assessments SELECT i, i FROM generate_series(1, 10) i`,
		`ANALYZE ` + q + `.assessments`,
		`ANALYZE ` + q + `.audit`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return schema
}

// TestChildrenBlockTheRunBeforeAnythingIsExported pins the ordering the first
// real archive on a customer database got wrong: a parent whose children still
// hold rows must be refused up front, not after an export the delete phase can
// never use.
func TestChildrenBlockTheRunBeforeAnythingIsExported(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)

	refs, err := archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read blocking children: %v", err)
	}
	if len(refs) != 1 || refs[0].Table != "assessments" || refs[0].Column != "observation_id" {
		t.Fatalf("blocking children = %+v, want only the child that holds rows", refs)
	}
	if refs[0].Rows <= 0 {
		t.Fatalf("the blocking child was reported with %d rows", refs[0].Rows)
	}

	msg := archiveChildrenReason(schema, "observations", refs)
	if !strings.Contains(msg, "assessments") || !strings.Contains(msg, "observation_id") {
		t.Fatalf("the refusal does not name the table to archive first: %s", msg)
	}

	if _, err := conn.Exec(ctx,
		`INSERT INTO `+pgx.Identifier{schema, "audit"}.Sanitize()+` SELECT i, i FROM generate_series(1, 3) i`); err != nil {
		t.Fatalf("fill the cascade child: %v", err)
	}
	if _, err := conn.Exec(ctx, `ANALYZE `+pgx.Identifier{schema, "audit"}.Sanitize()); err != nil {
		t.Fatalf("analyze the cascade child: %v", err)
	}
	refs, err = archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read blocking children again: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("a cascade child holding rows must block too, got %+v", refs)
	}
	if msg := archiveChildrenReason(schema, "observations", refs); !strings.Contains(msg, "cascade") {
		t.Fatalf("the refusal does not warn that a cascade would delete unarchived rows: %s", msg)
	}

	free, err := archiveBlockingChildren(ctx, conn, schema, "unrelated")
	if err != nil {
		t.Fatalf("read children of the unreferenced table: %v", err)
	}
	if len(free) != 0 {
		t.Fatalf("a table nobody references must not be blocked, got %+v", free)
	}
}
