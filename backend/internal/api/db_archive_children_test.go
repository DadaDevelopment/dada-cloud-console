package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// testProbeBudget is the ordinary budget for the seeded ten-row tables, large
// enough that a timeout in a test is a real failure and not a slow machine.
var testProbeBudget = archiveProbeBudget{Probe: 10 * time.Second, Count: 10 * time.Second}

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

// TestChildrenOnlyBlockTheRowsTheCutoffWouldDelete pins the second half of the
// gate, found on the same customer database after the first archives landed: a
// child keeps pointing at the parent's recent rows forever, so counting the
// child's whole population refuses runs over rows they would never touch. Two
// of the three keys blocking a 5 GB table held zero rows in the window.
func TestChildrenOnlyBlockTheRowsTheCutoffWouldDelete(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)

	if _, err := conn.Exec(ctx,
		`INSERT INTO `+pgx.Identifier{schema, "observations"}.Sanitize()+
			` SELECT i, TIMESTAMPTZ '2026-09-01 00:00:00+00' FROM generate_series(11, 20) i`); err != nil {
		t.Fatalf("seed the recent parent rows: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`DELETE FROM `+pgx.Identifier{schema, "assessments"}.Sanitize()); err != nil {
		t.Fatalf("clear the child: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO `+pgx.Identifier{schema, "assessments"}.Sanitize()+
			` SELECT i, i FROM generate_series(11, 20) i`); err != nil {
		t.Fatalf("point the child at the recent rows only: %v", err)
	}
	if _, err := conn.Exec(ctx, `ANALYZE `+pgx.Identifier{schema, "assessments"}.Sanitize()); err != nil {
		t.Fatalf("analyze the child: %v", err)
	}

	candidates, err := archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ParentColumn != "id" {
		t.Fatalf("candidates = %+v, want the assessments key with its referenced column", candidates)
	}

	run := archiveRun{
		Schema:       schema,
		Table:        "observations",
		CutoffColumn: "observed_at",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	if blocking := archiveChildrenInWindow(ctx, conn, run, candidates, testProbeBudget); len(blocking) != 0 {
		t.Fatalf("a child that points only at rows the cutoff keeps must not block, got %+v", blocking)
	}

	if _, err := conn.Exec(ctx,
		`INSERT INTO `+pgx.Identifier{schema, "assessments"}.Sanitize()+` VALUES (1, 1)`); err != nil {
		t.Fatalf("point one child row at an old parent: %v", err)
	}
	blocking := archiveChildrenInWindow(ctx, conn, run, candidates, testProbeBudget)
	if len(blocking) != 1 || blocking[0].Rows != 1 {
		t.Fatalf("one child row inside the window must block and be counted exactly, got %+v", blocking)
	}
	if msg := archiveChildrenReason(schema, "observations", blocking, ""); !strings.Contains(msg, "1 rows") {
		t.Fatalf("the refusal does not carry the measured overlap: %s", msg)
	}
}

// TestTheVerdictSurvivesACountThatCannotFinish pins what the live 5 GB table
// exposed the first time the narrowed gate ran in production: every key came
// back marked estimated, because the exact count did not fit its deadline and
// the verdict was riding on that count. On that pair EXISTS answers in 130 ms
// what the count needs 2.5 s for, so the decision must ride the probe and only
// the number may go missing.
func TestTheVerdictSurvivesACountThatCannotFinish(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)

	if _, err := conn.Exec(ctx,
		`UPDATE `+pgx.Identifier{schema, "observations"}.Sanitize()+
			` SET observed_at = TIMESTAMPTZ '2026-09-01 00:00:00+00'`); err != nil {
		t.Fatalf("move every parent row past the cutoff: %v", err)
	}

	candidates, err := archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}

	deadCount := archiveProbeBudget{Probe: testProbeBudget.Probe, Count: time.Nanosecond}

	run := archiveRun{
		Schema:       schema,
		Table:        "observations",
		CutoffColumn: "observed_at",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	if blocking := archiveChildrenInWindow(ctx, conn, run, candidates, deadCount); len(blocking) != 0 {
		t.Fatalf("a count that cannot finish must not turn a non-blocking key into a refusal, got %+v", blocking)
	}

	if _, err := conn.Exec(ctx,
		`UPDATE `+pgx.Identifier{schema, "observations"}.Sanitize()+
			` SET observed_at = TIMESTAMPTZ '2026-07-01 00:00:00+00' WHERE id = 1`); err != nil {
		t.Fatalf("pull one parent row back inside the window: %v", err)
	}
	blocking := archiveChildrenInWindow(ctx, conn, run, candidates, deadCount)
	if len(blocking) != 1 || !blocking[0].Estimated || blocking[0].Unknown {
		t.Fatalf("a real blocker whose count timed out must still block as a decided key, marked estimated, got %+v", blocking)
	}
}

// TestAProbeThatCannotAnswerDoesNotRefuseTheRequest pins the third and last way
// this gate refused everything in production. Proving that a key holds nothing
// in the window is a full scan -- EXISTS is only fast when it finds a row -- and
// on the customer database two keys need 11 s and 41 s to come back false
// against a 4 s request budget. Silence at that budget must leave the request
// undecided rather than refuse it, and the same silence must still stop the run
// in the worker, which asks with minutes to spare and before anything is
// exported.
func TestAProbeThatCannotAnswerDoesNotRefuseTheRequest(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)

	if _, err := conn.Exec(ctx,
		`UPDATE `+pgx.Identifier{schema, "observations"}.Sanitize()+
			` SET observed_at = TIMESTAMPTZ '2026-09-01 00:00:00+00'`); err != nil {
		t.Fatalf("move every parent row past the cutoff: %v", err)
	}

	candidates, err := archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}

	run := archiveRun{
		Schema:       schema,
		Table:        "observations",
		CutoffColumn: "observed_at",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	deadProbe := archiveProbeBudget{Probe: time.Nanosecond, Count: time.Nanosecond}

	refs := archiveChildrenInWindow(ctx, conn, run, candidates, deadProbe)
	if len(refs) != 1 || !refs[0].Unknown {
		t.Fatalf("a probe that cannot finish must return the key undecided, got %+v", refs)
	}
	if decided := archiveChildrenDecided(refs); len(decided) != 0 {
		t.Fatalf("an undecided key must not refuse a request, got %+v", decided)
	}
	if unknown := archiveChildrenUnknown(refs); len(unknown) != 1 {
		t.Fatalf("an undecided key must be reported as unchecked, got %+v", unknown)
	}

	if err := archiveChildrenVerdict(ctx, conn, run); err != nil {
		t.Fatalf("with its own budget the worker can prove this key clean, got refusal: %v", err)
	}

	if _, err := conn.Exec(ctx,
		`UPDATE `+pgx.Identifier{schema, "observations"}.Sanitize()+
			` SET observed_at = TIMESTAMPTZ '2026-07-01 00:00:00+00' WHERE id = 1`); err != nil {
		t.Fatalf("pull one parent row back inside the window: %v", err)
	}
	err = archiveChildrenVerdict(ctx, conn, run)
	if err == nil {
		t.Fatal("a real blocker must stop the run in the worker, before anything is exported")
	}
	if !strings.Contains(err.Error(), "assessments") {
		t.Fatalf("the refusal must name the blocking table, got %v", err)
	}
}

