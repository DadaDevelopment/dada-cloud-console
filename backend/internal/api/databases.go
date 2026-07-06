package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
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

	// Check write permission
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "databases"); qErr != nil {
			if qe, ok := qErr.(*quotaExceededError); ok {
				respondQuotaExceeded(c, qe.Resource, qe.Limit)
				return
			}
		}
	}

	var req createServiceDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Validate fields
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.Database == "" {
		respondError(c, http.StatusBadRequest, "database is required")
		return
	}
	// app_ref is optional: empty = standalone, environment-level database that
	// owns its own chart. When set, the database is bound to that app's chart.
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePgName(req.Database); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
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
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "a database with that name already exists in this environment")
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
			respondError(c, http.StatusInternalServerError, "failed to generate database credential")
			return
		}
		const dbUser = "dada"
		for _, kv := range [][2]string{
			{"POSTGRES_PASSWORD", password},
			{"POSTGRES_DB", req.Database},
			{"POSTGRES_USER", dbUser},
		} {
			if err := h.seedEnvVar(c.Request.Context(), envID, req.Name, kv[0], kv[1], claims.UserID); err != nil {
				respondError(c, http.StatusInternalServerError, "failed to seed database credentials")
				return
			}
		}
		if req.AppRef != "" {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, password, req.Name, req.Database)
			if err := h.seedEnvVar(c.Request.Context(), envID, req.AppRef, "DATABASE_URL", dsn, claims.UserID); err != nil {
				respondError(c, http.StatusInternalServerError, "failed to inject database connection string")
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
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	// Insert Operation
	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateServiceDatabase', 'ServiceDatabaseV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	// Insert AuditEvent (best-effort — don't fail the request if this fails)
	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreateServiceDatabase', 'ServiceDatabaseV2', $4, $5)`,
		claims.UserID, projectID, op.ID, req.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "ServiceDatabase creation queued",
	})
}
