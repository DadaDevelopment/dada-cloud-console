// db_query.go exposes read-only ad-hoc SQL against a managed database as
// two REST endpoints, following the reflective MCP model this repo uses
// (backend/internal/mcp/toolgen.go): swaggo-annotated Gin handlers ARE the
// MCP tool surface -- the MCP tool for postgres_query is not hand-written
// anywhere, it is generated from THIS endpoint's @ID/@Summary/@Description
// and its JSON body schema, the same way getDatabaseActivity became a tool
// with no MCP-specific code.
//
// Credential model -- deliberately NOT a separate platform-wide read-only
// role: this endpoint connects as the database's OWN principal, reading the
// exact same Crossplane connection secret GetDatabaseCredentials reveals to
// a project writer (db_query_helpers.go's connectTenantRole shares
// resolveDBQueryTarget's derivation with GetDatabaseCredentials in
// databases.go: same namespace/secretOwner lookup, same secret). Three
// consequences, on purpose:
//   - The audit trail names the REAL database user the statement ran as
//     (Metadata.dbUser), not a shared platform identity every agent call
//     would otherwise collapse into.
//   - A caller gets exactly the SQL access they already have via reveal +
//     psql -- this tool adds a grammar gate and a forced READ ONLY
//     transaction on top of that access, it does not grant a NEW capability
//     the caller lacked.
//   - There is no separate role-provisioning step: the endpoint works the
//     moment a database's connection secret exists, same lifecycle as
//     reveal-credentials. No runbook, no extra DSN config.
//
// Layering that IS still independent of the connecting role's own grants:
//  1. policy.Classify (internal/pgread/policy) -- grammar-level allow/deny;
//     rejects INSERT/UPDATE/DELETE/DDL/dangerous functions before the
//     statement is ever sent to Postgres, regardless of what the connecting
//     role is permitted to do.
//  2. pgexec (internal/pgread/pgexec) -- a READ ONLY transaction is a
//     PostgreSQL engine-level restriction (SQLSTATE 25006 on any write
//     attempt) that applies independently of the role's own privileges, so
//     it still holds even though the role behind this connection is the
//     app's normal, non-restricted principal. Always rolled back, 5s
//     statement timeout, cursor-based 1000-row/5MB cap.
package api

import (
	"errors"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/pgread/pgexec"
	"github.com/dada-tuda/console/backend/internal/pgread/policy"
	"github.com/gin-gonic/gin"
)

// dbQueryRequest is the postgres_query / postgres_explain tool input, per
// the spec's input_schema. Params are positional $1..$n bind values, never
// string-interpolated into Query.
type dbQueryRequest struct {
	Query  string `json:"query" binding:"required"`
	Params []any  `json:"params"`
}

type dbExplainRequest struct {
	Query  string `json:"query" binding:"required"`
	Format string `json:"format"`
}

// resolveDBQueryTarget authorizes the caller (write access on the project --
// the same bar GetDatabaseCredentials uses, since this tool reaches the same
// credential a reveal would) and resolves the database's connection-secret
// coordinates. Mirrors GetDatabaseCredentials's own auth + lookup exactly
// (databases.go:733-794) rather than reusing resolveInsightsTarget, which
// only requires read access -- read access is NOT enough here because this
// tool, unlike insights, executes with the database's real write-capable
// credential (constrained only by the READ ONLY transaction).
func (h *Handler) resolveDBQueryTarget(c *gin.Context) (dbQueryTenantTarget, bool) {
	var zero dbQueryTenantTarget
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return zero, false
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return zero, false
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return zero, false
	}
	return h.dbQueryTargetFromSnapshot(c, projectID, envID, c.Param("name"))
}

