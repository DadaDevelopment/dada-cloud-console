package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// findAppPVCName resolves the live PersistentVolumeClaim backing appName in
// namespace through the app's pod spec: rendered PVCs carry no dada.io/app
// label (observed live — only pods do), so the pod volume list is the one
// authoritative claim-to-app binding. Returns "" when no pod of this app
// mounts a PVC, or the cluster is unreachable.
func findAppPVCName(ctx context.Context, namespace, appName string) string {
	clientset := newAppHealthClientset()
	if clientset == nil {
		return ""
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err != nil {
		return ""
	}
	for i := range pods.Items {
		for _, v := range pods.Items[i].Spec.Volumes {
			if v.PersistentVolumeClaim != nil {
				return v.PersistentVolumeClaim.ClaimName
			}
		}
	}
	return ""
}

// GetAppVolumeUsage returns an app's live persistent-volume fill ratio, read
// straight from Prometheus. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/usage
//
// @ID          getAppVolumeUsage
// @Summary     Get an app's persistent volume usage
// @Description Reads the app's PersistentVolumeClaim used/capacity ratio from Prometheus (kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes). 404 when the app has no volume, is not a Kubernetes app, or the metric has not scraped yet. 503 when Prometheus or the cluster client is not configured.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with used_bytes, capacity_bytes and ratio"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/usage [get]
func (h *Handler) GetAppVolumeUsage(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	appName := c.Param("appName")

	if h.prometheus == nil {
		respondError(c, http.StatusServiceUnavailable, "volume usage is not configured for this environment")
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
		respondNotFound(c)
		return
	}

	pvcName := findAppPVCName(c.Request.Context(), namespace, appName)
	if pvcName == "" {
		respondNotFound(c)
		return
	}

	ctx := c.Request.Context()
	nsq := prometheus.EscapeLabelValue(namespace)
	pvcq := prometheus.EscapeLabelValue(pvcName)
	matcher := fmt.Sprintf(`namespace="%s",persistentvolumeclaim="%s"`, nsq, pvcq)

	used, err := h.prometheus.QueryInstant(ctx, fmt.Sprintf("kubelet_volume_stats_used_bytes{%s}", matcher), time.Time{}, "")
	if err != nil || len(used) == 0 {
		respondNotFound(c)
		return
	}
	capacity, err := h.prometheus.QueryInstant(ctx, fmt.Sprintf("kubelet_volume_stats_capacity_bytes{%s}", matcher), time.Time{}, "")
	if err != nil || len(capacity) == 0 || capacity[0].Point.V <= 0 {
		respondNotFound(c)
		return
	}

	usedBytes := used[0].Point.V
	capacityBytes := capacity[0].Point.V
	c.JSON(http.StatusOK, gin.H{
		"used_bytes":     usedBytes,
		"capacity_bytes": capacityBytes,
		"ratio":          usedBytes / capacityBytes,
	})
}
