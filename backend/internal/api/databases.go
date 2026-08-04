package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// randomPassword returns a 32-hex-char (16-byte) secret suitable for a managed
// database credential.
func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ListDatabases returns all ServiceDatabase resources in a project environment.
//
// @ID          listDatabases
// @Summary     List databases in an environment
// @Description Returns all managed PostgreSQL (ServiceDatabaseV2) resources in the given project environment, with their live phase/status.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with a databases array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases [get]
func (h *Handler) ListDatabases(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	// Verify membership
	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query databases")
		return
	}
	defer rows.Close()

	var databases []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan database")
			return
		}
		databases = append(databases, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading databases")
		return
	}
	if databases == nil {
		databases = []models.ResourceSnapshot{}
	}

	enrichDatabaseSizes(c.Request.Context(), h, databases)

	c.JSON(http.StatusOK, gin.H{"databases": databases})
}

// enrichDatabaseSizes injects the live on-disk size of each database (Postgres
// `pg_database_size_bytes`, keyed by the CR's spec.database == datname) into the
// snapshot summary as size_bytes. Best-effort: one batched query, silent on any
// error so the list still renders without sizes.
func enrichDatabaseSizes(ctx context.Context, h *Handler, dbs []models.ResourceSnapshot) {
	if h.prometheus == nil || len(dbs) == 0 {
		return
	}
	samples, err := h.prometheus.QueryInstant(ctx, "pg_database_size_bytes", time.Time{}, "")
	if err != nil {
		return
	}
	sizeByDatname := make(map[string]float64, len(samples))
	for _, s := range samples {
		if dn := s.Metric["datname"]; dn != "" {
			sizeByDatname[dn] = s.Point.V
		}
	}
	for i := range dbs {
		var summary map[string]any
		if err := json.Unmarshal(dbs[i].SummaryJSON, &summary); err != nil || summary == nil {
			continue
		}
		spec, ok := summary["spec"].(map[string]any)
		if !ok {
			continue
		}
		datname, _ := spec["database"].(string)
		if datname == "" {
			continue
		}
		if size, ok := sizeByDatname[datname]; ok {
			summary["size_bytes"] = int64(size)
			if patched, err := json.Marshal(summary); err == nil {
				dbs[i].SummaryJSON = patched
			}
		}
	}
}

type createServiceDatabaseRequest struct {
	Name            string `json:"name"`
	Database        string `json:"database"`
	AppRef          string `json:"app_ref"`
	BackupEnabled   bool   `json:"backup_enabled"`
	BackupSchedule  string `json:"backup_schedule"`
	BackupRetention string `json:"backup_retention"`
}

// seedEnvVar upserts one encrypted runtime env var for an app in an environment
// (used to inject managed-database credentials/DSN for the VM compose track).
func (h *Handler) seedEnvVar(ctx context.Context, envID uuid.UUID, appName, key, value string, createdBy uuid.UUID) error {
	enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(value))
	if err != nil {
		return err
	}
	_, err = h.pool.Exec(ctx,
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
		 VALUES ($1, $2, $3, $4, TRUE, 'runtime', $5)
		 ON CONFLICT (environment_id, app_name, key)
		 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted, is_secret = EXCLUDED.is_secret,
		               scope = EXCLUDED.scope, updated_at = NOW()`,
		envID, appName, key, enc, createdBy,
	)
	return err
}

// seedOptimisticSnapshot writes a Pending resource_snapshots row inside a create transaction, so
// a newly ordered resource (database, app, endpoint, bucket, model) is visible in every read
// surface (overview badge, list page, MCP) the instant the API returns 202 -- before the gitops
// worker claims the operation, pushes to git and seeds the row itself. The worker's later
// UpsertSnapshot and the status reconciler advance this same row in place to its live phase;
// DBWatcher.cleanupFailedOptimisticSnapshot deletes it if the create operation fails. Callers
// therefore only ever observe closed, valid states. live_source=create-optimistic is stamped so
// the row is identifiable; ON CONFLICT DO NOTHING keeps it idempotent and never clobbers a
// concurrent worker write.
func seedOptimisticSnapshot(ctx context.Context, tx pgx.Tx, projectID, envID uuid.UUID, kind, name string, summary map[string]any) error {
	if summary == nil {
		summary = map[string]any{}
	}
	summary["live_source"] = "create-optimistic"
	if _, ok := summary["status"]; !ok {
		summary["status"] = "Pending"
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO resource_snapshots
			(project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, $3, $4, 'Pending', $5, now())
		 ON CONFLICT (project_id, environment_id, kind, name) DO NOTHING`,
		projectID, envID, kind, name, raw,
	)
	return err
}

