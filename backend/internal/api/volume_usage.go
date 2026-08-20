package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// findAppPVCName resolves the live PersistentVolumeClaim backing appName in
// namespace through the app's pod spec: rendered PVCs carry no dada.io/app
// label (observed live — only pods do), so the pod volume list is the one
// authoritative claim-to-app binding. When no pod exists (an app crashlooping
// past its restart budget, or stopped) this falls back to a direct Get on
// "<appName>-pvc", the deterministic name the Helm chart gives every app's
// claim. The fallback never lists or guesses: it only trusts a claim whose
// name it already knows, so it cannot hand back a different app's PVC even
// if the namespace holds several. Returns "" when neither path finds a claim,
// or the cluster is unreachable.
func findAppPVCName(ctx context.Context, namespace, appName string) string {
	clientset := newAppHealthClientset()
	if clientset == nil {
		return ""
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return resolveAppPVCName(listCtx, clientset, namespace, appName)
}

// resolveAppPVCName is findAppPVCName's logic with the clientset taken as a
// parameter, so it is callable with a fake clientset in tests instead of only
// through the in-cluster client findAppPVCName builds. Pure given clientset.
func resolveAppPVCName(ctx context.Context, clientset kubernetes.Interface, namespace, appName string) string {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err == nil {
		for i := range pods.Items {
			for _, v := range pods.Items[i].Spec.Volumes {
				if v.PersistentVolumeClaim != nil {
					return v.PersistentVolumeClaim.ClaimName
				}
			}
		}
	}
	return fallbackAppPVCName(ctx, clientset, namespace, appName)
}

// fallbackAppPVCName is resolveAppPVCName's second path, used when no pod of
// the app mounts a claim. Verified live against fonbet-value-pvc (2026-08-19,
// artemmendeleev-gmail-com-prod): the claim carries no dada.io/app label of
// its own, only argocd.argoproj.io/instance, so a Get by the naming
// convention -- not a label selector -- is the only lookup this can trust
// without risking a different app's claim.
func fallbackAppPVCName(ctx context.Context, clientset kubernetes.Interface, namespace, appName string) string {
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, appName+"-pvc", metav1.GetOptions{})
	if err != nil || pvc == nil {
		return ""
	}
	return pvc.Name
}

// volumeUsageFields is the pure, testable shape of GetAppVolumeUsage's
// response: byte fill is always known (the endpoint 404s before this point
// if it is not), inode fill is best-effort and may be unknown when the
// kubelet_volume_stats_inodes* series has not scraped yet or the query
// failed -- ext4 does not grow its inode table on a PVC resize, so a volume
// can sit at a low byte ratio while its inode table is fully exhausted (live
// case, fonbet-value 2026-08-19: bytes 0.73, inodes 1.000, five days of
// silent ENOSPC crashlooping because nothing surfaced the second dimension).
type volumeUsageFields struct {
	UsedBytes     float64
	CapacityBytes float64
	Ratio         float64
	InodesUsed    float64
	InodesTotal   float64
	InodesRatio   float64
	InodesKnown   bool
	BindingKind   string
}

// buildVolumeUsageFields combines the byte and inode Prometheus reads into
// one response shape. inodesOK is false when either inode query errored or
// returned no sample, in which case the inode fields stay zero/omitted and
// BindingKind defaults to bytes -- inode blindness must never fail the
// endpoint, since the byte fields it already has are still real data.
//
// BindingKind mirrors hotVolumeSamples' rule in app_volume_watcher.go: a
// volume gets tagged inodes only when the inode ratio is at least as high as
// the byte ratio AND itself over appVolumeAlertThreshold, so a volume that
// merely has more free inodes than free bytes does not get mislabeled while
// still comfortably under threshold on both dimensions.
func buildVolumeUsageFields(usedBytes, capacityBytes, inodesUsed, inodesTotal float64, inodesOK bool) volumeUsageFields {
	byteRatio := usedBytes / capacityBytes
	f := volumeUsageFields{
		UsedBytes:     usedBytes,
		CapacityBytes: capacityBytes,
		Ratio:         byteRatio,
		BindingKind:   ratioKindBytes,
	}
	if !inodesOK || inodesTotal <= 0 {
		return f
	}
	inodeRatio := inodesUsed / inodesTotal
	if math.IsNaN(inodeRatio) || math.IsInf(inodeRatio, 0) {
		return f
	}
	f.InodesUsed = inodesUsed
	f.InodesTotal = inodesTotal
	f.InodesRatio = inodeRatio
	f.InodesKnown = true
	if inodeRatio >= byteRatio && inodeRatio >= appVolumeAlertThreshold {
		f.BindingKind = ratioKindInodes
	}
	return f
}

// toJSON renders the fields additively over the endpoint's original
// used_bytes/capacity_bytes/ratio shape: those three keep their exact prior
// name and semantics, and the inode keys only appear when inodesOK held.
func (f volumeUsageFields) toJSON() gin.H {
	out := gin.H{
		"used_bytes":     f.UsedBytes,
		"capacity_bytes": f.CapacityBytes,
		"ratio":          f.Ratio,
		"binding_kind":   f.BindingKind,
	}
	if f.InodesKnown {
		out["inodes_used"] = f.InodesUsed
		out["inodes_total"] = f.InodesTotal
		out["inodes_ratio"] = f.InodesRatio
	}
	return out
}

// GetAppVolumeUsage returns an app's live persistent-volume fill ratio, read
// straight from Prometheus. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/usage
//
// @ID          getAppVolumeUsage
// @Summary     Get an app's persistent volume usage
// @Description Reads the app's PersistentVolumeClaim used/capacity ratio from Prometheus (kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes), plus its inode used/total ratio (kubelet_volume_stats_inodes_used / kubelet_volume_stats_inodes) when that series has scraped. 404 when the app has no volume, is not a Kubernetes app, or the byte metric has not scraped yet. 503 when Prometheus or the cluster client is not configured.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with used_bytes, capacity_bytes, ratio, binding_kind, and (when the inode series has scraped) inodes_used, inodes_total, inodes_ratio"
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

	var inodesUsed, inodesTotal float64
	inodesOK := false
	inodesUsedSamples, iuErr := h.prometheus.QueryInstant(ctx, fmt.Sprintf("kubelet_volume_stats_inodes_used{%s}", matcher), time.Time{}, "")
	inodesTotalSamples, itErr := h.prometheus.QueryInstant(ctx, fmt.Sprintf("kubelet_volume_stats_inodes{%s}", matcher), time.Time{}, "")
	if iuErr == nil && itErr == nil && len(inodesUsedSamples) > 0 && len(inodesTotalSamples) > 0 {
		inodesUsed = inodesUsedSamples[0].Point.V
		inodesTotal = inodesTotalSamples[0].Point.V
		inodesOK = true
	}

	c.JSON(http.StatusOK, buildVolumeUsageFields(usedBytes, capacityBytes, inodesUsed, inodesTotal, inodesOK).toJSON())
}
