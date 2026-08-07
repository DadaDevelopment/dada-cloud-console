package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.vals) {
		return errors.New("fake row: wrong number of destinations")
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = r.vals[i].(string)
		case *bool:
			*p = r.vals[i].(bool)
		case *int:
			*p = r.vals[i].(int)
		case *int64:
			*p = r.vals[i].(int64)
		default:
			return errors.New("fake row: unsupported destination")
		}
	}
	return nil
}

type fakeShard struct {
	answers []fakeRow
	queries []string
	cmds    []string
	args    [][]any
	execErr error
}

func (f *fakeShard) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.queries = append(f.queries, sql)
	if len(f.answers) == 0 {
		return fakeRow{err: pgx.ErrNoRows}
	}
	r := f.answers[0]
	f.answers = f.answers[1:]
	return r
}

func (f *fakeShard) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.cmds = append(f.cmds, sql)
	f.args = append(f.args, args)
	return pgconn.CommandTag{}, f.execErr
}

const testVerifier = "SCRAM-SHA-256$4096:abc$def:ghi"

func TestCopyRoleCopiesVerifierVerbatim(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{testVerifier}}}}
	dst := &fakeShard{answers: []fakeRow{{vals: []any{false}}}}

	if err := copyRole(context.Background(), src, dst, "tenant_user"); err != nil {
		t.Fatalf("copyRole: %v", err)
	}
	if len(dst.cmds) != 1 {
		t.Fatalf("want one statement on the target, got %v", dst.cmds)
	}
	stmt := dst.cmds[0]
	if !strings.HasPrefix(stmt, `CREATE ROLE "tenant_user" WITH LOGIN PASSWORD `) {
		t.Fatalf("unexpected statement: %s", stmt)
	}
	if !strings.Contains(stmt, testVerifier) {
		t.Fatalf("the stored verifier must be copied as-is, else the tenant's password stops working: %s", stmt)
	}
}

func TestCopyRoleAltersRoleThatAlreadyExists(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{testVerifier}}}}
	dst := &fakeShard{answers: []fakeRow{{vals: []any{true}}}}

	if err := copyRole(context.Background(), src, dst, "tenant_user"); err != nil {
		t.Fatalf("copyRole: %v", err)
	}
	if !strings.HasPrefix(dst.cmds[0], "ALTER ROLE ") {
		t.Fatalf("a role the shard already has must be altered, not created: %s", dst.cmds[0])
	}
}

func TestCopyRoleRefusesWhenSourceRoleMissing(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{err: pgx.ErrNoRows}}}
	dst := &fakeShard{}

	if err := copyRole(context.Background(), src, dst, "ghost"); err == nil {
		t.Fatal("a missing owner must fail the move, not produce a database nobody can log into")
	}
	if len(dst.cmds) != 0 {
		t.Fatalf("nothing may be written to the target: %v", dst.cmds)
	}
}

func TestEnsureTargetDatabaseSkipsExisting(t *testing.T) {
	dst := &fakeShard{answers: []fakeRow{{vals: []any{true}}}}
	if err := ensureTargetDatabase(context.Background(), dst, "odds-research", "tenant"); err != nil {
		t.Fatalf("ensureTargetDatabase: %v", err)
	}
	if len(dst.cmds) != 0 {
		t.Fatalf("a database that is already there must not be recreated: %v", dst.cmds)
	}
}

func TestEnsureTargetDatabaseCreatesOwnedByTenant(t *testing.T) {
	dst := &fakeShard{answers: []fakeRow{{vals: []any{false}}}}
	if err := ensureTargetDatabase(context.Background(), dst, "odds-research", "tenant"); err != nil {
		t.Fatalf("ensureTargetDatabase: %v", err)
	}
	want := `CREATE DATABASE "odds-research" OWNER "tenant"`
	if dst.cmds[0] != want {
		t.Fatalf("got %q, want %q", dst.cmds[0], want)
	}
}

func TestReplicationLagRefusesWithoutWalsender(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{0, int64(0)}}}}
	if _, err := replicationLag(context.Background(), src, "odds-research"); err == nil {
		t.Fatal("no walsender must not read as zero lag, that would cut over to a stale copy")
	}
}

func TestReplicationLagReportsBytes(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{1, int64(4096)}}}}
	lag, err := replicationLag(context.Background(), src, "odds-research")
	if err != nil {
		t.Fatalf("replicationLag: %v", err)
	}
	if lag != 4096 {
		t.Fatalf("lag = %d, want 4096", lag)
	}
}

func TestCopySequencesSetsEveryPosition(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{
		`[{"s":"public","n":"orders_id_seq","v":42},{"s":"app","n":"runs_id_seq","v":7}]`}}}}
	dst := &fakeShard{}

	if err := copySequences(context.Background(), src, dst); err != nil {
		t.Fatalf("copySequences: %v", err)
	}
	if len(dst.cmds) != 2 {
		t.Fatalf("every sequence must be moved, got %v", dst.cmds)
	}
	if dst.args[0][0] != `"public"."orders_id_seq"` || dst.args[0][1].(int64) != 42 {
		t.Fatalf("unexpected setval arguments: %v", dst.args[0])
	}
}

func TestCopySequencesHandlesNone(t *testing.T) {
	src := &fakeShard{answers: []fakeRow{{vals: []any{"[]"}}}}
	dst := &fakeShard{}
	if err := copySequences(context.Background(), src, dst); err != nil {
		t.Fatalf("copySequences: %v", err)
	}
	if len(dst.cmds) != 0 {
		t.Fatalf("no sequences, no statements: %v", dst.cmds)
	}
}

func TestFinishReplicationDropsSubscriptionFirst(t *testing.T) {
	src := &fakeShard{}
	dst := &fakeShard{}
	if err := finishReplication(context.Background(), src, dst, "odds-research"); err != nil {
		t.Fatalf("finishReplication: %v", err)
	}
	if len(dst.cmds) != 1 || !strings.HasPrefix(dst.cmds[0], "DROP SUBSCRIPTION IF EXISTS ") {
		t.Fatalf("target statements: %v", dst.cmds)
	}
	if len(src.cmds) != 1 || !strings.HasPrefix(src.cmds[0], "DROP PUBLICATION IF EXISTS ") {
		t.Fatalf("source statements: %v", src.cmds)
	}
}

func TestStartReplicationNamesBothSides(t *testing.T) {
	src := &fakeShard{}
	dst := &fakeShard{}
	if err := startReplication(context.Background(), src, dst, "odds-research",
		"host=old port=5432 dbname=odds-research user=postgres password=s3cret"); err != nil {
		t.Fatalf("startReplication: %v", err)
	}
	if src.cmds[0] != `CREATE PUBLICATION "dada_move_odds_research" FOR ALL TABLES` {
		t.Fatalf("publication: %s", src.cmds[0])
	}
	if !strings.Contains(dst.cmds[0], `CREATE SUBSCRIPTION "dada_move_odds_research" CONNECTION '`) {
		t.Fatalf("subscription: %s", dst.cmds[0])
	}
}

func TestMoveObjectNameIsSafeIdentifier(t *testing.T) {
	if got := moveObjectName("Odds-Research"); got != "dada_move_odds_research" {
		t.Fatalf("got %q", got)
	}
	if got := moveObjectName(strings.Repeat("x", 90)); len(got) != 63 {
		t.Fatalf("length = %d, want 63", len(got))
	}
}

func TestQuoteSQLLiteralEscapesQuotes(t *testing.T) {
	if got := quoteSQLLiteral("a'b"); got != "'a''b'" {
		t.Fatalf("got %s", got)
	}
}