// TestACancelledProbeCostsItsOwnConnectionAndNothingElse pins why the probes
// need a connection nobody else will reuse. pgx answers a query cancelled by a
// deadline by closing the connection, so a probe that runs out of budget took
// the whole request down with it: in production the plan came back 503 "cannot
// read the table's delete rules right now" for the two tables whose foreign
// keys are slowest to prove clean.
func TestACancelledProbeCostsItsOwnConnectionAndNothingElse(t *testing.T) {
	conn := testTenantConn(t)

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, err := conn.Exec(ctx, `SELECT pg_sleep(5)`); err == nil {
		t.Fatal("expected the sleep to be cancelled by its deadline")
	}
	var one int
	if err := conn.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err == nil {
		t.Skip("this driver now survives a cancelled query; the separate connection is no longer load-bearing")
	}

	fresh := testTenantConn(t)
	schema := seedParentChild(t, fresh)
	run := archiveRun{
		Schema:       schema,
		Table:        "observations",
		CutoffColumn: "observed_at",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	candidates, err := archiveBlockingChildren(context.Background(), fresh, schema, "observations")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}

	probeConn := testTenantConn(t)
	archiveChildrenInWindow(context.Background(), probeConn, run, candidates,
		archiveProbeBudget{Probe: time.Nanosecond, Count: time.Nanosecond})

	if err := fresh.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("the caller's connection must survive a probe run on its own connection: %v", err)
	}
}

// TestDerivedChildCountUsesTheRunsOwnPredicate covers the shape the 10 GB table
// needed: the archived table has no date of its own, so the window is a join to
// a dated parent, and the child count has to cut on the same join the delete
// will use.
func TestDerivedChildCountUsesTheRunsOwnPredicate(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)
	q := pgx.Identifier{schema}.Sanitize()

	stmts := []string{
		`ALTER TABLE ` + q + `.assessments ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT TIMESTAMPTZ '2026-07-01 00:00:00+00'`,
		`UPDATE ` + q + `.assessments SET created_at = TIMESTAMPTZ '2026-09-01 00:00:00+00' WHERE id > 5`,
		`CREATE TABLE ` + q + `.candidates (id BIGINT PRIMARY KEY, assessment_id BIGINT NOT NULL REFERENCES ` + q + `.assessments(id))`,
		`CREATE TABLE ` + q + `.decisions (id BIGINT PRIMARY KEY, candidate_id BIGINT REFERENCES ` + q + `.candidates(id))`,
		`INSERT INTO ` + q + `.candidates SELECT i, i FROM generate_series(1, 10) i`,
		`INSERT INTO ` + q + `.decisions SELECT i, i FROM generate_series(6, 10) i`,
		`ANALYZE ` + q + `.decisions`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}

	candidates, err := archiveBlockingChildren(ctx, conn, schema, "candidates")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Table != "decisions" {
		t.Fatalf("candidates = %+v, want the decisions key", candidates)
	}

	run := archiveRun{
		Schema:       schema,
		Table:        "candidates",
		CutoffColumn: "created_at",
		ViaTable:     "assessments",
		ViaFK:        "assessment_id",
		ViaPK:        "id",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	if blocking := archiveChildrenInWindow(ctx, conn, run, candidates, testProbeBudget); len(blocking) != 0 {
		t.Fatalf("the decisions all hang off parents dated after the cutoff, got %+v", blocking)
	}

	if _, err := conn.Exec(ctx, `INSERT INTO `+q+`.decisions VALUES (1, 3)`); err != nil {
		t.Fatalf("point a decision at an old candidate: %v", err)
	}
	blocking := archiveChildrenInWindow(ctx, conn, run, candidates, testProbeBudget)
	if len(blocking) != 1 || blocking[0].Rows != 1 {
		t.Fatalf("a decision inside the derived window must block, got %+v", blocking)
	}
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

	msg := archiveChildrenReason(schema, "observations", refs, "")
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
	if msg := archiveChildrenReason(schema, "observations", refs, ""); !strings.Contains(msg, "cascade") {
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
