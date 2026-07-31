package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListS3Buckets returns all S3Bucket resources in a project environment.
//
// @ID          listS3Buckets
// @Summary     List S3 buckets in an environment
// @Description Returns all managed S3 storage buckets (S3Bucket) in the given project environment.
// @Tags        storage
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with a buckets array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/s3buckets [get]
func (h *Handler) ListS3Buckets(c *gin.Context) {
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
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query s3 buckets")
		return
	}
	defer rows.Close()

	var buckets []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan bucket")
			return
		}
		buckets = append(buckets, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading buckets")
		return
	}
	if buckets == nil {
		buckets = []models.ResourceSnapshot{}
	}

	c.JSON(http.StatusOK, gin.H{"buckets": buckets})
}

type createS3BucketRequest struct {
	Name          string `json:"name"`
	BucketName    string `json:"bucket_name"`
	Region        string `json:"region"`
	Description   string `json:"description"`
	Public        bool   `json:"public"`
	FtpSftpEnable bool   `json:"ftp_sftp_enable"`
	// AppRef optionally binds the bucket to an app's chart. Empty = env-level shared storage.
	AppRef string `json:"app_ref"`
}

// CreateS3Bucket enqueues an operation to provision a new S3Bucket CRD.
//
// @ID          createS3Bucket
// @Summary     Order a managed S3 object storage bucket
// @Description Provisions a new Beget S3 bucket via Crossplane (S3Bucket XR). Async: returns 202 with an operation; poll until terminal.
// @Tags        storage
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string               true "Project UUID"
// @Param       envId     path     string               true "Environment UUID"
// @Param       body      body     createS3BucketRequest true "Bucket specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/s3buckets [post]
func (h *Handler) CreateS3Bucket(c *gin.Context) {
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

	bucketAudit := ""
	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateS3Bucket",
			ResourceKind:  "S3Bucket",
			ResourceName:  bucketAudit,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	var req createS3BucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		reject(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	bucketAudit = req.Name

	if req.Name == "" {
		reject(http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	if req.BucketName == "" {
		reject(http.StatusBadRequest, "missing_bucket_name", "bucket_name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		reject(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	if req.Region == "" {
		req.Region = "ru1"
	}

	var existing int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing)
	if err != nil {
		reject(http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		reject(http.StatusConflict, "name_taken", "an S3 bucket with that name already exists in this environment")
		return
	}

	payload := models.CreateS3BucketPayload{
		Name:          req.Name,
		BucketName:    req.BucketName,
		Region:        req.Region,
		Description:   req.Description,
		Public:        req.Public,
		FtpSftpEnable: req.FtpSftpEnable,
		AppRef:        req.AppRef,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		reject(http.StatusInternalServerError, "marshal_failed", "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		reject(http.StatusInternalServerError, "operation_begin_failed", "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateS3Bucket', 'S3Bucket', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "S3Bucket", req.Name, map[string]any{
		"app_ref": req.AppRef,
	}); err != nil {
		reject(http.StatusInternalServerError, "snapshot_seed_failed", "failed to create operation")
		return
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		reject(http.StatusInternalServerError, "operation_commit_failed", "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "CreateS3Bucket",
		ResourceKind:  "S3Bucket",
		ResourceName:  req.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"bucket_name": req.BucketName,
			"region":      req.Region,
			"public":      req.Public,
			"ftp_sftp":    req.FtpSftpEnable,
		},
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "S3Bucket creation queued",
	})
}

// GetS3BucketCredentials reveals an S3 bucket's live access credentials by
// reading the Crossplane connection secret on demand. Write-gated and audited;
// the credentials are never persisted in the console database.
//
// @ID          getS3BucketCredentials
// @Summary     Reveal an S3 bucket's access credentials
// @Description Reveals the S3 endpoint, access key and secret key for a bucket by reading its Crossplane connection secret. Requires reveal=true and write access; every reveal is audited. Returns 404 while the bucket is still provisioning (no secret yet) and 503 when in-cluster credential access is not configured.
// @Tags        storage
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Bucket resource name"
// @Param       reveal    query    bool   true "Must be true to reveal the credentials"
// @Success     200       {object} map[string]interface{} "object with endpoint, access_key, secret_key, bucket_name"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/s3buckets/{name}/credentials [get]
func (h *Handler) GetS3BucketCredentials(c *gin.Context) {
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
	rejectReveal := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "RevealS3Credentials",
			ResourceKind:  "S3Bucket",
			ResourceName:  name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}
	if c.Query("reveal") != "true" {
		rejectReveal(http.StatusBadRequest, "reveal_flag_missing", "reveal=true is required")
		return
	}

	var summaryRaw []byte
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
				ProjectID:     projectID,
				EnvironmentID: envID,
				Action:        "RevealS3Credentials",
				ResourceKind:  "S3Bucket",
				ResourceName:  name,
				Outcome:       auditOutcomeFailure,
				Metadata:      map[string]any{"reason": "bucket_not_found", "status": http.StatusNotFound},
			})
			respondNotFound(c)
			return
		}
		rejectReveal(http.StatusInternalServerError, "existence_check_failed", "failed to check bucket existence")
		return
	}

	ns, secretName := declaredS3ConnectionSecret(summaryRaw)
	var creds cloudtask.S3Credentials
	var err error
	if secretName != "" {
		creds, err = h.s3creds.ResolveRef(c.Request.Context(), ns, secretName)
	} else {
		creds, err = h.s3creds.Resolve(c.Request.Context(), name)
	}
	if err != nil {
		if errors.Is(err, cloudtask.ErrS3CredentialsNotReady) {
			rejectReveal(http.StatusNotFound, "credentials_not_ready", "credentials not available yet — the bucket is still provisioning")
			return
		}
		rejectReveal(http.StatusServiceUnavailable, "credential_access_unconfigured", "S3 credential access is not configured for this environment")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "RevealS3Credentials",
		ResourceKind:  "S3Bucket",
		ResourceName:  name,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"revealed": true},
	})

	c.JSON(http.StatusOK, gin.H{
		"endpoint":    creds.Endpoint,
		"access_key":  creds.AccessKey,
		"secret_key":  creds.SecretKey,
		"bucket_name": creds.BucketName,
		"ftp_host":    creds.FtpHost,
		"sftp_host":   creds.SftpHost,
	})
}

// declaredS3ConnectionSecret extracts a bucket's explicitly-declared
// spec.connectionSecret (namespace, name) from its resource-snapshot summary,
// so a bucket adopted from git that publishes its credentials somewhere other
// than the composition default can still be revealed. Returns empty strings
// when the bucket relies on the default, so the caller falls back to the
// <name>-s3-credentials convention. As a guard against pointing the reveal at
// an unrelated secret, only names ending in "-s3-credentials" are honored.
func declaredS3ConnectionSecret(summaryRaw []byte) (namespace, name string) {
	if len(summaryRaw) == 0 {
		return "", ""
	}
	var s struct {
		Spec struct {
			ConnectionSecret struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"connectionSecret"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(summaryRaw, &s); err != nil {
		return "", ""
	}
	ref := s.Spec.ConnectionSecret
	if !strings.HasSuffix(ref.Name, "-s3-credentials") {
		return "", ""
	}
	return ref.Namespace, ref.Name
}
