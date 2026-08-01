package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const dbBackupSelect = `SELECT id, project_id, environment_id, resource_name, database_name,
	dump_path, size_bytes, status, kind, action_set, error_message,
	created_by, created_at, updated_at, expires_at FROM db_backups`

// scanDBBackup scans one db_backups row (columns per dbBackupSelect).
func scanDBBackup(row pgx.Row, b *models.DBBackup) error {
	return row.Scan(&b.ID, &b.ProjectID, &b.EnvironmentID, &b.ResourceName, &b.DatabaseName,
		&b.DumpPath, &b.SizeBytes, &b.Status, &b.Kind, &b.ActionSet,
		&b.ErrorMessage, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt, &b.ExpiresAt)
}

// serviceDatabaseName pulls the logical database (spec.database) from a
// ServiceDatabaseV2 snapshot's summary_json, falling back to the top-level
// "database" field. Empty when unparseable.
func serviceDatabaseName(summaryRaw []byte) string {
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

// lookupManagedDatabase resolves the logical database name for a
// ServiceDatabaseV2 resource, or reports 404 if it does not exist.
func (h *Handler) lookupManagedDatabase(c *gin.Context, projectID, envID uuid.UUID, name string) (string, bool) {
	var summaryRaw []byte
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return "", false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to look up database")
		return "", false
	}
	database := serviceDatabaseName(summaryRaw)
	if database == "" {
		database = name
	}
	return database, true
}

// ListDBBackups returns the per-database backup catalog for a managed database.
//
// @ID          listDatabaseBackups
// @Summary     List a database's backups
// @Description Returns the per-database logical backup catalog (newest first) for a managed PostgreSQL database.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     200       {object} map[string]interface{} "object with a backups array"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/backups [get]
func (h *Handler) ListDBBackups(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")
	if _, ok := h.lookupManagedDatabase(c, projectID, envID, name); !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		dbBackupSelect+` WHERE project_id = $1 AND environment_id = $2 AND resource_name = $3
		 AND status <> 'Deleted' ORDER BY created_at DESC`,
		projectID, envID, name,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query backups")
		return
	}
	defer rows.Close()

	backups := []models.DBBackup{}
	for rows.Next() {
		var b models.DBBackup
		if err := scanDBBackup(rows, &b); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan backup")
			return
		}
		backups = append(backups, b)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading backups")
		return
	}
	c.JSON(http.StatusOK, gin.H{"backups": backups})
}

// CreateDBBackup starts an on-demand per-database logical backup.
//
// @ID          createDatabaseBackup
// @Summary     Back up a managed database now
// @Description Starts an on-demand per-database logical backup (pg_dump) via a Kanister ActionSet. Asynchronous: returns 202 with the Pending/Running backup; poll the backups list until Ready.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Success     202       {object} map[string]interface{} "object with the accepted backup"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/backups [post]
func (h *Handler) CreateDBBackup(c *gin.Context) {
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
	reject := func(status int, reason string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateDatabaseBackup",
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		reject(c.Writer.Status(), "not_a_writer")
		return
	}
	database, ok := h.lookupManagedDatabase(c, projectID, envID, name)
	if !ok {
		reject(c.Writer.Status(), "database_not_found")
		return
	}
	if !h.kanister.Enabled() {
		rejectErr(http.StatusServiceUnavailable, "backups_not_configured", "database backups are not configured for this environment")
		return
	}

	backup, err := h.startDBBackup(c.Request.Context(), projectID, envID, name, database, models.DBBackupKindManual, &claims.UserID)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "backup_start_failed", "failed to start backup")
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "CreateDatabaseBackup",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  name,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"backup_id": backup.ID, "database": database},
	})
	c.JSON(http.StatusAccepted, gin.H{"backup": backup})
}

