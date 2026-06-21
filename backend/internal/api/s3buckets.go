package api

import (
	"encoding/json"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
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

	var req createS3BucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.BucketName == "" {
		respondError(c, http.StatusBadRequest, "bucket_name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
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
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "an S3 bucket with that name already exists in this environment")
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
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateS3Bucket', 'S3Bucket', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreateS3Bucket', 'S3Bucket', $4, $5)`,
		claims.UserID, projectID, op.ID, req.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "S3Bucket creation queued",
	})
}