// CreateServiceDatabase enqueues an operation to provision a new ServiceDatabase CRD.
//
// @ID          createDatabase
// @Summary     Order a managed PostgreSQL database
// @Description Provisions a new managed PostgreSQL database (ServiceDatabaseV2). app_ref is optional: omit it for a standalone, environment-level database, or set it to bind the database to an app's chart. Asynchronous: returns 202 with an operation; poll the operation until it reaches a terminal status.
// @Tags        database
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                        true "Project UUID"
// @Param       envId     path     string                        true "Environment UUID"
// @Param       body      body     createServiceDatabaseRequest  true "Database specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases [post]
func (h *Handler) CreateServiceDatabase(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var req createServiceDatabaseRequest
	dbName := func() string { return req.Name }

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "CreateServiceDatabase",
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  dbName(),
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	// Check write permission
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_member", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "membership_check_failed", "failed to check project membership")
		return
	}
	if !canWrite(role) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "read_only_role", "status": http.StatusForbidden})
		respondForbidden(c)
		return
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "databases"); qErr != nil {
			if meta, blocked := billingBlockAudit(qErr); blocked {
				meta["status"] = http.StatusPaymentRequired
				audit(uuid.Nil, auditOutcomeFailure, meta)
				h.respondBillingBlocked(c, orgID, qErr)
				return
			}
		}
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}

	// Validate fields
	if req.Name == "" {
		rejectErr(http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if req.Database == "" {
		rejectErr(http.StatusBadRequest, "database_required", "database is required")
		return
	}
	// app_ref is optional: empty = standalone, environment-level database that
	// owns its own chart. When set, the database is bound to that app's chart.
	if err := validateKubeName(req.Name); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}
	if err := validatePgName(req.Database); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_database_name", err.Error())
		return
	}

	// Check name uniqueness in resource_snapshots for this project/env
	var existing int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		rejectErr(http.StatusConflict, "name_taken", "a database with that name already exists in this environment")
		return
	}

	// VM (compose) environments render the managed database as a platform-owned
	// Application in the environment's aggregate stack (postgres image + external
	// volume). The backend generates the credential and seeds env vars now (it
	// holds the encryption key); the gitops worker just materialises the App and
	// re-assembles the stack. k8s keeps the Crossplane path (engine stays empty).
	var runtime string
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT runtime FROM environments WHERE id = $1`, envID).Scan(&runtime)

	engine := ""
	if runtime == "vm" {
		engine = "postgres"
		password, perr := randomPassword()
		if perr != nil {
			rejectErr(http.StatusInternalServerError, "credential_generation_failed", "failed to generate database credential")
			return
		}
		const dbUser = "dada"
		for _, kv := range [][2]string{
			{"POSTGRES_PASSWORD", password},
			{"POSTGRES_DB", req.Database},
			{"POSTGRES_USER", dbUser},
		} {
			if err := h.seedEnvVar(c.Request.Context(), envID, req.Name, kv[0], kv[1], claims.UserID); err != nil {
				rejectErr(http.StatusInternalServerError, "seed_credentials_failed", "failed to seed database credentials")
				return
			}
		}
		if req.AppRef != "" {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, password, req.Name, req.Database)
			if err := h.seedEnvVar(c.Request.Context(), envID, req.AppRef, "DATABASE_URL", dsn, claims.UserID); err != nil {
				rejectErr(http.StatusInternalServerError, "seed_dsn_failed", "failed to inject database connection string")
				return
			}
		}
	}

	// Marshal payload
	payload := models.CreateServiceDatabasePayload{
		Name:            req.Name,
		Database:        req.Database,
		AppRef:          req.AppRef,
		Engine:          engine,
		BackupEnabled:   req.BackupEnabled,
		BackupSchedule:  req.BackupSchedule,
		BackupRetention: req.BackupRetention,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		rejectErr(http.StatusInternalServerError, "tx_begin_failed", "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateServiceDatabase', 'ServiceDatabaseV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "ServiceDatabaseV2", req.Name, map[string]any{
		"name":     req.Name,
		"kind":     "ServiceDatabaseV2",
		"app_ref":  req.AppRef,
		"database": req.Database,
		"spec": map[string]any{
			"appRef":   req.AppRef,
			"database": req.Database,
		},
	}); err != nil {
		rejectErr(http.StatusInternalServerError, "snapshot_seed_failed", "failed to create operation")
		return
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		rejectErr(http.StatusInternalServerError, "tx_commit_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{
		"database":         req.Database,
		"app_ref":          req.AppRef,
		"engine":           engine,
		"runtime":          runtime,
		"backup_enabled":   req.BackupEnabled,
		"backup_schedule":  req.BackupSchedule,
		"backup_retention": req.BackupRetention,
	})
	h.notifyAuditEvent(claims, projectID, "CreateServiceDatabase", req.Name)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "ServiceDatabase creation queued",
	})
}

// DeleteServiceDatabase enqueues an operation to tear down a managed
// PostgreSQL database (ServiceDatabaseV2).
//
// @ID          deleteDatabase
// @Summary     Delete a managed PostgreSQL database
// @Description Destructive: permanently removes a managed PostgreSQL database (ServiceDatabaseV2) and its data. The agent drops the CR entry from git and Argo prunes it. Asynchronous: returns 202 with an operation; poll the operation until terminal. Only k8s (Crossplane) databases are deletable here; VM-hosted databases are managed as apps.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name} [delete]
func (h *Handler) DeleteServiceDatabase(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "DeleteServiceDatabase",
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}

	var summaryRaw []byte
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up database")
		return
	}

	appRef := serviceDatabaseAppRef(summaryRaw)

	payload := models.DeleteServiceDatabasePayload{Name: name, AppRef: appRef}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteServiceDatabase', 'ServiceDatabaseV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{"app_ref": appRef})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "ServiceDatabase deletion queued",
	})
}

// serviceDatabaseAppRef pulls spec.appRef from a ServiceDatabaseV2 snapshot's
// summary_json (empty when standalone, or when the summary is unparseable),
// telling the agent which owner app's resources.values.yaml holds the CR entry.
func serviceDatabaseAppRef(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	spec, ok := summary["spec"].(map[string]any)
	if !ok {
		return ""
	}
	appRef, _ := spec["appRef"].(string)
	return appRef
}

// GetDatabaseCredentials reveals a managed PostgreSQL database's connection
// credentials by reading its Crossplane connection secret on demand.
//
// @ID          getDatabaseCredentials
// @Summary     Reveal a database's connection credentials
// @Description Reveals the host, port, database name, username and password for a managed PostgreSQL database (ServiceDatabaseV2) by reading its Crossplane connection secret. Requires reveal=true and write access; every reveal is audited. Returns 404 while the database is still provisioning (no secret yet) and 503 when in-cluster credential access is not configured.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Param       reveal    query    bool   true "Must be true to reveal the credentials"
// @Success     200       {object} map[string]interface{} "object with host, port, database, username, password"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/credentials [get]
func (h *Handler) GetDatabaseCredentials(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")

	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        auditActionRevealDBCreds,
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}
	if c.Query("reveal") != "true" {
		rejectErr(http.StatusBadRequest, "reveal_not_confirmed", "reveal=true is required")
		return
	}

	var summaryRaw []byte
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw); err != nil {
		if err == pgx.ErrNoRows {
			audit(auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
			respondNotFound(c)
			return
		}
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up database")
		return
	}

	// The connection secret is named "<appRef>-db-credentials" (the CRD ties the
	// secret name to spec.appRef, not the resource name — e.g. a DB named
	// "mlflow-v2" bound to app "mlflow-db" publishes "mlflow-db-db-credentials").
	// Standalone DBs self-own (appRef defaults to the DB name), so fall back to name.
	namespace := serviceDatabaseNamespace(summaryRaw)
	secretOwner := serviceDatabaseAppRef(summaryRaw)
	if secretOwner == "" {
		secretOwner = name
	}
	creds, err := h.dbcreds.Resolve(c.Request.Context(), namespace, secretOwner)
	if err != nil {
		if errors.Is(err, cloudtask.ErrDBCredentialsNotReady) {
			rejectErr(http.StatusNotFound, "secret_not_ready", "credentials not available yet — the database is still provisioning")
			return
		}
		rejectErr(http.StatusServiceUnavailable, "credential_access_unconfigured", "database credential access is not configured for this environment")
		return
	}

	// host/port come from the connection secret written by the ServiceDatabaseV2
	// composition — the AUTHORITATIVE endpoint the app actually connects to (a
	// shared managed-Postgres Service, e.g. postgresql.databases.svc.cluster.local),
	// NOT a per-database Service in the app namespace (none exists). Fall back to a
	// derived in-namespace name only if the secret omits the endpoint.
	database := serviceDatabaseDatname(summaryRaw)
	host := creds.Endpoint
	if host == "" {
		host = name
		if namespace != "" {
			host = fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
		}
	}
	port := creds.Port
	if port == "" {
		port = "5432"
	}

	audit(auditOutcomeSuccess, map[string]any{"revealed": true, "namespace": namespace})

	resp := gin.H{
		"host":     host,
		"port":     port,
		"database": database,
		"username": creds.Username,
		"password": creds.Password,
	}
	extHost, extPort := creds.ExternalHost, creds.ExternalPort
	if extHost != "" {
		resp["external_host"] = extHost
		if extPort == "" {
			extPort = port
		}
		resp["external_port"] = extPort
	}
	c.JSON(http.StatusOK, resp)
}

// serviceDatabaseNamespace pulls the app namespace from a ServiceDatabaseV2
// snapshot's summary_json (spec.namespace, or the top-level namespace mirror),
// which is where the composition writes the "<db>-db-credentials" secret. Empty
// when the summary is unparseable or the database has no namespace yet.
func serviceDatabaseNamespace(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	if spec, ok := summary["spec"].(map[string]any); ok {
		if ns, _ := spec["namespace"].(string); ns != "" {
			return ns
		}
	}
	ns, _ := summary["namespace"].(string)
	return ns
}

// serviceDatabaseDatname pulls the Postgres database name (spec.database, or the
// top-level database mirror) from a ServiceDatabaseV2 snapshot's summary_json.
func serviceDatabaseDatname(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	if spec, ok := summary["spec"].(map[string]any); ok {
		if d, _ := spec["database"].(string); d != "" {
			return d
		}
	}
	d, _ := summary["database"].(string)
	return d
}
