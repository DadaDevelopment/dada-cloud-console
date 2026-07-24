package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const volumeExportDownloadTTL = 15 * time.Minute

const volumeExportTimeout = 20 * time.Minute

func volumeExportObjectKey(projectID uuid.UUID, appName string, exportID uuid.UUID) string {
	return fmt.Sprintf("volexports/%s/%s/%s.tar.gz", projectID, appName, exportID)
}

// ExportAppVolume tars up an app's PVC directory straight from a live pod and
// hands back a short-lived download URL, without restarting or mutating the
// pod. POST
// /projects/:projectId/environments/:envId/apps/:appName/volume/export
//
// @ID          exportAppVolume
// @Summary     Export an app's persistent volume as a tar.gz
// @Description Streams "tar czf" of an app's persistent-volume directory out of a live, Running pod straight into object storage, then returns a short-lived (15 min) presigned download URL. Read-only against the pod: no restart, no mutation. 409 when the app has no volume. 502 when the pod exec fails (tar error, no running pod). 503 when volume export is not configured for this environment.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with url, filename and expires_at"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/export [post]
func (h *Handler) ExportAppVolume(c *gin.Context) {
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
	if !h.podTarExporter.Enabled() || !h.dbBackupPresigner.Enabled() {
		respondError(c, http.StatusServiceUnavailable, "volume export is not configured for this environment")
		return
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	var namespace string
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&namespace)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment")
		return
	}
	if namespace == "" {
		respondError(c, http.StatusConflict, "environment has no namespace")
		return
	}

	var summaryRaw []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return
	}

	var cur struct {
		Volume *models.AppVolume `json:"volume"`
	}
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur.Volume == nil || cur.Volume.Path == "" {
		respondError(c, http.StatusConflict, "app has no volume")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), volumeExportTimeout)
	defer cancel()

	podName, containerName, err := h.podTarExporter.FindRunningPod(ctx, namespace, appName)
	if err != nil {
		respondError(c, http.StatusNotFound, "no running pod found for this app: "+err.Error())
		return
	}

	exportID := uuid.New()
	objectKey := volumeExportObjectKey(projectID, appName, exportID)

	pr, pw := io.Pipe()
	execErrCh := make(chan error, 1)
	go func() {
		err := h.podTarExporter.StreamTarball(ctx, namespace, podName, containerName, cur.Volume.Path, pw)
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
		execErrCh <- err
	}()

	putErr := h.dbBackupPresigner.PutObject(ctx, objectKey, pr, -1, "application/gzip")
	pr.Close()
	execErr := <-execErrCh

	if execErr != nil {
		var pe *cloudtask.PodExecError
		msg := "volume export failed: " + execErr.Error()
		if errors.As(execErr, &pe) {
			msg = "volume export failed: " + pe.Error()
		}
		respondError(c, http.StatusBadGateway, msg)
		return
	}
	if putErr != nil {
		respondError(c, http.StatusInternalServerError, "failed to store volume export: "+putErr.Error())
		return
	}

	filename := fmt.Sprintf("%s-volume-%s.tar.gz", appName, time.Now().UTC().Format("20060102-150405"))
	downloadURL, err := h.dbBackupPresigner.PresignGet(ctx, objectKey, filename, volumeExportDownloadTTL)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to prepare download")
		return
	}

	auditMeta, _ := json.Marshal(map[string]any{"export_id": exportID.String(), "object_key": objectKey})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, 'ExportAppVolume', 'App', $3, $4)`,
		claims.UserID, projectID, appName, auditMeta,
	)

	c.JSON(http.StatusOK, gin.H{
		"url":        downloadURL,
		"filename":   filename,
		"expires_at": time.Now().Add(volumeExportDownloadTTL).UTC(),
	})
}
