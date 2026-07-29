package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// appVolumeWatchInterval is the poll period for the volume-fill watcher: a
// user's PVC otherwise fills silently until ENOSPC crashes the app (P2, real
// incident: fonbet-value 10Gi hit 100% and CrashLooped for about a day before
// anyone noticed).
const appVolumeWatchInterval = 15 * time.Minute

// appVolumeAlertThreshold is the used/capacity ratio that triggers an owner
// email.
const appVolumeAlertThreshold = 0.85

// appVolumeAlertCooldown caps one alert email per app per window, mirroring
// appHealthAlertCooldown's anti-spam rationale but tracked in its own table
// (app_volume_alerts) so a volume-fill alert never suppresses, or gets
// suppressed by, a crash-loop alert for the same app.
const appVolumeAlertCooldown = 24 * time.Hour

// volumeUsageQuery is a single cluster-wide instant query for every PVC's
// used/capacity ratio; the watcher filters the result set down to user
// namespaces itself rather than querying per-namespace.
const volumeUsageQuery = `kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes`

// appVolumeWatcher polls Prometheus for PVC fill ratio across every user
// namespace and emails the project owner once per app per cooldown window
// when a volume crosses appVolumeAlertThreshold. The cooldown lives in
// app_volume_alerts (not in memory) so it holds across pod restarts and
// across replicas.
type appVolumeWatcher struct {
	clientset kubernetes.Interface
	h         *Handler
}

// StartAppVolumeWatcher launches the volume-fill watcher goroutine. No-op
// when mail is unconfigured, Prometheus is unconfigured, or off-cluster (no
// PVCs to list), so local dev and tests never spawn it.
func (h *Handler) StartAppVolumeWatcher(ctx context.Context) {
	if h.auditNotifier == nil || h.prometheus == nil {
		return
	}
	clientset := newAppHealthClientset()
	if clientset == nil {
		log.Printf("app-volume: no in-cluster client, watcher disabled")
		return
	}
	w := &appVolumeWatcher{clientset: clientset, h: h}
	log.Printf("app-volume: watcher started interval=%s threshold=%.2f", appVolumeWatchInterval, appVolumeAlertThreshold)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyAppVolumeWatch, "app-volume", w.tick)
		t := time.NewTicker(appVolumeWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyAppVolumeWatch, "app-volume", w.tick)
			}
		}
	}()
}

// volumeUsageSample is one PVC's fill ratio read off the Prometheus vector
// result, with the namespace/persistentvolumeclaim labels it was keyed by.
type volumeUsageSample struct {
	Namespace string
	PVCName   string
	Ratio     float64
}

// parseVolumeUsageSamples extracts (namespace, persistentvolumeclaim, ratio)
// out of the raw Prometheus samples, dropping anything with a missing label
// or a non-finite ratio (a capacity of 0 divides to +Inf/NaN, which is not a
// real fill level). Pure, unit-tested without a live Prometheus.
func parseVolumeUsageSamples(samples []prometheus.Sample) []volumeUsageSample {
	out := make([]volumeUsageSample, 0, len(samples))
	for _, s := range samples {
		ns := s.Metric["namespace"]
		pvc := s.Metric["persistentvolumeclaim"]
		if ns == "" || pvc == "" {
			continue
		}
		if math.IsNaN(s.Point.V) || math.IsInf(s.Point.V, 0) {
			continue
		}
		out = append(out, volumeUsageSample{Namespace: ns, PVCName: pvc, Ratio: s.Point.V})
	}
	return out
}

// overThreshold filters samples down to the ones at or above ratio, so the
// caller only pays for a namespace's PVC listing and an owner lookup when
// there is actually something to alert on.
func overThreshold(samples []volumeUsageSample, ratio float64) []volumeUsageSample {
	out := make([]volumeUsageSample, 0)
	for _, s := range samples {
		if s.Ratio >= ratio {
			out = append(out, s)
		}
	}
	return out
}

// pvcAppLabels maps PVC claim names in namespace to the app that mounts them,
// resolved through pod specs: rendered PVCs carry no dada.io/app label
// (observed live — only the pod template does), so the pod's volume list is
// the one authoritative claim-to-app binding available at runtime.
func (w *appVolumeWatcher) pvcAppLabels(ctx context.Context, namespace string) map[string]string {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pods, err := w.clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{LabelSelector: "dada.io/app"})
	if err != nil {
		log.Printf("app-volume: list pods in %s failed: %v", namespace, err)
		return nil
	}
	out := map[string]string{}
	for i := range pods.Items {
		appName := pods.Items[i].Labels["dada.io/app"]
		if appName == "" {
			continue
		}
		for _, v := range pods.Items[i].Spec.Volumes {
			if v.PersistentVolumeClaim != nil {
				out[v.PersistentVolumeClaim.ClaimName] = appName
			}
		}
	}
	return out
}

