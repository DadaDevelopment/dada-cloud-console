package api

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// testCutoffBudget is generous on the seeded tables, so a timeout here means
// the join is wrong rather than the machine slow.
const testCutoffBudget = 20 * time.Second

// seedDatedParents gives every parent row its own day and points the child at a
// suffix of them, which is the shape the customer table had: the child holds no
// rows at all before a certain date, and every run whose cutoff sits after that
// date is refused.
func seedDatedParents(t *testing.T, conn *pgx.Conn, schema string, firstReferenced int) {
	t.Helper()
	ctx := context.Background()
	obs := pgx.Identifier{schema, "observations"}.Sanitize()
	child := pgx.Identifier{schema, "assessments"}.Sanitize()
	stmts := []string{
		`DELETE FROM ` + child,
		`DELETE FROM ` + obs,
		`INSERT INTO ` + obs + ` SELECT i, TIMESTAMPTZ '2026-07-01 00:00:00+00' + make_interval(days => i) FROM generate_series(1, 20) i`,
		`INSERT INTO ` + child + ` SELECT i, i FROM generate_series(` + strconv.Itoa(firstReferenced) + `, 20) i`,
		`ANALYZE ` + child,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
}

// TestBlockedRunIsToldACutoffThatWouldWork is the gap the live case exposed.
// The gate correctly refused an archive of a 5 GB table and named the child
// table and the number of rows still pointing into the window - and stopped
// there. Finding the date on which the refusal lifts meant writing the join
// against the customer's own database by hand, which is not something the owner
// of the database can be asked to do.
//
// The answer is the oldest cutoff-column value among the parent rows the
// children still reference: a delete removes rows strictly older than the
// cutoff, so a run stopped at that value leaves every referenced parent alive.
func TestBlockedRunIsToldACutoffThatWouldWork(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)
	seedDatedParents(t, conn, schema, 12)

	run := archiveRun{
		Schema:       schema,
		Table:        "observations",
		CutoffColumn: "observed_at",
		Cutoff:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
	candidates, err := archiveBlockingChildren(ctx, conn, schema, "observations")
	if err != nil {
		t.Fatalf("read candidate children: %v", err)
	}
	blocking := archiveChildrenInWindow(ctx, conn, run, candidates, testProbeBudget)
	if len(blocking) == 0 {
		t.Fatalf("the seeded child points into the window and must block")
	}

	got := archiveWorkingCutoffText(ctx, conn, run, blocking, testCutoffBudget)
	if got != "2026-07-13" {
		t.Fatalf("working cutoff = %q, want 2026-07-13: the oldest referenced parent is day 12, and a run stopped there deletes nothing the keys hold", got)
	}

	msg := archiveChildrenReason(schema, "observations", blocking, got)
	if !strings.Contains(msg, "2026-07-13") {
		t.Fatalf("the refusal names the blocking table but not the date that lifts it: %s", msg)
	}
}

// TestWorkingCutoffStaysQuietWhenNothingIsReferenced pins the other half: with
// no child rows left there is no date to suggest, and the refusal must not grow
// a clause built on an empty answer.
func TestWorkingCutoffStaysQuietWhenNothingIsReferenced(t *testing.T) {
	conn := testTenantConn(t)
	ctx := context.Background()
	schema := seedParentChild(t, conn)
	seedDatedParents(t, conn, schema, 12)

	if _, err := conn.Exec(ctx, `DELETE FROM `+pgx.Identifier{schema, "assessments"}.Sanitize()); err != nil {
		t.Fatalf("clear the child: %v", err)
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
	if got := archiveWorkingCutoffText(ctx, conn, run, candidates, testCutoffBudget); got != "" {
		t.Fatalf("working cutoff = %q with no referencing rows, want no suggestion at all", got)
	}
	if msg := archiveChildrenReason(schema, "observations", candidates, ""); strings.Contains(msg, "newest one") {
		t.Fatalf("the refusal invented a suggestion it does not have: %s", msg)
	}
}
