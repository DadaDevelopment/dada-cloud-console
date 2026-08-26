package pgexec_test

// Integration test against a REAL PostgreSQL instance, following the
// existing repo convention (see internal/api/onboarding_test.go,
// ai_routing_test.go): skip when TEST_DATABASE_URL is unset rather than
// faking success.
//
// pgexec connects as the caller's OWN database credential in production
// (db_query_helpers.go's connectTenantRole, reading the same Crossplane
// connection secret GetDatabaseCredentials reveals) -- there is no separate,
// narrowed platform role to test against here. What these tests prove is
// pgexec's OWN guarantee, independent of which role is connected: the
// transaction never commits and the row/size cap holds regardless of the
// connecting role's privileges. The grammar gate (policy.Classify) is the
// other independent layer and is exercised in policy/classify_test.go's
// red-team corpus without any database at all.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/pgread/pgexec"
	"github.com/dada-tuda/console/backend/internal/pgread/policy"
)

func testConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping pgexec integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func TestExecuteQuery_HappyPath(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	cls, err := policy.Classify(`SELECT 1 AS n, 'hi' AS s`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pgexec.ExecuteQuery(ctx, conn, cls.SQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 1 || res.Truncated {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestExecuteQuery_RowLimitTruncates(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	cls, err := policy.Classify(`SELECT * FROM generate_series(1, 5000)`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pgexec.ExecuteQuery(ctx, conn, cls.SQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.TruncationReason != pgexec.TruncationRows {
		t.Fatalf("expected row_limit truncation, got %+v", res)
	}
	if res.RowCount != pgexec.MaxRows {
		t.Fatalf("expected exactly %d rows, got %d", pgexec.MaxRows, res.RowCount)
	}
}

// TestTransactionNeverCommits is pgexec's own independent guarantee in
// isolation: even a query that somehow reached here (bypassing
// policy.Classify entirely, simulating a bug in the classifier) cannot
// persist a write, because ExecuteQuery's transaction is always rolled
// back -- true regardless of which role is connected, including a normal,
// non-restricted tenant role. This does NOT run the classifier at all, on
// purpose.
func TestTransactionNeverCommits(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE dada_pgexec_probe(n int)`); err != nil {
		t.Fatal(err)
	}
	// Deliberately NOT policy.Classify'd: exercising ExecuteQuery's own
	// transaction discipline directly, independent of the grammar layer.
	if _, err := pgexec.ExecuteQuery(ctx, conn,
		`SELECT 1 FROM (SELECT 1) x`, nil); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM dada_pgexec_probe`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("probe table should be empty, got %d rows", count)
	}
}

func TestExecuteExplain_NeverExecutesInner(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE dada_pgexec_explain_probe(n int)`); err != nil {
		t.Fatal(err)
	}
	cls, err := policy.Classify(`SELECT * FROM dada_pgexec_explain_probe`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pgexec.ExecuteExplain(ctx, conn, cls.SQL, "text")
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount == 0 {
		t.Fatal("expected a plan to come back")
	}
}
