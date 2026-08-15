package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

// maxS3BucketDescriptionLen mirrors the hard limit of the upstream Beget
// provider: beget_s3_bucket.description rejects anything longer than 45
// characters. Exceeding it does not fail the API call — it strands the
// Terraform workspace in ReconcileError, so the connection secret is never
// created and every credential reveal answers 404 "still provisioning".
const maxS3BucketDescriptionLen = 45

// s3BucketDescriptionAllowedExtra lists the punctuation Beget accepts in
// beget_s3_bucket.description beyond Unicode letters, digits and space.
// There is no published character-set spec from Beget for this field; this
// set was derived empirically on 2026-08-03 from a live incident where user
// artemmendeleev's bucket create sat in ReconcileError for 72 minutes
// because the description "Cold storage: Fonbet raw bodies offloaded"
// (43 runes, under the length cap) was silently rejected for its colon.
// Keep this in sync with the strip set in
// gitops-agent/internal/renderer/renderer.go (RenderS3Bucket).
const s3BucketDescriptionAllowedExtra = ".,_-"

// validateS3BucketDescriptionCharset rejects description runes outside
// Unicode letters, digits, space and s3BucketDescriptionAllowedExtra — the
// character set Beget's S3 provider is empirically known to accept (see
// s3BucketDescriptionAllowedExtra). An empty description always passes: the
// field is optional. On failure it returns an error naming each distinct
// rejected character once, in order of first appearance, so the caller does
// not have to guess which one tripped the check.
func validateS3BucketDescriptionCharset(desc string) error {
	var bad []rune
	seen := map[rune]bool{}
	for _, r := range desc {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(s3BucketDescriptionAllowedExtra, r) {
			continue
		}
		if !seen[r] {
			seen[r] = true
			bad = append(bad, r)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	quoted := make([]string, len(bad))
	for i, r := range bad {
		quoted[i] = fmt.Sprintf("%q", string(r))
	}
	return fmt.Errorf(
		"description contains characters the storage provider rejects: %s; allowed are letters, digits, space and . , _ -",
		strings.Join(quoted, ", "),
	)
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
	if n := utf8.RuneCountInString(req.Description); n > maxS3BucketDescriptionLen {
		reject(http.StatusBadRequest, "description_too_long",
			fmt.Sprintf("description must be at most %d characters, got %d", maxS3BucketDescriptionLen, n))
		return
	}
	if err := validateS3BucketDescriptionCharset(req.Description); err != nil {
		reject(http.StatusBadRequest, "description_invalid", err.Error())
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

// DeleteS3Bucket enqueues an operation to tear down a managed S3 bucket.
//
// Destructive on the provider side, unlike the ServiceDatabaseV2 precedent: the
// S3Bucket composition creates its Terraform Workspace without
// deletionPolicy: Orphan (see the platform chart's
// compositions/s3bucket-composition.yaml, where every managed-database resource
// sets Orphan and the bucket workspace deliberately does not), so Crossplane's
// default Delete applies — dropping the CR from git runs terraform destroy on
// beget_s3_bucket, and the bucket and every object in it are gone. The console
// UI must say that outright; a delete that silently orphaned a billed bucket
// would be worse than no delete at all.
//
// @ID          deleteS3Bucket
// @Summary     Delete a managed S3 bucket
// @Description Destructive: permanently removes a managed S3 bucket AND its contents. The agent drops the S3Bucket CR from git, Argo prunes it, and Crossplane runs terraform destroy against Beget (the composition does not orphan the bucket). Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        storage
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Bucket resource name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/s3buckets/{name} [delete]
func (h *Handler) DeleteS3Bucket(c *gin.Context) {
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
			Action:        "DeleteS3Bucket",
			ResourceKind:  "S3Bucket",
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
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up bucket")
		return
	}

	appRef := s3BucketAppRef(summaryRaw)

	payload := models.DeleteS3BucketPayload{Name: name, AppRef: appRef}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteS3Bucket', 'S3Bucket', $4, 'Created', $5)
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
		"message":   "S3Bucket deletion queued",
	})
}

// s3BucketStandaloneOwnerPrefix is the per-project carrier app the renderer puts
// env-level buckets in ("s3-buckets-<project>"); a bucket living there has no
// bound app, which is exactly what an empty AppRef means to the agent.
const s3BucketStandaloneOwnerPrefix = "s3-buckets-"

// s3BucketAppRef resolves which app's chart carries a bucket's CR, from the
// resource snapshot's summary_json. Unlike ServiceDatabaseV2, an S3Bucket CR has
// no spec.appRef to read back, so the owner has to be recovered from whichever
// writer touched the snapshot last:
//   - app_ref — written by the console's own create path (API seed and agent).
//   - app_name — written by the git-watcher when it reverse-syncs an app's
//     resources.values.yaml, which is last-writer-wins on commit time and so can
//     overwrite the create-time summary and take app_ref with it. It holds the
//     owner app's folder name, which for an env-level bucket is the standalone
//     carrier and must map back to an empty AppRef.
//
// Empty (standalone carrier) is the safe default: a bucket that never went
// through either writer is looked for in the carrier chart, where the agent
// finds no CR to remove and fails the operation loudly rather than reporting a
// delete it did not perform.
func s3BucketAppRef(summaryRaw []byte) string {
	if len(summaryRaw) == 0 {
		return ""
	}
	var s struct {
		AppRef  string `json:"app_ref"`
		AppName string `json:"app_name"`
	}
	if json.Unmarshal(summaryRaw, &s) != nil {
		return ""
	}
	if s.AppRef != "" {
		return s.AppRef
	}
	if s.AppName != "" && !strings.HasPrefix(s.AppName, s3BucketStandaloneOwnerPrefix) {
		return s.AppName
	}
	return ""
}

// GetS3BucketCredentials reveals an S3 bucket's live access credentials by
// reading the Crossplane connection secret on demand. Write-gated and audited;
// the credentials are never persisted in the console database.
//
// @ID          getS3BucketCredentials
// @Summary     Reveal an S3 bucket's access credentials
// @Description Reveals the S3 endpoint, access key and secret key for a bucket by reading its Crossplane connection secret. Requires reveal=true and write access; every reveal is audited. Returns 404 while the bucket is still provisioning (no secret yet), 409 with the provider's own error text when provisioning has failed outright, and 503 when in-cluster credential access is not configured.
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
	var firstSeenAt time.Time
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json, first_seen_at FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw, &firstSeenAt); err != nil {
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
			if provisionErr, reason := s3ProvisionError(summaryRaw); provisionErr != "" {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:     projectID,
					EnvironmentID: envID,
					Action:        "RevealS3Credentials",
					ResourceKind:  "S3Bucket",
					ResourceName:  name,
					Outcome:       auditOutcomeFailure,
					Metadata: map[string]any{
						"reason":           "provisioning_failed",
						"status":           http.StatusConflict,
						"provider_reason":  reason,
						"provider_message": provisionErr,
					},
				})
				c.JSON(http.StatusConflict, gin.H{"error": "provisioning_failed", "message": provisionErr})
				return
			}
			since, haveSince := h.s3BucketProvisioningSince(c.Request.Context(), projectID, envID, name, firstSeenAt)
			metadata := map[string]any{"reason": "credentials_not_ready", "status": http.StatusNotFound}
			body := gin.H{"error": "credentials_not_ready", "message": "credentials not available yet — the bucket is still provisioning"}
			if haveSince {
				metadata["waited_seconds"] = int(time.Since(since).Seconds())
				body["provisioning_since"] = since.UTC().Format(time.RFC3339)
			}
			h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
				ProjectID:     projectID,
				EnvironmentID: envID,
				Action:        "RevealS3Credentials",
				ResourceKind:  "S3Bucket",
				ResourceName:  name,
				Outcome:       auditOutcomeFailure,
				Metadata:      metadata,
			})
			c.JSON(http.StatusNotFound, body)
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

