package pgexec

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dada-tuda/console/backend/internal/pgread/policy"
)

// classifyPgError maps a PostgreSQL error to the spec's error taxonomy
// (section 18) using SQLSTATE, not message text (message text is locale- and
// version-dependent; SQLSTATE is not). Every distinct sqlstate this policy
// cares about maps to a CALLER-FACING code that is one of the ones already
// defined in policy/errors.go, so the classifier layer and the database
// layer share one vocabulary end to end -- a caller cannot tell, from the
// error shape alone, whether the parser or Postgres itself rejected the
// query, which is exactly the point of having both layers agree.
func classifyPgError(pgErr *pgconn.PgError) policy.Code {
	switch pgErr.Code {
	case "25006": // read_only_sql_transaction
		return policy.CodeNotReadOnly
	case "42501": // insufficient_privilege
		return policy.CodeNotReadOnly
	case "57014": // query_canceled (statement_timeout)
		return "QUERY_TIMEOUT"
	case "55P03": // lock_not_available (lock_timeout)
		return "LOCK_TIMEOUT"
	case "42601", "42883", "42P01", "42703": // syntax_error, undefined_function,
		// undefined_table, undefined_column -- these slipped past
		// policy.Classify only because they are semantically invalid, not
		// structurally forbidden (e.g. a typo'd table/column name); Postgres
		// itself is the right place to catch that class of error, and the
		// spec calls for surfacing sqlstate/message/position back to the
		// agent so it can self-correct.
		return "DATABASE_ERROR"
	default:
		return "DATABASE_ERROR"
	}
}

// pgconnPgError aliases the real pgconn error type so exec.go's mapErr does
// not need to import pgconn directly. Kept as a type alias (not a
// redefinition) so errors.As still matches the concrete type pgx returns.
type pgconnPgError = pgconn.PgError

var errNotPgError = errors.New("not a *pgconn.PgError")