// startDBBackup inserts a Pending backup row, creates the backup ActionSet, and
// marks the row Running. The row id doubles as the S3 dump object key so the
// artifact is unique and traceable.
func (h *Handler) startDBBackup(ctx context.Context, projectID, envID uuid.UUID, resourceName, database, kind string, createdBy *uuid.UUID) (models.DBBackup, error) {
	id := uuid.New()
	dumpPath := fmt.Sprintf("dumps/%s/%s/%s.dump", projectID, database, id)
	expiresAt := time.Now().AddDate(0, 0, h.cfg.DBBackupRetentionDays)

	var b models.DBBackup
	row := h.pool.QueryRow(ctx,
		`INSERT INTO db_backups (id, project_id, environment_id, resource_name, database_name,
		     dump_path, status, kind, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 'Pending', $7, $8, $9)
		 RETURNING `+dbBackupCols,
		id, projectID, envID, resourceName, database, dumpPath, kind, createdBy, expiresAt,
	)
	if err := scanDBBackup(row, &b); err != nil {
		return models.DBBackup{}, err
	}

	asName, err := h.kanister.CreateBackup(ctx, cloudtask.KanisterActionSpec{
		Namespace:   h.cfg.DBBackupNamespace,
		StatefulSet: h.cfg.DBBackupStatefulSet,
		Profile:     h.cfg.DBBackupProfile,
		Blueprint:   h.cfg.DBBackupBlueprint,
		Database:    database,
		DumpPath:    dumpPath,
		Labels:      map[string]string{"dada.io/db-backup-id": id.String()},
	})
	if err != nil {
		_, _ = h.pool.Exec(ctx,
			`UPDATE db_backups SET status = 'Failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
			id, err.Error())
		return models.DBBackup{}, err
	}
	if err := scanDBBackup(h.pool.QueryRow(ctx,
		`UPDATE db_backups SET status = 'Running', action_set = $2, updated_at = NOW() WHERE id = $1
		 RETURNING `+dbBackupCols, id, asName), &b); err != nil {
		return models.DBBackup{}, err
	}
	return b, nil
}

// dbBackupCols is the RETURNING/SELECT column list matching scanDBBackup.
const dbBackupCols = `id, project_id, environment_id, resource_name, database_name,
	dump_path, size_bytes, status, kind, action_set, error_message, created_by,
	created_at, updated_at, expires_at`

type restoreDatabaseRequest struct {
	BackupID string `json:"backup_id"`
}

// RestoreServiceDatabase restores a managed database from a cataloged backup.
//
// @ID          restoreDatabase
// @Summary     Restore a managed database from a backup
// @Description Destructive: overwrites the managed PostgreSQL database with the contents of a cataloged backup, restoring only that one database (pg_restore) via a Kanister ActionSet. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        database
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       name      path     string                 true "Database resource name"
// @Param       body      body     restoreDatabaseRequest true "Backup to restore from"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/restore [post]
func (h *Handler) RestoreServiceDatabase(c *gin.Context) {
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
	reject := func(status int, reason string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "RestoreServiceDatabase",
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		reject(c.Writer.Status(), "not_a_writer")
		return
	}
	database, ok := h.lookupManagedDatabase(c, projectID, envID, name)
	if !ok {
		reject(c.Writer.Status(), "database_not_found")
		return
	}
	if !h.kanister.Enabled() {
		rejectErr(http.StatusServiceUnavailable, "restore_not_configured", "database restore is not configured for this environment")
		return
	}

	var req restoreDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	backupID, err := uuid.Parse(req.BackupID)
	if err != nil {
		rejectErr(http.StatusBadRequest, "missing_backup_id", "backup_id is required")
		return
	}

	var backup models.DBBackup
	err = scanDBBackup(h.pool.QueryRow(c.Request.Context(),
		dbBackupSelect+` WHERE id = $1 AND project_id = $2 AND environment_id = $3 AND resource_name = $4`,
		backupID, projectID, envID, name), &backup)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "backup_not_found")
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "backup_lookup_failed", "failed to look up backup")
		return
	}
	if backup.Status != models.DBBackupStatusReady {
		rejectErr(http.StatusBadRequest, "backup_not_ready", "backup is not ready to restore")
		return
	}

	payload := models.RestoreServiceDatabasePayload{Name: name, Database: database, BackupID: backup.ID.String()}
	payloadBytes, _ := json.Marshal(payload)

	// Insert as Reconciling (not Created) so neither the gitops-agent nor the
	// portainer-agent claims it — this operation is driven entirely by the
	// backend's backup reconciler (it creates + polls the Kanister ActionSet).
	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'RestoreServiceDatabase', 'ServiceDatabaseV2', $4, 'Reconciling', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	_, err = h.kanister.CreateRestore(c.Request.Context(), cloudtask.KanisterActionSpec{
		Namespace:   h.cfg.DBBackupNamespace,
		StatefulSet: h.cfg.DBBackupStatefulSet,
		Profile:     h.cfg.DBBackupProfile,
		Blueprint:   h.cfg.DBBackupBlueprint,
		Database:    database,
		DumpPath:    backup.DumpPath,
		Labels:      map[string]string{"dada.io/operation-id": op.ID.String()},
	})
	if err != nil {
		_, _ = h.pool.Exec(c.Request.Context(),
			`UPDATE operations SET status = 'Failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
			op.ID, err.Error())
		rejectErr(http.StatusInternalServerError, "actionset_create_failed", "failed to start restore")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "RestoreServiceDatabase",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  name,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"database": database, "backup_id": backup.ID.String()},
	})
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "ServiceDatabase restore queued"})
}