// tick runs one cluster-wide fill-ratio query, restricts the result set to
// user namespaces, and fires at most one alert per app per cooldown window.
// Every failure (Prometheus query, namespace list, PVC list per namespace) is
// logged and swallowed: one bad namespace must never block the scan of the
// rest, and the watcher must never crash the backend pod it runs inside.
func (w *appVolumeWatcher) tick(ctx context.Context) {
	nsProjects, err := w.h.namespaceProjects(ctx)
	if err != nil {
		log.Printf("app-volume: load namespaces failed: %v", err)
		return
	}

	samples, err := w.h.prometheus.QueryInstant(ctx, volumeUsageQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-volume: query failed: %v", err)
		return
	}

	parsed := parseVolumeUsageSamples(samples)
	byNamespace := map[string][]volumeUsageSample{}
	for _, s := range overThreshold(parsed, appVolumeAlertThreshold) {
		if _, ok := nsProjects[s.Namespace]; !ok {
			continue
		}
		byNamespace[s.Namespace] = append(byNamespace[s.Namespace], s)
	}
	log.Printf("app-volume: tick samples=%d parsed=%d hot_user_ns=%d", len(samples), len(parsed), len(byNamespace))

	for ns, hot := range byNamespace {
		env := nsProjects[ns]
		pvcApp := w.pvcAppLabels(ctx, ns)
		if pvcApp == nil {
			continue
		}
		for _, s := range hot {
			appName := pvcApp[s.PVCName]
			if appName == "" {
				continue
			}
			w.maybeNotify(ctx, env.ProjectID, ns, appName, s.Ratio)
		}
	}
}

// claimAppVolumeAlertSlot atomically claims the right to send one alert for
// (namespace, app) by upserting app_volume_alerts, succeeding only when no
// send is recorded within cooldown. Race-free across replicas, identical
// shape to claimAppHealthAlertSlot but against its own table so a crash alert
// and a volume alert for the same app never suppress one another.
func claimAppVolumeAlertSlot(ctx context.Context, pool *pgxpool.Pool, namespace, appName string, cooldown time.Duration) bool {
	ct, err := pool.Exec(ctx,
		`INSERT INTO app_volume_alerts (namespace, app_name, last_sent_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (namespace, app_name) DO UPDATE SET last_sent_at = now()
		 WHERE app_volume_alerts.last_sent_at <= now() - make_interval(secs => $3)`,
		namespace, appName, cooldown.Seconds())
	if err != nil {
		log.Printf("app-volume: cooldown claim for %s/%s failed: %v", namespace, appName, err)
		return false
	}
	return ct.RowsAffected() > 0
}

// maybeNotify sends the owner alert for one over-threshold volume, gated by
// the per-app 24h cooldown persisted in app_volume_alerts. The cooldown is
// claimed before the send attempt so a slow/failing SMTP relay cannot cause a
// retry storm on the next tick.
func (w *appVolumeWatcher) maybeNotify(ctx context.Context, projectID uuid.UUID, namespace, appName string, ratio float64) {
	if !claimAppVolumeAlertSlot(ctx, w.h.pool, namespace, appName, appVolumeAlertCooldown) {
		return
	}

	to := w.h.projectOwnerEmail(ctx, projectID)
	if to == "" {
		log.Printf("app-volume: no owner email for project %s, dropping alert for app=%s ratio=%.3f", projectID, appName, ratio)
		return
	}

	size := w.h.declaredVolumeSize(ctx, projectID, appName)
	consoleLink := fmt.Sprintf("%s/projects/%s/apps/%s/settings?tab=storage", w.h.cfg.PublicBaseURL, projectID, appName)
	subject, body := notify.ComposeVolumeAlert(appName, ratio, size, consoleLink)
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("app-volume: send to %s failed for app=%s: %v", to, appName, err)
		return
	}
	log.Printf("app-volume: alerted %s for app=%s ratio=%.3f", to, appName, ratio)
}

// declaredVolumeSize best-effort reads the app's declared volume size (e.g.
// "10Gi") out of its resource_snapshots summary for the email body. Returns
// "" on any failure so the alert is still sent without the figure.
func (h *Handler) declaredVolumeSize(ctx context.Context, projectID uuid.UUID, appName string) string {
	var summaryRaw []byte
	err := h.pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND kind = 'App' AND name = $2
		 LIMIT 1`,
		projectID, appName,
	).Scan(&summaryRaw)
	if err != nil {
		return ""
	}
	var cur struct {
		Volume *struct {
			Size string `json:"size"`
		} `json:"volume"`
	}
	if err := json.Unmarshal(summaryRaw, &cur); err != nil || cur.Volume == nil {
		return ""
	}
	return cur.Volume.Size
}
