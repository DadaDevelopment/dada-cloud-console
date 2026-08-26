// db_query_helpers.go holds the plumbing db_query.go needs: resolving the
// caller's OWN database credential (the same Crossplane connection secret
// GetDatabaseCredentials reveals, NOT a separate platform-wide role),
// request-scoped timeout derivation, and audit-row construction (spec
// section 16) that names the real database principal the query ran as.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/pgread/pgexec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dbQueryTimeout bounds the whole request, not just the statement inside
// it: pgexec's own SET LOCAL statement_timeout caps the SQL execution at 5s,
// but the surrounding BEGIN/DECLARE/FETCH*/ROLLBACK round trips plus network
// need their own margin, same rationale as dbActivityTimeout in
// db_activity.go for a live tenant-instance read.
const dbQueryTimeout = 8 * time.Second

func dbQueryContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), dbQueryTimeout)
}

// dbCloseCtx is used for conn.Close after the request context may already be
// canceled, mirroring connectToTenantDB's callers in db_activity.go
// (context.Background() passed to Close so a canceled request context does
// not also abort the connection teardown).
func dbCloseCtx() context.Context { return context.Background() }

// dbQueryTenantTarget is what connectTenantRole needs: the database's own
// name and the (namespace, secretOwner) pair GetDatabaseCredentials already
// uses to locate its Crossplane connection secret.
type dbQueryTenantTarget struct {
	Namespace   string
	SecretOwner string
	Datname     string
}

// dbQueryTargetFromSnapshot re-derives the same (namespace, secretOwner,
// datname) triple GetDatabaseCredentials computes from a ServiceDatabaseV2
// snapshot (databases.go:785-794), so postgres_query/postgres_explain reach
// the exact same connection secret a human with reveal access would see --
// there is intentionally no second, platform-controlled credential in this
// path. Called by db_query.go's resolveDBQueryTarget after auth.
func (h *Handler) dbQueryTargetFromSnapshot(c *gin.Context, projectID, envID uuid.UUID, name string) (dbQueryTenantTarget, bool) {
	var zero dbQueryTenantTarget
	var summaryRaw []byte
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		  WHERE project_id = $1 AND environment_id = $2
		    AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name).Scan(&summaryRaw); err != nil {
		if err == pgx.ErrNoRows {
			respondNotFound(c)
			return zero, false
		}
		respondError(c, http.StatusInternalServerError, "failed to look up database")
		return zero, false
	}

	datname := serviceDatabaseDatname(summaryRaw)
	if datname == "" {
		respondNotFound(c)
		return zero, false
	}

	namespace := serviceDatabaseNamespace(summaryRaw)
	boundAppRef := serviceDatabaseAppRef(summaryRaw)
	secretOwner := boundAppRef
	if secretOwner == "" {
		secretOwner = name
	}
	return dbQueryTenantTarget{Namespace: namespace, SecretOwner: secretOwner, Datname: datname}, true
}

// errDBCredentialsUnavailable is the sentinel db_query.go checks to answer
// the spec's DATABASE_NOT_ACCESSIBLE, covering both "still provisioning, no
// secret yet" and "credential access not configured on this installation" --
// the two ways cloudtask.DBCredentialsResolver.Resolve can fail.
var errDBCredentialsUnavailable = errors.New("database credentials not available")

// connectTenantRole reads the database's OWN connection secret (the same one
// GetDatabaseCredentials reveals to a project writer, the same principal the
// owner's app itself connects as) and opens a pgx connection under it.
//
// This deliberately does NOT introduce a separate, platform-managed
// read-only role: the caller already has exactly this access via reveal +
// psql, so routing through this tool grants nothing new -- it only adds
// policy.Classify's grammar gate and pgexec's READ ONLY transaction (which
// Postgres itself enforces at the engine level regardless of the
// connecting role's own grants, so it stays a real, independent control
// even though the role behind it is not narrowed). See db_query.go's
// package doc for the resulting two-layer model.
func (h *Handler) connectTenantRole(ctx context.Context, target dbQueryTenantTarget) (*pgx.Conn, string, error) {
	creds, err := h.dbcreds.Resolve(ctx, target.Namespace, target.SecretOwner)
	if err != nil {
		if errors.Is(err, cloudtask.ErrDBCredentialsNotReady) {
			return nil, "", fmt.Errorf("%w: still provisioning", errDBCredentialsUnavailable)
		}
		return nil, "", fmt.Errorf("%w: %v", errDBCredentialsUnavailable, err)
	}

	host := creds.Endpoint
	if host == "" {
		return nil, "", fmt.Errorf("%w: no connection endpoint in secret", errDBCredentialsUnavailable)
	}
	dsn := postgresDSN(creds.Username, creds.Password, host, creds.Port, target.Datname)
	if dsn == "" {
		return nil, "", fmt.Errorf("%w: could not build a connection string", errDBCredentialsUnavailable)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("connect as %s: %w", creds.Username, err)
	}
	return conn, creds.Username, nil
}

// recordDBQueryAudit writes the spec's section-16 audit event, naming the
// REAL database principal the query executed as (creds.Username from
// connectTenantRole) alongside the platform actor who called the tool --
// two identities, both true: who asked (claims.UserID, already the
// recordAudit actor) and which database role actually ran the statement.
// The full SQL text is deliberately NOT stored (it may carry caller-chosen
// literal values, i.e. the tenant's own data) -- only a hash of the
// classified/deparsed SQL and the relations it touched.
func (h *Handler) recordDBQueryAudit(c *gin.Context, target dbQueryTenantTarget, dbUser, tool, sql string, result *pgexec.Result, relations []string) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		Action:       "database." + tool,
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: c.Param("name"),
		Outcome:      "success",
		Metadata: map[string]any{
			"database":   target.Datname,
			"dbUser":     dbUser,
			"tool":       tool,
			"queryHash":  dbQueryHash(sql),
			"relations":  relations,
			"rowCount":   result.RowCount,
			"truncated":  result.Truncated,
			"durationMs": result.DurationMs,
		},
	})
}

// dbQueryHash normalizes and hashes SQL for the audit trail's query_hash
// field (spec section 16), grouping identical query shapes without storing
// literal values. sha256 of the already-deparsed, already-classified SQL is
// a safe choice because that text is grammar-validated, not raw caller
// input -- it cannot itself carry a comment or literal the caller tried to
// smuggle through (see policy.Classify's Deparse step).
func dbQueryHash(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])[:16]
}
