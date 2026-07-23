package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// UploadSourceArchive accepts a multipart archive (zip or tar.gz, max 100MB)
// of an app's source, detects its framework and port from manifest files
// (Dockerfile, package.json, requirements.txt, pyproject.toml), stores the
// bytes in object storage, upserts a provider='archive' git_repos row
// pointing at that object, and queues a build against it — the same
// builds/poller/build-agent/Jenkins pipeline that serves git-linked apps.
// The app must already exist (create it via the ordinary CreateApp flow
// first); this endpoint only replaces the "clone from git" step.
//
// @ID          uploadSourceArchive
// @Summary     Deploy from an uploaded source archive
// @Description Uploads a zip/tar.gz of an app's source (max 100MB), detects framework/port from manifest files, stores it in object storage, and queues a build. The app must already exist. Requires write access.
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

	if !h.sourceUploader.Enabled() {
		respondError(c, http.StatusServiceUnavailable, "source upload is not configured")
		return
	}

	var appCount int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&appCount); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if appCount == 0 {
		respondNotFound(c)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, uploadSourceMaxBytes)
	fileHeader, err := c.FormFile("archive")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
			return
		}
		respondError(c, http.StatusBadRequest, `missing "archive" form field`)
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		respondError(c, http.StatusBadRequest, "failed to read uploaded archive")
		return
	}
	defer src.Close()

	data, err := io.ReadAll(io.LimitReader(src, uploadSourceMaxBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
			return
		}
		respondError(c, http.StatusBadRequest, "failed to read uploaded archive")
		return
	}
	if len(data) > uploadSourceMaxBytes {
		respondError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("archive exceeds %d bytes", uploadSourceMaxBytes))
		return
	}
	if len(data) == 0 {
		respondError(c, http.StatusBadRequest, "uploaded archive is empty")
		return
	}

	detected, err := sourcedetect.Detect(data)
	if err != nil {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("unrecognized archive: %v", err))
		return
	}

	ext, contentType := ".zip", "application/zip"
	if detected.Format == sourcedetect.FormatTarGz {
		ext, contentType = ".tar.gz", "application/gzip"
	}

	uploadID := uuid.New().String()
	key := fmt.Sprintf("source-uploads/%s/%s/%s%s", projectID, appName, uploadID, ext)
	if err := h.sourceUploader.PutObject(c.Request.Context(), key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to store uploaded archive")
		return
	}
	artifactURI := fmt.Sprintf("s3://%s/%s", h.sourceUploader.Bucket(), key)

	var frameworkOverride *string
	if detected.Framework != "" {
		frameworkOverride = &detected.Framework
	}
	port := detected.Port
	if port <= 0 {
		port = 8080
	}

	var gitRepoID uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, provider, repo_full_name, clone_url,
		    production_branch, framework_override, port, created_by)
		 VALUES ($1, $2, $3, 'archive', $4, $5, 'upload', $6, $7, $8)
		 ON CONFLICT (project_id, environment_id, app_name) DO UPDATE SET
		   provider           = 'archive',
		   repo_full_name     = EXCLUDED.repo_full_name,
		   clone_url          = EXCLUDED.clone_url,
		   installation_id    = NULL,
		   production_branch  = 'upload',
		   framework_override = EXCLUDED.framework_override,
		   port               = EXCLUDED.port,
		   updated_at         = NOW()
		 RETURNING id`,
		projectID, envID, appName, "upload/"+appName, artifactURI,
		frameworkOverride, port, claims.UserID,
	).Scan(&gitRepoID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record uploaded source")
		return
	}

	commitSHA := "manual-" + time.Now().UTC().Format("20060102150405.000000")
	var b build
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO builds
		   (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, 'upload', $5, 'manual', 'queued')
		 RETURNING `+buildSelectCols,
		gitRepoID, envID, appName, commitSHA, claims.UserID,
	)
	if err := scanBuild(row, &b); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to queue build")
		return
	}

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'UploadSourceArchive', 'Build', $3)`,
		claims.UserID, projectID, appName,
	)
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
