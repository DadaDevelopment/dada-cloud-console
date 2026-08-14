package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// databaseTierByPlan maps a billing plan key onto the ServiceDatabaseV2 quota
// tier declared by the crossplane-platform-api chart. The tier decides the
// role's CONNECTION LIMIT and its per-role postgres parameters (statement and
// idle-in-transaction timeouts, temp_file_limit, work_mem) — the isolation that
// keeps one tenant from starving the shared instance.
//
// enterprise deliberately lands on the same ceiling as business rather than on
// "unlimited": an unbounded role is exactly the failure mode the tiers exist to
// prevent. A genuinely larger enterprise gets its own pool, not no limits.
var databaseTierByPlan = map[string]string{
	"free":       "free",
	"startup":    "starter",
	"business":   "business",
	"enterprise": "business",
}

// databaseTierFor resolves the quota tier for an org's databases. An unknown or
// unresolvable plan yields "" — the XRD default ("unlimited"), i.e. today's
// behaviour — so a transient billing-lookup failure never cripples a paying
// tenant's new database. Drift is corrected by the tier reconciler, not here.
func (h *Handler) databaseTierFor(ctx context.Context, orgID string) string {
	if h.dbQuotaExemptOrg(orgID) {
		return dbTierInternal
	}
	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		return ""
	}
	return databaseTierByPlan[plan.Key]
}

// dbQuotaExemptOrg reports whether the org owns platform databases rather than
// customer ones (DB_QUOTA_EXEMPT_ORGS). Its databases are tiered "internal",
// which carries no storage limit, so neither the create path nor the tier
// reconciler can hand the control plane a quota to hit.
func (h *Handler) dbQuotaExemptOrg(orgID string) bool {
	if orgID == "" || h.cfg == nil {
		return false
	}
	for _, exempt := range h.cfg.DBQuotaExemptOrgs {
		if exempt == orgID {
			return true
		}
	}
	return false
}

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

// opFault is a rejection a shared handler core hands back to whichever endpoint
// called it: the HTTP status to answer with, the machine-readable reason the
// audit trail records, and the sentence the customer reads. It exists so a core
// can be reused by a second endpoint without either endpoint inventing its own
// status codes or audit vocabulary for the same failure.
type opFault struct {
	Status  int
	Reason  string
	Message string
}

// Error makes opFault usable where an error is expected.
func (f *opFault) Error() string { return f.Message }

// managedDatabaseResult is what provisioning a managed database produced: the
// queued operation, plus the runtime and engine the caller needs for its audit
// record — both are decided inside the core from the environment, so a caller
// that guessed them would be recording a guess. AppRef is the binding actually
// used (the caller's request value, or the sole-app auto-resolution below),
// which can differ from the request the caller sent.
type managedDatabaseResult struct {
	Operation models.Operation
	Runtime   string
	Engine    string
	Shard     string
	AppRef    string
}

// resolveSoleAppRef auto-binds a database to an app when the caller left
// app_ref empty and the environment holds exactly one App resource: any other
// count (zero, or two-plus) is ambiguous and is left unresolved rather than
// guessed. The console has no app picker on the create-database form today, so
// every managed database is ordered with app_ref="" -- without this, no
// Crossplane database is ever bound to its app, deliverDatabaseDSNAsync never
// starts, and DATABASE_URL is never seeded.
func (h *Handler) resolveSoleAppRef(ctx context.Context, projectID, envID uuid.UUID) (string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App'`,
		projectID, envID,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var only string
	count := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		count++
		only = name
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count != 1 {
		return "", nil
	}
	return only, nil
}

