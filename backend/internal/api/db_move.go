package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// dbMoveStreamGrace is how long a move may see no walsender before it gives
// up. CREATE SUBSCRIPTION returns as soon as the slot exists on the source; the
// subscriber worker connects a moment later, and the first tick after the
// schema copy lands inside that gap often enough that two of the first six real
// moves died there - both were streaming healthily by the time the failure was
// read. Minutes rather than seconds because the alternative failure, a stream
// that really is dead, costs nothing extra to notice a little later: nothing
// cuts over while the lag is unknown.
const dbMoveStreamGrace = 5 * time.Minute

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
	UpdatedAt   time.Time
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

// resolveOwner reads who owns the database on the shard it is leaving.
//
// The owner is not something the person starting a move should have to know:
// it is a fact of the source shard, and a move enqueued without it used to die
// in prepare with "empty owner role" -- a message that names a column rather
// than the thing to look up. Roles are cluster-wide, so this is the only place
// the tenant's role can be learned before the database exists on the target.
func resolveOwner(ctx context.Context, src shardExecutor, datname string) (string, error) {
	var owner string
	err := src.QueryRow(ctx,
		`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, datname).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("db-move: database %q does not exist on the source shard", datname)
	}
	if err != nil {
		return "", fmt.Errorf("db-move: read owner of %q: %w", datname, err)
	}
	if owner == "" {
		return "", fmt.Errorf("db-move: database %q reports no owner", datname)
	}
	return owner, nil
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

// handOverObjects gives every copied object to the tenant's role.
//
// The schema copy runs as the shard admin, so the tables, sequences, views and
// functions it creates are owned by the admin no matter who owns the database.
// n8n found this the hard way: after its first real move every query answered
// "permission denied for schema n8n", because the schema on the new shard
// belonged to postgres. Ownership is not cosmetic - a tenant that cannot ALTER
// its own tables cannot run a migration, and the failure appears long after the
// move looked successful.
//
// A dollar-quoted DO block rather than a query and a loop in Go: the set of
// objects is only knowable on the target, and one statement means one round
// trip and no half-finished handover if the connection drops.
//
// Objects that belong to an extension are left alone (uuid-ossp installs
// functions owned by the admin on both shards, so touching them would make the
// copy differ from the original), and so are sequences owned by a column, which
// PostgreSQL refuses to reassign separately from their table.
//
// Schema public is left alone too when it belongs to pg_database_owner, which
// is how PostgreSQL 15 and later ship it: that role already resolves to
// whoever owns the database, so naming the tenant explicitly changes nothing a
// client can observe and makes the copy differ from the original for no
// reason. The first run of this handover did exactly that.
func handOverObjects(ctx context.Context, dstDB shardExecutor, owner string) error {
	stmt := `DO $handover$
DECLARE
	target text := ` + quoteSQLLiteral(owner) + `;
	r record;
BEGIN
	FOR r IN
		SELECT n.nspname
		FROM pg_namespace n
		WHERE n.nspname NOT LIKE 'pg\_%'
		  AND n.nspname <> 'information_schema'
		  AND pg_get_userbyid(n.nspowner) NOT IN (target, 'pg_database_owner')
		  AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.classid = 'pg_namespace'::regclass AND d.objid = n.oid AND d.deptype = 'e')
	LOOP
		EXECUTE format('ALTER SCHEMA %I OWNER TO %I', r.nspname, target);
	END LOOP;

	FOR r IN
		SELECT n.nspname, c.relname,
		       CASE c.relkind
		            WHEN 'S' THEN 'SEQUENCE'
		            WHEN 'v' THEN 'VIEW'
		            WHEN 'm' THEN 'MATERIALIZED VIEW'
		            WHEN 'f' THEN 'FOREIGN TABLE'
		            ELSE 'TABLE'
		       END AS kind
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT LIKE 'pg\_%'
		  AND n.nspname <> 'information_schema'
		  AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
		  AND pg_get_userbyid(c.relowner) <> target
		  AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
			  AND d.deptype IN ('a', 'i', 'e'))
	LOOP
		EXECUTE format('ALTER %s %I.%I OWNER TO %I', r.kind, r.nspname, r.relname, target);
	END LOOP;

	FOR r IN
		SELECT p.oid::regprocedure::text AS sig,
		       CASE p.prokind WHEN 'p' THEN 'PROCEDURE' ELSE 'FUNCTION' END AS kind
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname NOT LIKE 'pg\_%'
		  AND n.nspname <> 'information_schema'
		  AND p.prokind IN ('f', 'p')
		  AND pg_get_userbyid(p.proowner) <> target
		  AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
	LOOP
		EXECUTE format('ALTER %s %s OWNER TO %I', r.kind, r.sig, target);
	END LOOP;
END
$handover$`
	if _, err := dstDB.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("db-move: hand the copied objects to %q: %w", owner, err)
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

// errNoReplicationStream says nobody is streaming to the target right now.
//
// It is its own error because the same fact means two opposite things
// depending on when it is seen: in the seconds after CREATE SUBSCRIPTION the
// subscriber worker has not connected yet and the move only has to wait, while
// minutes later it means the stream died and the copy is silently frozen. The
// caller decides which, by how long the move has been waiting.
var errNoReplicationStream = errors.New("db-move: nothing is streaming to the target, the copy is not current")

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
		return 0, errNoReplicationStream
	}
	if lag < 0 {
		lag = 0
	}
	return lag, nil
}

// errInitialCopyPending says logical replication is still doing its first pass
// over the tables, so the target does not hold the data yet.
//
// It exists because the lag reading cannot see this. pg_stat_replication shows
// the apply worker, whose replay position advances from the moment the
// subscription starts, while the initial COPY of each table runs in separate
// tablesync workers with slots of their own. A move that trusted lag alone read
// "0 bytes behind" over an empty target, cut over, dropped the subscription
// mid-copy and left the tenant pointed at a database with no rows in it. That
// is exactly what happened to six databases on 2026-08-07.
var errInitialCopyPending = errors.New("db-move: the initial table copy has not finished")

// awaitInitialCopy fails with errInitialCopyPending until every table of the
// subscription reports state 'r' (ready) on the target.
//
// The count check is the second half of the guard: a subscription that knows
// about fewer tables than the target has schema for is one whose publication
// was read before those tables existed, and they would never be filled - "no
// pending rows" would otherwise read as success.
func awaitInitialCopy(ctx context.Context, dstDB shardExecutor, datname string) error {
	name := moveObjectName(datname)
	var pending, tracked int
	if err := dstDB.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE r.srsubstate <> 'r'), COUNT(r.srrelid)
		FROM pg_subscription s
		LEFT JOIN pg_subscription_rel r ON r.srsubid = s.oid
		WHERE s.subname = $1
	`, name).Scan(&pending, &tracked); err != nil {
		return fmt.Errorf("db-move: read initial copy state: %w", err)
	}
	var tables int
	if err := dstDB.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p')
		  AND n.nspname NOT LIKE 'pg\_%'
		  AND n.nspname <> 'information_schema'
	`).Scan(&tables); err != nil {
		return fmt.Errorf("db-move: count target tables: %w", err)
	}
	if pending > 0 || tracked != tables {
		return fmt.Errorf("%w: %d tables still copying, %d of %d subscribed",
			errInitialCopyPending, pending, tracked, tables)
	}
	return nil
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
