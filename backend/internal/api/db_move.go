package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Phases of a move. They are stored, not derived: the initial copy of a large
// database runs for hours, so a console that restarts mid-move has to know what
// already happened rather than start again.
const (
	dbMovePending   = "pending"
	dbMovePreparing = "preparing"
	dbMoveSchema    = "schema"
	dbMoveSyncing   = "syncing"
	dbMoveCutover   = "cutover"
	dbMoveDone      = "done"
	dbMoveFailed    = "failed"
)

// dbMoveCutoverLagBytes is how close the copy must be before the move is
// allowed to hold traffic. It is not zero on purpose: a busy database never
// reaches exactly zero while it keeps writing, and waiting for that would mean
// never cutting over. A megabyte of WAL replays in well under the second the
// router holds clients for.
const dbMoveCutoverLagBytes int64 = 1 << 20

// dbMove is one database's journey between shards.
type dbMove struct {
	ID          string
	Datname     string
	OwnerRole   string
	SourceShard string
	TargetShard string
	Phase       string
	LagBytes    int64
}

// shardExecutor is the slice of a pgx connection the move steps need. Narrow on
// purpose: every step is then testable against a fake, which is the only way to
// pin the exact SQL a move runs without a pair of live Postgres instances.
type shardExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// quoteSQLLiteral escapes a value for a statement that cannot take a parameter.
// CREATE ROLE ... PASSWORD, CREATE SUBSCRIPTION ... CONNECTION and friends are
// utility statements: PostgreSQL rejects bind parameters in them, so the value
// has to be inlined and therefore escaped here.
func quoteSQLLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// moveObjectName derives the publication and subscription name from the
// database. Both live in namespaces shared with whatever the tenant created, so
// the prefix keeps a move from colliding with a customer's own publication, and
// the sanitising keeps a database name that is legal in Postgres but not in an
// unquoted identifier from producing a broken statement.
func moveObjectName(datname string) string {
	var b strings.Builder
	b.WriteString("dada_move_")
	for _, r := range datname {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

// copyRole recreates the database's owner on the target shard.
//
// Roles are cluster-wide, not database-local, so a database that arrives on a
// new instance arrives without its owner: the tenant's application would
// authenticate against a shard that has never heard of its user. The stored
// SCRAM verifier is copied verbatim rather than a password being reset, because
// PostgreSQL stores a PASSWORD value that already looks like a verifier as-is.
// That keeps the tenant's existing password working and means the console never
// learns it.
func copyRole(ctx context.Context, src, dst shardExecutor, role string) error {
	if role == "" {
		return errors.New("db-move: empty owner role")
	}
	var verifier string
	err := src.QueryRow(ctx,
		`SELECT COALESCE(rolpassword, '') FROM pg_authid WHERE rolname = $1`, role).Scan(&verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("db-move: role %q does not exist on the source shard", role)
	}
	if err != nil {
		return fmt.Errorf("db-move: read role %q: %w", role, err)
	}

	var exists bool
	if err := dst.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return fmt.Errorf("db-move: probe role %q on the target: %w", role, err)
	}

	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	stmt := fmt.Sprintf("%s ROLE %s WITH LOGIN PASSWORD %s",
		verb, quoteRouterIdent(role), quoteSQLLiteral(verifier))
	if _, err := dst.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("db-move: %s role %q on the target: %w", strings.ToLower(verb), role, err)
	}
	return nil
}

// ensureTargetDatabase creates the empty database the copy lands in. Owned by
// the tenant's role from the first moment: a database created as the admin and
// handed over later leaves objects owned by the wrong role, and the tenant then
// cannot alter its own tables.
func ensureTargetDatabase(ctx context.Context, dst shardExecutor, datname, owner string) error {
	var exists bool
	if err := dst.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, datname).Scan(&exists); err != nil {
		return fmt.Errorf("db-move: probe database %q on the target: %w", datname, err)
	}
	if exists {
		return nil
	}
	stmt := fmt.Sprintf("CREATE DATABASE %s OWNER %s",
		quoteRouterIdent(datname), quoteRouterIdent(owner))
	if _, err := dst.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("db-move: create database %q on the target: %w", datname, err)
	}
	return nil
}

