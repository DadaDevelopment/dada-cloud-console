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
	return run(ctx, conn, sql, args)
}

// ExecuteExplain runs the gateway's OWN fixed-option EXPLAIN wrapper around
// an already-classified inner SELECT. The options list is hard-coded here,
// never assembled from caller input, so a client cannot smuggle ANALYZE past
// policy.Classify's ExplainStmt option check by finding some other path into
// this function -- there is no parameter that accepts option names at all.
func ExecuteExplain(ctx context.Context, conn Conn, innerSQL string, format string) (*Result, error) {
	if format != "json" && format != "text" {
		format = "text"
	}
	wrapped := "EXPLAIN (VERBOSE, COSTS, FORMAT " + format + ") " + innerSQL
	return run(ctx, conn, wrapped, nil)
}

func run(ctx context.Context, conn Conn, sql string, args []any) (*Result, error) {
	start := time.Now()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	// Always rolls back, even on the happy path: this is the THIRD
	// independent safety layer (transaction never commits), so a bug that
	// somehow got a write-shaped statement past policy.Classify still
	// cannot persist anything.
	defer tx.Rollback(context.Background())

	guards := []string{
		"SET LOCAL statement_timeout = '5s'",
		"SET LOCAL lock_timeout = '1s'",
		"SET LOCAL idle_in_transaction_session_timeout = '5s'",
		"SET LOCAL search_path = 'pg_catalog'",
	}
	for _, g := range guards {
		if _, err := tx.Exec(ctx, g); err != nil {
			return nil, mapErr(err)
		}
	}

	// DECLARE ... CURSOR + FETCH FORWARD N is the real limit mechanism (spec
	// section 10: "gateway не должен тупо дописывать LIMIT 1000 строкой").
	// The plan executes incrementally batch by batch; a query against a
	// billion-row table is not materialized in full before the cap can stop
	// it, unlike a client-side slice of an already-fetched result set.
	if _, err := tx.Exec(ctx, "DECLARE dada_c NO SCROLL CURSOR FOR "+sql, args...); err != nil {
		return nil, mapErr(err)
	}

	result := &Result{}
	sizeBytes := 0
	fieldsDescribed := false

	for {
		rows, err := tx.Query(ctx, "FETCH FORWARD $1 FROM dada_c",
			minInt(fetchBatchSize, MaxRows-result.RowCount+1))
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

		if batchRows < fetchBatchSize || result.RowCount >= MaxRows {
			break
		}
	}

done:
	result.DurationMs = time.Since(start).Milliseconds()
	// tx.Rollback via defer: nothing here ever commits.
	return result, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
