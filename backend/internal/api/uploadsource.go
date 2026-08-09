package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/sourcedetect"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const uploadSourceMaxBytes = 100 * 1024 * 1024

type uploadSourceResponse struct {
	ArtifactURI string             `json:"artifact_uri"`
	Detected    uploadSourceDetect `json:"detected"`
	Build       build              `json:"build"`
}

type uploadSourceDetect struct {
	Framework string `json:"framework"`
	Port      int    `json:"port"`
}

// uploadAppNameRe is the DNS-1123 label the app name must satisfy. The upload
// endpoint no longer requires the app to exist first, so this is the only
// gate between a user-typed name and a k8s object name; it mirrors the pattern
// the console's upload card enforces client-side.
var uploadAppNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// isWorkerUpload reports whether an archive whose detection produced this port
// is a workload with no HTTP entrypoint — a Telegram bot, a queue consumer, a
// cron job. Detection returns 0 exactly when it found no web framework and no
// EXPOSE, which is the shape of every long-polling bot, the headline case of
// the /hosting-telegram-bot landing.
//
// Such an app still needs a nominal servicePort for the chart, but it must
// never be handed a surrogate domain: nothing is listening, so the link the
// console would show can only ever 502. The verdict is stored on the git_repos
// row (migration 067) because the app itself is materialized later, by
// HandoffDeploy after the first successful build, long after this request is
// gone.
func isWorkerUpload(detectedPort int) bool {
	return detectedPort <= 0
}