// s3BucketProvisioningSince resolves when a bucket's provisioning began, for
// a stuck reveal to report honestly instead of the browser's own "since"
// clock, which resets on every reload or new tab. Two-tier lookup, in order
// of how honestly each answers "when did this start":
//  1. The earliest CreateS3Bucket audit row that is not a failure — the moment
//     the user actually pressed create. A bucket still being provisioned is
//     exactly the case where that row is outcome=pending, since the handler's
//     row is written at enqueue time and only turns into success once the
//     operation finishes [audit.go writeAuditRow]; filtering on success alone
//     would blind this lookup during the very window it exists to describe.
//  2. resourceFirstSeenAt, the snapshot's own first_seen_at (migration 049),
//     for buckets adopted from git that never went through the console's
//     create flow and so have no audit row — but only while it is still
//     recent, see resolveProvisioningSince.
//
// Returns false when neither is available, or when the audit lookup itself
// fails; a query failure here must not turn a working reveal-denial into a
// 500, so it is logged and swallowed.
func (h *Handler) s3BucketProvisioningSince(ctx context.Context, projectID, envID uuid.UUID, name string, resourceFirstSeenAt time.Time) (time.Time, bool) {
	var auditSince *time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT MIN(created_at) FROM audit_events
		 WHERE project_id = $1 AND environment_id = $2 AND action = 'CreateS3Bucket'
		   AND resource_kind = 'S3Bucket' AND resource_name = $3 AND outcome <> $4`,
		projectID, envID, name, auditOutcomeFailure,
	).Scan(&auditSince)
	if err != nil {
		log.Printf("s3buckets: provisioning-since lookup failed for project=%s env=%s bucket=%s: %v", projectID, envID, name, err)
		auditSince = nil
	}
	return resolveProvisioningSince(auditSince, resourceFirstSeenAt, time.Now())
}

// maxSnapshotProvisioningAge bounds how old a snapshot's first_seen_at may be
// before it stops counting as "provisioning started here". Adopted or imported
// buckets whose connection secret will never land in the console's namespace
// (ADR-013) answer not-ready forever, and their snapshot row can be months
// old; anchoring the wait counter to it would report a five-digit minute count
// and trip the slow-provisioning hint with a diagnosis that does not apply.
const maxSnapshotProvisioningAge = 24 * time.Hour

// resolveProvisioningSince applies the two-tier fallback documented on
// s3BucketProvisioningSince, isolated as a pure function so the fallback order
// and the freshness bound are unit-testable without a live database. Callers
// that get false fall back to their own clock, which is the behaviour that
// shipped before this anchor existed.
func resolveProvisioningSince(auditSince *time.Time, resourceFirstSeenAt time.Time, now time.Time) (time.Time, bool) {
	if auditSince != nil {
		return *auditSince, true
	}
	if !resourceFirstSeenAt.IsZero() && now.Sub(resourceFirstSeenAt) < maxSnapshotProvisioningAge {
		return resourceFirstSeenAt, true
	}
	return time.Time{}, false
}

// s3ProvisionError extracts the upstream provider's own failure text from a
// bucket's resource-snapshot summary, where the gitops agent mirrors the
// blocking Crossplane condition (provision_error is the condition message,
// provision_error_reason its reason, e.g. ReconcileError). A bucket whose
// provisioning died — a description the provider rejects is the common case —
// never gets a connection secret, so without this the reveal endpoint answers
// "still provisioning" forever and the failure is invisible to its owner.
// Returns empty strings while the bucket is genuinely still being built.
func s3ProvisionError(summaryRaw []byte) (message, reason string) {
	if len(summaryRaw) == 0 {
		return "", ""
	}
	var s struct {
		ProvisionError       string `json:"provision_error"`
		ProvisionErrorReason string `json:"provision_error_reason"`
	}
	if err := json.Unmarshal(summaryRaw, &s); err != nil {
		return "", ""
	}
	return strings.TrimSpace(s.ProvisionError), strings.TrimSpace(s.ProvisionErrorReason)
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