// dbBackupDownloadTTL bounds how long a presigned backup-download URL stays
// valid. Short: the browser follows it immediately after the JSON response.
const dbBackupDownloadTTL = 5 * time.Minute

// DownloadDBBackup issues a short-lived presigned URL to download a Ready
// backup's dump straight from object storage.
//
// @ID          downloadDatabaseBackup
// @Summary     Download a database backup
// @Description Returns a short-lived presigned URL to download a Ready per-database logical backup (the pg_dump artifact) directly from object storage; the dump bytes never transit the API. Requires write access and every download is audited. 409 while the backup is not Ready, 503 when dump-bucket access is not configured.
// @Tags        database
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Database resource name"
// @Param       backupId  path     string true "Backup UUID"
// @Success     200       {object} map[string]interface{} "object with a presigned download url and its expiry"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/databases/{name}/backups/{backupId}/download [get]
func (h *Handler) DownloadDBBackup(c *gin.Context) {
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
	reject := func(status int, reason string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DownloadServiceDatabaseBackup",
			ResourceKind:  "ServiceDatabaseV2",
			ResourceName:  name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		reject(c.Writer.Status(), "not_a_writer")
		return
	}
	backupID, err := uuid.Parse(c.Param("backupId"))
	if err != nil {
		rejectErr(http.StatusBadRequest, "invalid_backup_id", "invalid backup id")
		return
	}
	if !h.dbBackupPresigner.Enabled() {
		rejectErr(http.StatusServiceUnavailable, "download_not_configured", "backup download is not configured for this environment")
		return
	}

	var backup models.DBBackup
	err = scanDBBackup(h.pool.QueryRow(c.Request.Context(),
		dbBackupSelect+` WHERE id = $1 AND project_id = $2 AND environment_id = $3 AND resource_name = $4`,
		backupID, projectID, envID, name), &backup)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "backup_not_found")
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "backup_lookup_failed", "failed to look up backup")
		return
	}
	if backup.Status != models.DBBackupStatusReady {
		rejectErr(http.StatusConflict, "backup_not_ready", "backup is not ready to download")
		return
	}

	objectKey := backup.DumpPath
	if pfx := strings.Trim(h.cfg.DBBackupS3Prefix, "/"); pfx != "" {
		objectKey = pfx + "/" + strings.TrimPrefix(backup.DumpPath, "/")
	}
	filename := fmt.Sprintf("%s-%s.dump", backup.DatabaseName, backupID.String()[:8])
	downloadURL, err := h.dbBackupPresigner.PresignGet(c.Request.Context(), objectKey, filename, dbBackupDownloadTTL)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "presign_failed", "failed to prepare download")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "DownloadServiceDatabaseBackup",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  name,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"backup_id": backup.ID.String()},
	})

	c.JSON(http.StatusOK, gin.H{
		"url":        downloadURL,
		"filename":   filename,
		"expires_at": time.Now().Add(dbBackupDownloadTTL).UTC(),
	})
}