// UploadSourceArchive accepts a multipart archive (zip or tar.gz, max 100MB)
// of an app's source, detects its framework and port from manifest files
// (Dockerfile, package.json, requirements.txt, pyproject.toml), stores the
// bytes in object storage, upserts a provider='archive' git_repos row
// pointing at that object, and queues a build against it — the same
// builds/poller/build-agent/Jenkins pipeline that serves git-linked apps.
//
// The app does NOT have to exist yet. It is materialized by HandoffDeploy when
// the first build succeeds, exactly as for a git-connected repo, so its port,
// framework and worker flag come from detection instead of from a guess the
// console had to make before it had ever seen the archive. Creating it upfront
// (the old contract) meant deploying a pause placeholder that k8s reports 1/1
// Running within seconds — a green "Ready" badge and a live-looking domain over
// a build that may still be running, or may already have failed.
//
// @ID          uploadSourceArchive
// @Summary     Deploy from an uploaded source archive
// @Description Uploads a zip/tar.gz of an app's source (max 100MB), detects framework/port from manifest files, stores it in object storage, and queues a build. The app is created by the first successful build if it does not exist yet. Requires write access.
// @Tags        build
// @Accept      multipart/form-data
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       archive   formData file   true "Source archive (.zip or .tar.gz), max 100MB"
// @Success     202       {object} uploadSourceResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/source-archive [post]
func (h *Handler) UploadSourceArchive(c *gin.Context) {
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
	appName := c.Param("appName")

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

	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "UploadSourceArchive",
			ResourceKind:  "Build",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	if !h.sourceUploader.Enabled() {
		reject(http.StatusServiceUnavailable, "uploader_disabled", "source upload is not configured")
		return
	}

	if !uploadAppNameRe.MatchString(appName) {
		reject(http.StatusBadRequest, "invalid_app_name", "app name must be a lowercase DNS label (a-z, 0-9, '-'), 1-63 characters")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, uploadSourceMaxBytes)
	fileHeader, err := c.FormFile("archive")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			reject(http.StatusRequestEntityTooLarge, "archive_too_large", fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
			return
		}
		reject(http.StatusBadRequest, "missing_archive_field", `missing "archive" form field`)
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		reject(http.StatusBadRequest, "archive_unreadable", "failed to read uploaded archive")
		return
	}
	defer src.Close()

	data, err := io.ReadAll(io.LimitReader(src, uploadSourceMaxBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			reject(http.StatusRequestEntityTooLarge, "archive_too_large", fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
			return
		}
		reject(http.StatusBadRequest, "archive_unreadable", "failed to read uploaded archive")
		return
	}
	if len(data) > uploadSourceMaxBytes {
		reject(http.StatusRequestEntityTooLarge, "archive_too_large", fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
		return
	}
	if len(data) == 0 {
		reject(http.StatusBadRequest, "archive_empty", "uploaded archive is empty")
		return
	}

	detected, err := sourcedetect.Detect(data)
	if err != nil {
		reject(http.StatusBadRequest, "archive_unrecognized", fmt.Sprintf("unrecognized archive: %v", err))
		return
	}

	ext, contentType := ".zip", "application/zip"
	if detected.Format == sourcedetect.FormatTarGz {
		ext, contentType = ".tar.gz", "application/gzip"
	}

	uploadID := uuid.New().String()
	key := fmt.Sprintf("source-uploads/%s/%s/%s%s", projectID, appName, uploadID, ext)
	if err := h.sourceUploader.PutObject(c.Request.Context(), key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		reject(http.StatusInternalServerError, "store_failed", "failed to store uploaded archive")
		return
	}
	artifactURI := fmt.Sprintf("s3://%s/%s", h.sourceUploader.Bucket(), key)

	var frameworkOverride *string
	if detected.Framework != "" {
		frameworkOverride = &detected.Framework
	}
	isWorker := isWorkerUpload(detected.Port)
	port := detected.Port
	if port <= 0 {
		port = 8080
	}

	var gitRepoID uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, provider, repo_full_name, clone_url,
		    production_branch, framework_override, port, worker, created_by)
		 VALUES ($1, $2, $3, 'archive', $4, $5, 'upload', $6, $7, $8, $9)
		 ON CONFLICT (project_id, environment_id, app_name) DO UPDATE SET
		   provider           = 'archive',
		   repo_full_name     = EXCLUDED.repo_full_name,
		   clone_url          = EXCLUDED.clone_url,
		   installation_id    = NULL,
		   production_branch  = 'upload',
		   framework_override = EXCLUDED.framework_override,
		   port               = EXCLUDED.port,
		   worker             = EXCLUDED.worker,
		   updated_at         = NOW()
		 RETURNING id`,
		projectID, envID, appName, "upload/"+appName, artifactURI,
		frameworkOverride, port, isWorker, claims.UserID,
	).Scan(&gitRepoID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record uploaded source")
		return
	}

	commitSHA := "manual-" + time.Now().UTC().Format("20060102150405.000000")
	var headSHA *string
	if id := archiveUploadIDFromCloneURL(artifactURI); id != "" {
		headSHA = &id
	}
	var commitMessage *string
	if msg := sanitizeUploadedFilename(fileHeader.Filename); msg != "" {
		commitMessage = &msg
	}

	var b build
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO builds
		   (git_repo_id, environment_id, app_name, commit_sha, branch, head_sha, commit_message, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, 'upload', $5, $6, $7, 'manual', 'queued')
		 RETURNING `+buildSelectCols,
		gitRepoID, envID, appName, commitSHA, headSHA, commitMessage, claims.UserID,
	)
	if err := scanBuild(row, &b); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to queue build")
		return
	}
	b.Source = "archive"

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "UploadSourceArchive",
		ResourceKind:  "Build",
		ResourceName:  appName,
		Metadata: map[string]any{
			"framework": detected.Framework,
			"format":    string(detected.Format),
			"bytes":     len(data),
			"worker":    isWorker,
		},
	})
	h.notifyAuditEvent(claims, projectID, "UploadSourceArchive", appName)

	c.JSON(http.StatusAccepted, uploadSourceResponse{
		ArtifactURI: artifactURI,
		Detected: uploadSourceDetect{
			Framework: detected.Framework,
			Port:      detected.Port,
		},
		Build: b,
	})
}
