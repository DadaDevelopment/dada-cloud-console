package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// dbActivityLimit bounds how many connections the live view returns. A
// database holding more than this has a connection problem the list cannot
// help with, and the summary counts still describe it truthfully.
const dbActivityLimit = 50

// dbActivityTimeout bounds the live read. The view is answered from the tenant
// instance while the owner waits, so a shard under load must fail the panel
// rather than hold the request.
const dbActivityTimeout = 5 * time.Second

// connectToTenantDB opens a connection to one logical database on one shard
// using the collector's admin credentials.
//
// It exists for the two endpoints that answer questions no stored sample can:
// what is running right now, and cancel that. Everything else on the insights
// pages reads the control plane, and should keep doing so.
func (h *Handler) connectToTenantDB(ctx context.Context, shard, datname string) (*pgx.Conn, error) {
	if h.cfg == nil {
		return nil, errors.New("collector not configured")
	}
	dsn, ok := parseShardAdminDSNs(h.cfg.DBShardAdminDSNs)[shard]
	if !ok {
		return nil, fmt.Errorf("shard %s has no admin credentials", shard)
	}
	cfg, err := configForDatabase(dsn, datname)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// GetDatabaseActivity lists the connections a database is holding right now.
//
// Unlike the rest of Database Insights this reads the tenant instance live,
// because "what is running" has no useful sampled answer: by the time a
// five-minute sample lands, the query the owner is asking about has finished
// or has been holding a lock for five more minutes. Query text is returned as
// the client sent it -- constants included -- which is why it is never stored
// and why the endpoint is scoped to one database the caller already owns.
//
// @ID          getDatabaseActivity
// @Summary     Connections open on a database right now
// @Description Reads pg_stat_activity on the tenant instance filtered to this database and returns each client connection with its state, how long it has been in that state, what it is waiting on, and the statement it is running. Also returns counts by state and the age of the oldest open transaction. Live read, nothing here is stored.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/activity [get]
func (h *Handler) GetDatabaseActivity(c *gin.Context) {
	_, _, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbActivityTimeout)
	defer cancel()

	conn, err := h.connectToTenantDB(ctx, target.Shard, target.Datname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot reach the database instance right now")
		return
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(ctx, `
		SELECT pid, COALESCE(usename, ''), COALESCE(application_name, ''),
		       COALESCE(client_addr::text, ''), COALESCE(state, ''),
		       COALESCE(wait_event_type, ''), COALESCE(wait_event, ''),
		       EXTRACT(EPOCH FROM (NOW() - state_change)),
		       EXTRACT(EPOCH FROM (NOW() - xact_start)),
		       COALESCE(left(query, 4096), '')
		  FROM pg_stat_activity
		 WHERE datname = current_database()
		   AND backend_type = 'client backend'
		   AND pid <> pg_backend_pid()
		 ORDER BY xact_start ASC NULLS LAST, state_change ASC
		 LIMIT $1`, dbActivityLimit)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read activity right now")
		return
	}
	defer rows.Close()

	connections := []gin.H{}
	var active, idle, inTxn, waiting int
	var oldestXact float64
	for rows.Next() {
		var (
			pid                                       int32
			user, app, addr, state, waitType, waitEvt string
			stateSeconds, xactSeconds                 *float64
			query                                     string
		)
		if err := rows.Scan(&pid, &user, &app, &addr, &state, &waitType, &waitEvt,
			&stateSeconds, &xactSeconds, &query); err != nil {
			respondError(c, http.StatusServiceUnavailable, "cannot read activity right now")
			return
		}
		switch {
		case state == "active":
			active++
		case state == "idle":
			idle++
		case len(state) >= 19 && state[:19] == "idle in transaction":
			inTxn++
		}
		if waitType == "Lock" {
			waiting++
		}
		if xactSeconds != nil && *xactSeconds > oldestXact {
			oldestXact = *xactSeconds
		}
		connections = append(connections, gin.H{
			"pid":             pid,
			"user":            user,
			"applicationName": app,
			"clientAddr":      addr,
			"state":           state,
			"waitEventType":   waitType,
			"waitEvent":       waitEvt,
			"stateSeconds":    nullableSeconds(stateSeconds),
			"xactSeconds":     nullableSeconds(xactSeconds),
			"query":           query,
		})
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot read activity right now")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"connections": connections,
		"summary": gin.H{
			"total":             len(connections),
			"active":            active,
			"idle":              idle,
			"idleInTransaction": inTxn,
			"waitingOnLock":     waiting,
			"oldestXactSeconds": oldestXact,
			"truncated":         len(connections) == dbActivityLimit,
			"collectedAt":       time.Now().UTC(),
		},
	})
}

// nullableSeconds keeps a missing duration missing. A connection that has
// never started a transaction has no transaction age, and reporting it as zero
// would put "0s" next to the oldest transaction on the page.
func nullableSeconds(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// CancelDatabaseBackend asks PostgreSQL to cancel what one connection is
// doing.
//
// pg_cancel_backend, never pg_terminate_backend: cancelling ends the statement
// and rolls the transaction back to the client, which the client's own error
// handling is written for. Terminating drops the connection, and a pool that
// loses a connection under load usually opens another and retries the same
// query. The pid is verified to belong to this database first, so an owner
// cannot reach a backend on a neighbouring tenant by guessing numbers.
//
// @ID          cancelDatabaseBackend
// @Summary     Cancel what one database connection is doing
// @Description Runs pg_cancel_backend against one connection of this database, ending its current statement and rolling its transaction back. The connection itself survives. The pid must belong to this database. Requires write access to the project.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Param       pid       path     int    true "Backend pid to cancel"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/activity/{pid}/cancel [post]
func (h *Handler) CancelDatabaseBackend(c *gin.Context) {
	projectID, envID, target, ok := h.resolveInsightsTarget(c)
	if !ok {
		return
	}
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	pid, err := strconv.Atoi(c.Param("pid"))
	if err != nil || pid <= 0 {
		respondError(c, http.StatusBadRequest, "invalid pid")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbActivityTimeout)
	defer cancel()

	conn, err := h.connectToTenantDB(ctx, target.Shard, target.Datname)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot reach the database instance right now")
		return
	}
	defer conn.Close(context.Background())

	var cancelled bool
	err = conn.QueryRow(ctx, `
		SELECT pg_cancel_backend(pid)
		  FROM pg_stat_activity
		 WHERE pid = $1 AND datname = current_database()
		   AND backend_type = 'client backend'`, pid).Scan(&cancelled)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cannot cancel that connection right now")
		return
	}
	h.recordAudit(ctx, claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "database.backend.cancel",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  c.Param("name"),
		Outcome:       "success",
		Metadata:      map[string]any{"shard": target.Shard, "pid": pid, "cancelled": cancelled},
	})
	c.JSON(http.StatusOK, gin.H{"cancelled": cancelled, "pid": pid})
}