// createManagedDatabase validates a database request and queues its operation.
//
// This is the whole body of CreateServiceDatabase below except for
// authentication, membership, quota and audit, which stay in the endpoint. It
// is separated so ordering a database as part of installing a ready-made
// project goes through exactly this code: the VM track's credential generation
// and DSN injection are the kind of rules that quietly diverge when a second
// caller reimplements them, and a diverged copy hands the customer an app that
// cannot reach its own database.
//
// VM (compose) environments render the managed database as a platform-owned
// Application in the environment's aggregate stack (postgres image plus an
// external volume). The backend generates the credential and seeds the env vars
// here because it holds the encryption key; the gitops worker only materialises
// the App and re-assembles the stack. k8s keeps the Crossplane path, where the
// chart binds the database to the app through app_ref, so engine stays empty
// and no DSN is seeded.
//
// Quota tier and shard placement belong to the Crossplane path only: a VM
// compose database is a container of its own and is bounded by its own limits.
//
// A Crossplane database bound to an app (app_ref set) has no credential to
// seed here: its connection secret does not exist until the composition
// finishes reconciling, so the DSN handoff cannot happen inline the way the VM
// branch above does it. deliverDatabaseDSNAsync (db_dsn_delivery.go) is
// spawned after commit to wait for that secret and inject DATABASE_URL once it
// appears; GetDatabaseCredentials' reveal path runs the same idempotent
// seedDatabaseDSNIfAbsent synchronously as a manual fallback.
func (h *Handler) createManagedDatabase(ctx context.Context, actorID, projectID, envID uuid.UUID, req createServiceDatabaseRequest) (*managedDatabaseResult, *opFault) {
	if req.Name == "" {
		return nil, &opFault{http.StatusBadRequest, "name_required", "name is required"}
	}
	if req.Database == "" {
		return nil, &opFault{http.StatusBadRequest, "database_required", "database is required"}
	}
	if err := validateKubeName(req.Name); err != nil {
		return nil, &opFault{http.StatusBadRequest, "invalid_name", err.Error()}
	}
	if err := validatePgName(req.Database); err != nil {
		return nil, &opFault{http.StatusBadRequest, "invalid_database_name", err.Error()}
	}

	var existing int
	if err := h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing); err != nil {
		return nil, &opFault{http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness"}
	}
	if existing > 0 {
		return nil, &opFault{http.StatusConflict, "name_taken", "a database with that name already exists in this environment"}
	}

	var runtime string
	_ = h.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, envID).Scan(&runtime)

	appRef := req.AppRef
	if appRef == "" {
		if resolved, rerr := h.resolveSoleAppRef(ctx, projectID, envID); rerr == nil {
			appRef = resolved
		}
	}

	engine := ""
	if runtime == "vm" {
		engine = "postgres"
		password, perr := randomPassword()
		if perr != nil {
			return nil, &opFault{http.StatusInternalServerError, "credential_generation_failed", "failed to generate database credential"}
		}
		const dbUser = "dada"
		for _, kv := range [][2]string{
			{"POSTGRES_PASSWORD", password},
			{"POSTGRES_DB", req.Database},
			{"POSTGRES_USER", dbUser},
		} {
			if err := h.seedEnvVar(ctx, envID, req.Name, kv[0], kv[1], actorID); err != nil {
				return nil, &opFault{http.StatusInternalServerError, "seed_credentials_failed", "failed to seed database credentials"}
			}
		}
		if appRef != "" {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", dbUser, password, req.Name, req.Database)
			if err := h.seedEnvVar(ctx, envID, appRef, "DATABASE_URL", dsn, actorID); err != nil {
				return nil, &opFault{http.StatusInternalServerError, "seed_dsn_failed", "failed to inject database connection string"}
			}
		}
	}

	tier := ""
	shard := ""
	if engine == "" {
		if orgID, orgErr := h.projectOrg(ctx, projectID); orgErr == nil {
			tier = h.databaseTierFor(ctx, orgID)
		}
		shard = h.placeTenantDatabaseShard(ctx)
	}

	payload := models.CreateServiceDatabasePayload{
		Name:            req.Name,
		Database:        req.Database,
		AppRef:          appRef,
		Engine:          engine,
		Tier:            tier,
		Shard:           shard,
		BackupEnabled:   req.BackupEnabled,
		BackupSchedule:  req.BackupSchedule,
		BackupRetention: req.BackupRetention,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, &opFault{http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload"}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, &opFault{http.StatusInternalServerError, "tx_begin_failed", "failed to create operation"}
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var op models.Operation
	row := tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateServiceDatabase', 'ServiceDatabaseV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		actorID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		return nil, &opFault{http.StatusInternalServerError, "operation_insert_failed", "failed to create operation"}
	}

	if err = seedOptimisticSnapshot(ctx, tx, projectID, envID, "ServiceDatabaseV2", req.Name, map[string]any{
		"name":     req.Name,
		"kind":     "ServiceDatabaseV2",
		"app_ref":  appRef,
		"database": req.Database,
		"spec": map[string]any{
			"appRef":   appRef,
			"database": req.Database,
		},
	}); err != nil {
		return nil, &opFault{http.StatusInternalServerError, "snapshot_seed_failed", "failed to create operation"}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, &opFault{http.StatusInternalServerError, "tx_commit_failed", "failed to create operation"}
	}

	if engine == "" && appRef != "" {
		go h.deliverDatabaseDSNAsync(context.Background(), projectID, envID, req.Name, appRef, actorID)
	}

	return &managedDatabaseResult{Operation: op, Runtime: runtime, Engine: engine, Shard: shard, AppRef: appRef}, nil
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

	// app_ref is optional: empty = standalone, environment-level database that
	// owns its own chart. When set, the database is bound to that app's chart.
	res, fault := h.createManagedDatabase(c.Request.Context(), claims.UserID, projectID, envID, req)
	if fault != nil {
		rejectErr(fault.Status, fault.Reason, fault.Message)
		return
	}

	audit(res.Operation.ID, auditOutcomeSuccess, map[string]any{
		"database":         req.Database,
		"app_ref":          res.AppRef,
		"app_ref_resolved": res.AppRef != "" && req.AppRef == "",
		"engine":           res.Engine,
		"runtime":          res.Runtime,
		"shard":            res.Shard,
		"backup_enabled":   req.BackupEnabled,
		"backup_schedule":  req.BackupSchedule,
		"backup_retention": req.BackupRetention,
	})
	h.notifyAuditEvent(claims, projectID, "CreateServiceDatabase", req.Name)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": res.Operation,
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
// @Success     200       {object} map[string]interface{} "object with host, port, database, username, password and a ready-to-paste dsn"
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
	boundAppRef := serviceDatabaseAppRef(summaryRaw)
	secretOwner := boundAppRef
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

	dsn := postgresDSN(creds.Username, creds.Password, host, port, database)
	host = managedDBEffectiveHost(host)

	dsnSeeded := false
	if boundAppRef != "" && dsn != "" {
		if seeded, seedErr := h.seedDatabaseDSNIfAbsent(c.Request.Context(), projectID, envID, name, boundAppRef, dsn, claims.UserID, "reveal"); seedErr != nil {
			log.Printf("db-dsn-delivery: reveal fallback for %s/%s: %v", name, boundAppRef, seedErr)
		} else {
			dsnSeeded = seeded
		}
	}

	audit(auditOutcomeSuccess, map[string]any{"revealed": true, "namespace": namespace, "dsn_seeded_on_reveal": dsnSeeded})

	resp := gin.H{
		"host":                 host,
		"sslmode":              managedDBEffectiveSSLMode(host),
		"port":                 port,
		"database":             database,
		"username":             creds.Username,
		"password":             creds.Password,
		"dsn":                  dsn,
		"database_url_seeded":  dsnSeeded,
		"database_url_app_ref": boundAppRef,
	}
	extHost, extPort := creds.ExternalHost, creds.ExternalPort
	if extHost != "" {
		resp["external_host"] = extHost
		if extPort == "" {
			extPort = port
		}
		resp["external_port"] = extPort
		resp["external_dsn"] = postgresDSN(creds.Username, creds.Password, extHost, extPort, database)
	}
	c.JSON(http.StatusOK, resp)
}

