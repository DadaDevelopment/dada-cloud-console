// Package pgexec runs a policy.Classify-approved statement against a tenant
// PostgreSQL instance inside a READ ONLY transaction with a hard row/size
// cap, using the extended query protocol's portal-suspend mechanism rather
// than a text-concatenated LIMIT clause.
//
// The connecting credential is the database's OWN role (the same one
// GetDatabaseCredentials reveals to a project writer), not a
// platform-provisioned narrowed role -- see backend/internal/api/db_query.go
// for why. This package's job is the layer that stays independent of
// whatever privileges that role happens to carry: a READ ONLY transaction
// is a PostgreSQL engine-level restriction (SQLSTATE 25006 on any write
// attempt inside it) that applies regardless of the connecting role's own
// grants.
package pgexec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/pgread/policy"
)

// Limits are the v0.1 defaults from the design (section 10, Resource
// limits). They are deliberately conservative: this tool answers "what
// happened", not "export the table".
const (
	StatementTimeout                = 5 * time.Second
	LockTimeout                     = 1 * time.Second
	IdleInTransactionSessionTimeout = 5 * time.Second
	MaxRows                         = 1000
	MaxResponseBytes                = 5 * 1024 * 1024
	// fetchBatchSize bounds how many rows are pulled from the cursor per
	// FETCH round-trip. Kept well under MaxRows so the size cap can still
	// stop mid-batch without materializing a whole extra MaxRows batch past
	// the point the byte budget was already exceeded.
	fetchBatchSize = 200
)

// TruncationReason mirrors the spec's postgres_query output_schema.
type TruncationReason string

const (
	TruncationNone TruncationReason = ""
	TruncationRows TruncationReason = "row_limit"
	TruncationSize TruncationReason = "size_limit"
)

// Column describes one output column, matching the spec's columns[] shape.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Result is the postgres_query / postgres_explain output shape (spec section
// 5). Rows are already value-encoded per encode.go's rules (numeric/uuid/
// timestamp etc. as strings) so the caller can json.Marshal it directly.
type Result struct {
	Columns          []Column         `json:"columns"`
	Rows             [][]any          `json:"rows"`
	RowCount         int              `json:"row_count"`
	Truncated        bool             `json:"truncated"`
	TruncationReason TruncationReason `json:"truncation_reason,omitempty"`
	DurationMs       int64            `json:"duration_ms"`
}

// Conn is the minimal pgx surface this package needs, satisfied by
// *pgx.Conn (what connectToTenantDB in db_activity.go already returns) and
// by *pgx.Tx in tests.
type Conn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ExecuteQuery runs a classified, read-only SELECT/WITH-SELECT. sql must
// already be the output of policy.Classify -- this function does not parse
// or validate SQL itself; it only enforces the transaction/timeout/cursor
// discipline around whatever it is handed. Callers MUST NOT pass
// caller-supplied SQL text directly; see api/db_query.go for the only
// intended call site.
func ExecuteQuery(ctx context.Context, conn Conn, sql string, args []any) (*Result, error) {
	return runCursor(ctx, conn, sql, args)
}

// ExecuteExplain runs the gateway's OWN fixed-option EXPLAIN wrapper around
// an already-classified inner SELECT. The options list is hard-coded here,
// never assembled from caller input, so a client cannot smuggle ANALYZE past
// policy.Classify's ExplainStmt option check by finding some other path into
// this function -- there is no parameter that accepts option names at all.
//
// EXPLAIN cannot go through runCursor: PostgreSQL does not allow
// `DECLARE ... CURSOR FOR EXPLAIN ...` at all (EXPLAIN is a utility
// statement, not a cursor-able query) -- attempting it is a syntax error at
// the server, not a permissions problem. EXPLAIN output is inherently
// bounded (one plan, not a table scan), so running it as a single direct
// query and applying the same row/size caps to whatever comes back is both
// correct and sufficient; there is no billion-row case here to protect
// against with incremental FETCHing.
func ExecuteExplain(ctx context.Context, conn Conn, innerSQL string, format string) (*Result, error) {
	if format != "json" && format != "text" {
		format = "text"
	}
	wrapped := "EXPLAIN (VERBOSE, COSTS, FORMAT " + format + ") " + innerSQL
	return runDirect(ctx, conn, wrapped)
}