// QueryDatabase runs a single read-only SQL statement against a managed
// database and returns its rows.
//
// @ID          queryDatabase
// @Summary     Run a read-only SQL query against this database
// @Description Executes a single read-only SQL statement (SELECT or WITH ... SELECT) against this managed PostgreSQL database, connecting as the database's own credential (the same one reveal-credentials exposes) inside a READ ONLY transaction that is always rolled back, under a 5 second statement timeout, capped at 1000 rows / 5 MB. INSERT/UPDATE/DELETE/DDL/administrative functions (pg_sleep, pg_read_file, dblink, set_config, ...) are rejected before reaching the database by SQL-grammar classification, and PostgreSQL itself refuses any write inside the READ ONLY transaction regardless of the connecting role's own privileges. Multi-statement input is rejected. Params are positional $1, $2, ... bind values -- never interpolate values into the query text. Every call is audited under both the calling platform user and the database role the statement actually ran as.
// @Tags        database
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string         true "Project UUID"
// @Param       envId     path     string         true "Environment UUID"
// @Param       name      path     string         true "Database resource name"
// @Param       body      body     dbQueryRequest true "Query and optional bind params"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]interface{} "QUERY_PARSE_ERROR, QUERY_MULTI_STATEMENT, QUERY_NOT_READ_ONLY, QUERY_FORBIDDEN_CONSTRUCT or QUERY_FORBIDDEN_FUNCTION"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]interface{} "DATABASE_NOT_ACCESSIBLE, QUERY_TIMEOUT, LOCK_TIMEOUT or DATABASE_ERROR"
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/query [post]
func (h *Handler) QueryDatabase(c *gin.Context) {
	target, ok := h.resolveDBQueryTarget(c)
	if !ok {
		return
	}

	var req dbQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorCode(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	cls, err := policy.Classify(req.Query)
	if err != nil {
		respondPolicyError(c, err)
		return
	}
	if cls.Kind != policy.KindSelect {
		// A caller hitting /query with an EXPLAIN statement gets routed to
		// the wrong endpoint's semantics; /explain exists precisely so the
		// gateway -- not the caller -- controls which fixed EXPLAIN options
		// run. Reject rather than silently reinterpreting intent.
		respondErrorCode(c, http.StatusBadRequest, string(policy.CodeNotReadOnly),
			"Use the /explain endpoint for EXPLAIN queries.")
		return
	}

	ctx, cancel := dbQueryContext(c)
	defer cancel()

	conn, dbUser, err := h.connectTenantRole(ctx, target)
	if err != nil {
		respondDBCredentialError(c, err)
		return
	}
	defer conn.Close(dbCloseCtx())

	result, err := pgexec.ExecuteQuery(ctx, conn, cls.SQL, req.Params)
	if err != nil {
		respondExecError(c, err)
		return
	}

	h.recordDBQueryAudit(c, target, dbUser, "postgres_query", cls.SQL, result, cls.Relations)
	c.JSON(http.StatusOK, result)
}

// ExplainDatabaseQuery returns the query plan for a read-only statement
// without executing it in the sense of producing rows the caller can read
// as data -- EXPLAIN alone (no ANALYZE) never runs the plan.
//
// @ID          explainDatabaseQuery
// @Summary     Get the query plan for a read-only query, without running it
// @Description Returns the PostgreSQL query plan for a read-only SELECT / WITH ... SELECT against this database, connecting as the database's own credential. ANALYZE, BUFFERS and WAL are never available as options here -- the gateway always wraps the statement as EXPLAIN (VERBOSE, COSTS, FORMAT <format>), so this endpoint cannot execute the statement even if the caller tries to request it.
// @Tags        database
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       envId     path     string           true "Environment UUID"
// @Param       name      path     string           true "Database resource name"
// @Param       body      body     dbExplainRequest true "Query to explain; format is text or json"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]interface{}
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/explain [post]
func (h *Handler) ExplainDatabaseQuery(c *gin.Context) {
	target, ok := h.resolveDBQueryTarget(c)
	if !ok {
		return
	}

	var req dbExplainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorCode(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	cls, err := policy.Classify(req.Query)
	if err != nil {
		respondPolicyError(c, err)
		return
	}
	if cls.Kind != policy.KindSelect {
		respondErrorCode(c, http.StatusBadRequest, string(policy.CodeNotReadOnly),
			"Only a plain SELECT / WITH ... SELECT may be explained; do not wrap the query in EXPLAIN yourself.")
		return
	}

	ctx, cancel := dbQueryContext(c)
	defer cancel()

	conn, dbUser, err := h.connectTenantRole(ctx, target)
	if err != nil {
		respondDBCredentialError(c, err)
		return
	}
	defer conn.Close(dbCloseCtx())

	result, err := pgexec.ExecuteExplain(ctx, conn, cls.SQL, req.Format)
	if err != nil {
		respondExecError(c, err)
		return
	}

	h.recordDBQueryAudit(c, target, dbUser, "postgres_explain", cls.SQL, result, cls.Relations)
	c.JSON(http.StatusOK, result)
}

// respondPolicyError maps a policy.Error (rejected before ever reaching
// Postgres) onto the spec's error model (section 18): HTTP 400, body
// {"error": message, "code": QUERY_*}.
func respondPolicyError(c *gin.Context, err error) {
	var pe *policy.Error
	if errors.As(err, &pe) {
		respondErrorCode(c, http.StatusBadRequest, string(pe.Code), pe.Message)
		return
	}
	respondErrorCode(c, http.StatusBadRequest, "QUERY_PARSE_ERROR", err.Error())
}

// respondDBCredentialError distinguishes "this database's connection
// credential is not resolvable right now" (still provisioning, or
// credential access unconfigured on this installation) -- the spec's
// DATABASE_NOT_ACCESSIBLE -- from a generic transient connectivity failure.
func respondDBCredentialError(c *gin.Context, err error) {
	if errors.Is(err, errDBCredentialsUnavailable) {
		respondErrorCode(c, http.StatusServiceUnavailable, "DATABASE_NOT_ACCESSIBLE", err.Error())
		return
	}
	respondErrorCode(c, http.StatusServiceUnavailable, "DATABASE_NOT_ACCESSIBLE",
		"cannot reach the database instance right now")
}

// respondExecError maps a pgexec-layer failure. pgexec.mapErr already
// translates *pgconn.PgError into a *policy.Error carrying one of
// QUERY_TIMEOUT / LOCK_TIMEOUT / QUERY_NOT_READ_ONLY / DATABASE_ERROR, so
// this stays a thin HTTP-status decision on top of that shared vocabulary.
func respondExecError(c *gin.Context, err error) {
	var pe *policy.Error
	if errors.As(err, &pe) {
		status := http.StatusServiceUnavailable
		if pe.Code == policy.CodeNotReadOnly || pe.Code == policy.CodeForbiddenConstruct ||
			pe.Code == policy.CodeForbiddenFunction {
			status = http.StatusBadRequest
		}
		respondErrorCode(c, status, string(pe.Code), pe.Message)
		return
	}
	respondError(c, http.StatusServiceUnavailable, "cannot run that query right now")
}