// postgresDSN assembles a ready-to-paste libpq connection string. Users who are
// handed only host/port/user/password assemble it by hand and get it wrong (a
// live user pasted the bare host into DATABASE_URL and his app crash-looped on
// getaddrinfo), so the endpoint hands the whole string over instead.
// url.UserPassword percent-encodes credentials, which matters because generated
// passwords may carry characters that are structural in a URL.
//
// sslmode=disable was appended on purpose, not left for the client library to
// guess: pg-router (edoburu/pgbouncer, verified live against both the
// transaction and session pools in the databases namespace) carried no
// client_tls_sslmode directive, so it answered any TLS handshake with "server
// does not support SSL, but SSL was required". Client libraries that default
// to requesting TLS (node-postgres, Prisma, Heroku-style templates) crash
// looped on that unless the DSN spelled out sslmode explicitly. A live user
// hit exactly this after the previous bare-host bug was fixed: same DSN, new
// crash, "the server does not support SSL connections".
//
// That is now half-fixed at the infra layer: pg-router gained
// client_tls_sslmode=allow and a publicly trusted cert was issued for
// pgRouterTLSHostname (db.pv.dada-tuda.ru, ClusterIssuer letsencrypt-dns01) --
// but ONLY for a host reachable and name-matched from inside the app's own
// pod, which pgRouterInternalHost (the *.svc.cluster.local Service DNS name)
// is not: no public CA will ever sign that name, so a client that verifies
// the certificate (node-postgres 8+ with ssl:true, the exact default every
// Supabase/Neon/Heroku-style snippet copies) still cannot connect to it.
// Closing that gap needs the app's own pod to resolve db.pv.dada-tuda.ru to
// pg-router's ClusterIP, which is a hostAliases entry rendered by gitops-agent
// (see renderer.AppSpec.PgRouterHostAliasIP), not something this function can
// do by itself.
//
// So this only switches the *bare, in-cluster* endpoint (host ==
// pgRouterInternalHost) over to the TLS-verified name and mode, and only when
// managedDBTLSDSNEnabled() reports the infra side is actually live -- default
// off, so every DSN issued before that flag flips (and every host that is not
// pgRouterInternalHost, e.g. the externally-routed endpoint) keeps rendering
// exactly the old sslmode=disable string it always did. Existing DSNs already
// sitting in an app's env are never rewritten by this function; only a fresh
// call renders the new form.
func postgresDSN(username, password, host, port, database string) string {
	if host == "" || database == "" {
		return ""
	}
	if port == "" {
		port = "5432"
	}
	host = managedDBEffectiveHost(host)
	sslmode := managedDBEffectiveSSLMode(host)
	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(username, password),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + database,
		RawQuery: "sslmode=" + sslmode,
	}
	return u.String()
}

