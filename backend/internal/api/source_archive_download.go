package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const sourceArchiveDownloadTTL = 15 * time.Minute

// parseSourceArchiveURL splits an "s3://<bucket>/<key>" clone_url value (the
// format UploadSourceArchive writes into git_repos.clone_url for
// provider='archive' rows) into its bucket and key.
func parseSourceArchiveURL(u string) (bucket, key string, err error) {
	const prefix = "s3://"
	if !strings.HasPrefix(u, prefix) {
		return "", "", fmt.Errorf("not an s3 url: %q", u)
	}
	rest := strings.TrimPrefix(u, prefix)
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return "", "", fmt.Errorf("malformed s3 url: %q", u)
	}
	return bucket, key, nil
}

// sourceArchiveExt returns the archive extension (".tar.gz" or ".zip") that
// UploadSourceArchive appended to the stored object key.
func sourceArchiveExt(key string) string {
	if strings.HasSuffix(key, ".tar.gz") {
		return ".tar.gz"
	}
	if idx := strings.LastIndex(key, "."); idx >= 0 {
		return key[idx:]
	}
	return ""
}

// DownloadSourceArchive hands back a short-lived presigned URL to the source
// archive a user uploaded for this app via upload-deploy (UploadSourceArchive),
// so a user who lost their local checkout can pull the exact bytes back down.
// GET /projects/:projectId/environments/:envId/apps/:appName/source-archive/download
//
// @ID          downloadSourceArchive
// @Summary     Download the uploaded source archive for an app
// @Description Returns a short-lived (15 min) presigned download URL for the source archive most recently uploaded for this app via upload-deploy. 404 when the app has no uploaded archive (it was created from a connected git repo instead, or nothing has been uploaded yet). 503 when source upload/download is not configured for this environment. Requires write access.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with url, filename and expires_at"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/source-archive/download [get]
func (h *Handler) DownloadSourceArchive(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	appName := c.Param("appName")

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DownloadSourceArchive",
			ResourceKind:  "Build",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}
	if !h.sourceUploader.Enabled() {
		reject(http.StatusServiceUnavailable, "uploader_disabled", "source archive download is not configured")
		return
	}

	var provider, cloneURL string
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT provider, clone_url FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&provider, &cloneURL)
	if err == pgx.ErrNoRows || (err == nil && provider != "archive") {
		reject(http.StatusNotFound, "no_uploaded_source", "this app has no uploaded source archive")
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "source_lookup_failed", "failed to look up uploaded source")
		return
	}

	bucket, key, err := parseSourceArchiveURL(cloneURL)
	if err != nil || bucket != h.sourceUploader.Bucket() {
		reject(http.StatusInternalServerError, "invalid_archive_ref", "stored source archive reference is invalid")
		return
	}

	filename := appName + "-source" + sourceArchiveExt(key)
	downloadURL, err := h.sourceUploader.PresignGet(c.Request.Context(), key, filename, sourceArchiveDownloadTTL)
	if err != nil {
		reject(http.StatusInternalServerError, "presign_failed", "failed to prepare download")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "DownloadSourceArchive",
		ResourceKind:  "Build",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"app_name": appName},
	})

	c.JSON(http.StatusOK, gin.H{
		"url":        downloadURL,
		"filename":   filename,
		"expires_at": time.Now().Add(sourceArchiveDownloadTTL).UTC(),
	})
}