// startReplication opens the stream that keeps the copy current while the
// tenant keeps writing. This is what makes a move a flap instead of an outage:
// the data is already there when traffic switches.
//
// FOR ALL TABLES rather than a table list: a tenant can create a table while
// the move runs, and a list captured at the start would silently leave that
// table behind on the old shard.
func startReplication(ctx context.Context, srcDB, dstDB shardExecutor, datname, sourceConnInfo string) error {
	name := moveObjectName(datname)
	if _, err := srcDB.Exec(ctx,
		fmt.Sprintf("CREATE PUBLICATION %s FOR ALL TABLES", quoteRouterIdent(name))); err != nil {
		return fmt.Errorf("db-move: create publication: %w", err)
	}
	stmt := fmt.Sprintf("CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s",
		quoteRouterIdent(name), quoteSQLLiteral(sourceConnInfo), quoteRouterIdent(name))
	if _, err := dstDB.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("db-move: create subscription: %w", err)
	}
	return nil
}

// replicationLag reports how far the copy trails the original, in bytes of WAL.
//
// A missing walsender is an error rather than zero lag. Zero is what a caller
// waits for, so reporting it for "nobody is streaming" would cut over to a copy
// that stopped receiving changes at some unknown point.
func replicationLag(ctx context.Context, srcDB shardExecutor, datname string) (int64, error) {
	var senders int
	var lag int64
	err := srcDB.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(MAX(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)), 0)::bigint
		FROM pg_stat_replication
		WHERE application_name = $1
	`, moveObjectName(datname)).Scan(&senders, &lag)
	if err != nil {
		return 0, fmt.Errorf("db-move: read replication lag: %w", err)
	}
	if senders == 0 {
		return 0, errors.New("db-move: nothing is streaming to the target, the copy is not current")
	}
	if lag < 0 {
		lag = 0
	}
	return lag, nil
}

// moveSequence is one sequence's position, which logical replication does not
// carry.
type moveSequence struct {
	Schema string `json:"s"`
	Name   string `json:"n"`
	Value  int64  `json:"v"`
}

// copySequences replays sequence positions onto the copy.
//
// Logical replication replicates table rows and nothing else, so every sequence
// on the target sits at its starting value. Left alone, the first insert after
// the move would hand out a primary key the tenant already used, and the
// tenant's own unique constraint would start rejecting writes. Run inside the
// held window, after writes stop, so the copied position is the final one.
func copySequences(ctx context.Context, srcDB, dstDB shardExecutor) error {
	var payload string
	err := srcDB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		           's', schemaname, 'n', sequencename, 'v', last_value))::text, '[]')
		FROM pg_sequences
		WHERE last_value IS NOT NULL
	`).Scan(&payload)
	if err != nil {
		return fmt.Errorf("db-move: read sequences: %w", err)
	}
	var seqs []moveSequence
	if err := json.Unmarshal([]byte(payload), &seqs); err != nil {
		return fmt.Errorf("db-move: decode sequences: %w", err)
	}
	for _, s := range seqs {
		qualified := quoteRouterIdent(s.Schema) + "." + quoteRouterIdent(s.Name)
		if _, err := dstDB.Exec(ctx,
			`SELECT pg_catalog.setval($1::regclass, $2, true)`, qualified, s.Value); err != nil {
			return fmt.Errorf("db-move: set sequence %s: %w", qualified, err)
		}
	}
	return nil
}

// finishReplication takes the stream down once the move is over.
//
// The subscription goes first: dropping the publication while a subscriber is
// still attached leaves the target retrying a stream that no longer exists, and
// leaves a replication slot holding WAL on the source shard until someone
// notices the disk filling.
func finishReplication(ctx context.Context, srcDB, dstDB shardExecutor, datname string) error {
	name := quoteRouterIdent(moveObjectName(datname))
	if _, err := dstDB.Exec(ctx, fmt.Sprintf("DROP SUBSCRIPTION IF EXISTS %s", name)); err != nil {
		return fmt.Errorf("db-move: drop subscription: %w", err)
	}
	if _, err := srcDB.Exec(ctx, fmt.Sprintf("DROP PUBLICATION IF EXISTS %s", name)); err != nil {
		return fmt.Errorf("db-move: drop publication: %w", err)
	}
	return nil
}