// managedDBEffectiveHost maps a raw connection-secret endpoint to the hostname
// the platform actually wants users to type. Only the bare in-cluster router
// name is rewritten, and only once the TLS chain is confirmed live; every other
// endpoint (external, per-namespace fallback) passes through untouched. It is
// idempotent, so it is safe to apply to a host that has already been mapped.
//
// The database page must call this before showing the host field. Showing the
// pre-mapped host next to a DSN built from the mapped one is what produced the
// split the flag rollout exposed: dsn said db.pv.dada-tuda.ru while the host
// field beside it still said pg-router.databases.svc.cluster.local, and users
// assemble connection strings from whichever of the two they read first.
func managedDBEffectiveHost(host string) string {
	if host == pgRouterInternalHost && managedDBTLSDSNEnabled() {
		return pgRouterTLSHostname
	}
	return host
}

// managedDBEffectiveSSLMode reports the sslmode that belongs with a host
// already passed through managedDBEffectiveHost. Only the TLS-verified name
// carries a certificate, so it is the only host that gets require; everything
// else keeps the pre-TLS disable it always had.
func managedDBEffectiveSSLMode(host string) string {
	if host == pgRouterTLSHostname {
		return "require"
	}
	return "disable"
}

// pgRouterInternalHost is the bare in-cluster Service DNS name for the shared
// managed-Postgres router, written into the connection secret every
// ServiceDatabaseV2 exposes. No public CA will ever sign a *.svc.cluster.local
// name, so this is the host postgresDSN rewrites, never the one it emits.
const pgRouterInternalHost = "pg-router.databases.svc.cluster.local"

// pgRouterTLSHostname is the DNS name the platform's managed-Postgres TLS
// certificate is issued for (ClusterIssuer letsencrypt-dns01, dnsName
// db.pv.dada-tuda.ru). It only resolves inside an app's pod once gitops-agent has
// rendered the matching hostAliases entry -- see
// renderer.AppSpec.PgRouterHostAliasIP.
const pgRouterTLSHostname = "db.pv.dada-tuda.ru"

// managedDBTLSDSNEnabled gates postgresDSN's switch to db.pv.dada-tuda.ru /
// sslmode=require behind an explicit env flag so a DSN handed to a new user
// cannot go live before the paired infra (pg-router TLS listener, the LE
// certificate, and the gitops-agent hostAliases rollout) is confirmed up.
// Default false: unset or any value other than "true" keeps the pre-TLS
// behavior byte-for-byte.
func managedDBTLSDSNEnabled() bool {
	return os.Getenv("MANAGED_DB_TLS_DSN_ENABLED") == "true"
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