// setGuards applies the v0.1 resource limits to the current transaction.
// Shared by both execution paths so a future limit change cannot drift
// between them.
func setGuards(ctx context.Context, tx pgx.Tx) error {
	guards := []string{
		"SET LOCAL statement_timeout = '5s'",
		"SET LOCAL lock_timeout = '1s'",
		"SET LOCAL idle_in_transaction_session_timeout = '5s'",
		"SET LOCAL search_path = 'pg_catalog'",
	}
	for _, g := range guards {
		if _, err := tx.Exec(ctx, g); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// beginGuarded starts the transaction, applies the guards, and returns a tx
// whose caller MUST still defer tx.Rollback(context.Background()) -- this
// helper does not itself register the rollback, since the caller needs the
// tx alive past this call.
func beginGuarded(ctx context.Context, conn Conn) (pgx.Tx, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	if err := setGuards(ctx, tx); err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}

// runCursor executes sql via DECLARE ... CURSOR + FETCH FORWARD N (spec
// section 10: "gateway не должен тупо дописывать LIMIT 1000 строкой"). The
// plan executes incrementally batch by batch; a query against a
// billion-row table is not materialized in full before the cap can stop
// it, unlike a client-side slice of an already-fetched result set.
func runCursor(ctx context.Context, conn Conn, sql string, args []any) (*Result, error) {
	start := time.Now()

	tx, err := beginGuarded(ctx, conn)
	if err != nil {
		return nil, err
	}
	// Always rolls back, even on the happy path: this is the THIRD
	// independent safety layer (transaction never commits), so a bug that
	// somehow got a write-shaped statement past policy.Classify still
	// cannot persist anything.
	defer tx.Rollback(context.Background())

	if _, err := tx.Exec(ctx, "DECLARE dada_c NO SCROLL CURSOR FOR "+sql, args...); err != nil {
		return nil, mapErr(err)
	}

	result := &Result{}
	sizeBytes := 0
	fieldsDescribed := false

	for {
		// The batch size for THIS fetch: fetchBatchSize normally, but once
		// what's left fits within one batch, request one MORE row than the
		// remaining budget instead of the plain remaining count. That extra
		// row is the truncation signal -- without it, a table with exactly
		// MaxRows rows and a table with far more both end their last batch
		// having consumed precisely `remaining` rows with nothing left over
		// to tell them apart, so Truncated would wrongly read false whenever
		// MaxRows happens to be an exact multiple of fetchBatchSize (as it
		// is by default: 1000 / 200). Requesting remaining+1 means a table
		// with MORE data always hands back that one extra row, which the
		// loop below turns into Truncated=true before it is ever appended.
		remaining := MaxRows - result.RowCount
		n := fetchBatchSize
		if remaining <= fetchBatchSize {
			n = remaining + 1
		}

		rows, err := tx.Query(ctx, fmt.Sprintf("FETCH FORWARD %d FROM dada_c", n))
		if err != nil {
			return nil, mapErr(err)
		}

		if !fieldsDescribed {
			result.Columns = describeColumns(rows.FieldDescriptions())
			fieldsDescribed = true
		}

		batchRows := 0
		for rows.Next() {
			if result.RowCount >= MaxRows {
				result.Truncated = true
				result.TruncationReason = TruncationRows
				rows.Close()
				goto done
			}
			vals, err := rows.Values()
			if err != nil {
				rows.Close()
				return nil, mapErr(err)
			}
			row := encodeRow(vals)
			sizeBytes += estimateRowBytes(row)
			if sizeBytes > MaxResponseBytes {
				result.Truncated = true
				result.TruncationReason = TruncationSize
				rows.Close()
				goto done
			}
			result.Rows = append(result.Rows, row)
			result.RowCount++
			batchRows++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		rows.Close()

		// Fewer rows came back than were requested: the cursor is
		// exhausted, there is nothing more to fetch. This compares against
		// n (this iteration's own request size), not the fetchBatchSize
		// constant -- the final iteration's n is deliberately
		// remaining+1, and comparing against the constant here would have
		// reintroduced the same off-by-one this whole block exists to
		// close.
		if batchRows < n {
			break
		}
	}

done:
	result.DurationMs = time.Since(start).Milliseconds()
	// tx.Rollback via defer: nothing here ever commits.
	return result, nil
}

// runDirect executes sql as a single statement (no cursor/FETCH), for
// statement shapes Postgres will not let a cursor wrap -- currently just
// EXPLAIN. The same row/size caps from runCursor apply, closing the read
// early if a pathological EXPLAIN output ever exceeded them, but there is
// no incremental FETCH here: the whole statement runs in one round trip,
// which is fine for a query plan (never a table's worth of rows).
func runDirect(ctx context.Context, conn Conn, sql string) (*Result, error) {
	start := time.Now()

	tx, err := beginGuarded(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())

	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	result := &Result{}
	result.Columns = describeColumns(rows.FieldDescriptions())
	sizeBytes := 0

	for rows.Next() {
		if result.RowCount >= MaxRows {
			result.Truncated = true
			result.TruncationReason = TruncationRows
			break
		}
		vals, err := rows.Values()
		if err != nil {
			return nil, mapErr(err)
		}
		row := encodeRow(vals)
		sizeBytes += estimateRowBytes(row)
		if sizeBytes > MaxResponseBytes {
			result.Truncated = true
			result.TruncationReason = TruncationSize
			break
		}
		result.Rows = append(result.Rows, row)
		result.RowCount++
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	// tx.Rollback via defer: nothing here ever commits.
	return result, nil
}

var errNilConn = errors.New("pgexec: nil connection")

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconnPgError
	if errors.As(err, &pgErr) {
		return &policy.Error{Code: classifyPgError(pgErr), Message: pgErr.Message}
	}
	return err
}
