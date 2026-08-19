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

// appVolumeAlertFreshWindow is how recently last_seen_at must have been
// touched for the console to still show a volume alert as current, mirroring
// appHealthAlertFreshWindow's rationale. Tied to appVolumeWatchInterval (15m)
// with a 3x margin (tighter than health's 5x because a 15m tick already
// leaves less room before the window would otherwise lapse between ticks):
// a volume still over threshold gets re-touched every 15m and never falls
// out of a 45m window, while a volume resized 20 minutes ago clears the
// banner instead of showing red for the next 24h. Move this with
// appVolumeWatchInterval if that interval ever changes.
const appVolumeAlertFreshWindow = 3 * appVolumeWatchInterval

// volumeUsageQuery is a single cluster-wide query for every PVC's
// used/capacity ratio; the watcher filters the result set down to user
// namespaces itself rather than querying per-namespace.
//
// The ratio is built from last_over_time rather than a bare instant division
// on purpose. kubelet publishes volume stats per node, so a pod that keeps
// getting rescheduled - exactly what a full volume causes, via ENOSPC
// CrashLoopBackOff - staleness-marks its series on the old node before the
// new node's first scrape lands. A bare instant query then finds no sample at
// the tick instant and the PVC silently drops out of the result set, so the
// watcher misses precisely the volumes it exists to catch. Reproduced on the
// 2026-07-29 fonbet-value incident: the fill ratio held at 0.9987 for the
// whole window while five consecutive ticks saw the PVC vanish and logged
// hot_user_ns=0, with the series bouncing across three nodes.
//
// The lookback exceeds the largest churn gap observed there (~13m) while
// staying far below appVolumeAlertCooldown, so a stale sample can never
// resurrect an alert for a volume that has since been resized. Do not attempt
// to fix this class of miss by shortening appVolumeWatchInterval: more
// frequent instant queries raise the chance of landing in a gap, they do not
// lower it.
const volumeUsageLookback = "20m"

var volumeUsageQuery = fmt.Sprintf(
	`last_over_time(kubelet_volume_stats_used_bytes[%[1]s]) / last_over_time(kubelet_volume_stats_capacity_bytes[%[1]s])`,
	volumeUsageLookback,
)

// volumeInodeQuery is volumeUsageQuery's inode counterpart: same
// last_over_time treatment, same lookback, same rationale (see
// volumeUsageQuery's doc comment) -- a rescheduled pod staleness-marks its
// inode series on the old node exactly as it does its byte series, so a bare
// instant query would drop the PVC from the result set the same way.
//
// This exists because a byte-fill ratio and an inode-fill ratio are
// independent facts about the same filesystem: ext4 does not grow its inode
// table when a PVC is resized, so a volume can sit at a low byte ratio while
// its inode table is completely exhausted. Live case, 2026-08-19:
// fonbet-value's PVC read kubelet_volume_stats_used_bytes/capacity_bytes =
// 0.73 (comfortably under appVolumeAlertThreshold) while
// kubelet_volume_stats_inodes_used/inodes = 1.000 (inodes_free = 0), and the
// app crashlooped on ENOSPC for five days because volumeUsageQuery alone
// never saw the byte ratio cross the threshold.
var volumeInodeQuery = fmt.Sprintf(
	`last_over_time(kubelet_volume_stats_inodes_used[%[1]s]) / last_over_time(kubelet_volume_stats_inodes[%[1]s])`,
	volumeUsageLookback,
)

// ratioKindBytes and ratioKindInodes are the two values app_volume_alerts.ratio_kind
// can hold, naming which dimension the row's persisted ratio measures. They
// alias notify.VolumeRatioKindBytes/Inodes rather than redeclaring the
// literals, since this package already imports notify and there is no cycle
// to avoid on this side.
const (
	ratioKindBytes  = notify.VolumeRatioKindBytes
	ratioKindInodes = notify.VolumeRatioKindInodes
)

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

// volumeUsageAlert is one PVC that crossed appVolumeAlertThreshold on at
// least one dimension, tagged with which dimension (Kind) and that
// dimension's ratio -- the value that actually gets persisted, emailed and
// shown in the console, as opposed to the other, still-under-threshold
// dimension of the same PVC.
type volumeUsageAlert struct {
	Namespace string
	PVCName   string
	Ratio     float64
	Kind      string
}

// volumeSampleKey is the (namespace, pvc) join key hotVolumeSamples uses to
// line up a PVC's byte sample with its inode sample.
func volumeSampleKey(namespace, pvc string) string {
	return namespace + "/" + pvc
}

// hotVolumeSamples merges the byte and inode fill-ratio samples for every
// PVC and keeps only the ones at or above ratio on at least one dimension,
// each tagged with the dimension that fired.
//
// A PVC over threshold on inodes is ALWAYS reported as ratioKindInodes, even
// when its byte ratio is also over threshold: inode exhaustion is the
// dimension the owner has to act on (delete/pack files), and "increasing the
// disk" -- the obvious, previously the ONLY, response to a byte-fill alert --
// does not touch the inode table at all (ext4 does not grow inodes on
// resize, see volumeInodeQuery's doc comment). Naming "bytes" for a PVC that
// is actually inode-exhausted would send the owner toward a fix that cannot
// work, which is worse than not naming a dimension at all.
func hotVolumeSamples(byteSamples, inodeSamples []volumeUsageSample, threshold float64) []volumeUsageAlert {
	hotInodes := map[string]volumeUsageSample{}
	for _, s := range overThreshold(inodeSamples, threshold) {
		hotInodes[volumeSampleKey(s.Namespace, s.PVCName)] = s
	}

	out := make([]volumeUsageAlert, 0)
	seen := map[string]bool{}
	for _, s := range overThreshold(byteSamples, threshold) {
		key := volumeSampleKey(s.Namespace, s.PVCName)
		seen[key] = true
		if inode, ok := hotInodes[key]; ok {
			out = append(out, volumeUsageAlert{Namespace: s.Namespace, PVCName: s.PVCName, Ratio: inode.Ratio, Kind: ratioKindInodes})
			continue
		}
		out = append(out, volumeUsageAlert{Namespace: s.Namespace, PVCName: s.PVCName, Ratio: s.Ratio, Kind: ratioKindBytes})
	}
	for key, s := range hotInodes {
		if seen[key] {
			continue
		}
		out = append(out, volumeUsageAlert{Namespace: s.Namespace, PVCName: s.PVCName, Ratio: s.Ratio, Kind: ratioKindInodes})
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

	byteSamples, err := w.h.prometheus.QueryInstant(ctx, volumeUsageQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-volume: bytes query failed: %v", err)
		return
	}
	inodeSamples, err := w.h.prometheus.QueryInstant(ctx, volumeInodeQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-volume: inodes query failed (continuing on bytes only): %v", err)
	}

	parsedBytes := parseVolumeUsageSamples(byteSamples)
	parsedInodes := parseVolumeUsageSamples(inodeSamples)
	byNamespace := map[string][]volumeUsageAlert{}
	for _, s := range hotVolumeSamples(parsedBytes, parsedInodes, appVolumeAlertThreshold) {
		if _, ok := nsProjects[s.Namespace]; !ok {
			continue
		}
		byNamespace[s.Namespace] = append(byNamespace[s.Namespace], s)
	}
	log.Printf("app-volume: tick byte_samples=%d inode_samples=%d hot_user_ns=%d", len(byteSamples), len(inodeSamples), len(byNamespace))

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
			w.maybeNotify(ctx, env.ProjectID, ns, appName, s.Ratio, s.Kind)
		}
	}
}

// claimAppVolumeAlertSlot atomically claims the right to send one alert for
// (namespace, app) by upserting app_volume_alerts, succeeding only when no
// send is recorded within cooldown. Race-free across replicas, identical
// shape to claimAppHealthAlertSlot but against its own table so a crash alert
// and a volume alert for the same app never suppress one another. ratio is
// persisted alongside the cooldown timestamp (P1-ALERTS-IN-UI) so the console
// can read back the detected fill level without a live Prometheus query.
// ratioKind (ratioKindBytes/ratioKindInodes) says which dimension ratio
// measures, so a reader of this row never has to guess which one filled.
func claimAppVolumeAlertSlot(ctx context.Context, pool *pgxpool.Pool, namespace, appName string, ratio float64, ratioKind string, cooldown time.Duration) bool {
	ct, err := pool.Exec(ctx,
		`INSERT INTO app_volume_alerts (namespace, app_name, last_sent_at, ratio, ratio_kind)
		 VALUES ($1, $2, now(), $3, $4)
		 ON CONFLICT (namespace, app_name) DO UPDATE SET last_sent_at = now(), ratio = $3, ratio_kind = $4
		 WHERE app_volume_alerts.last_sent_at <= now() - make_interval(secs => $5)`,
		namespace, appName, ratio, ratioKind, cooldown.Seconds())
	if err != nil {
		log.Printf("app-volume: cooldown claim for %s/%s failed: %v", namespace, appName, err)
		return false
	}
	return ct.RowsAffected() > 0
}

// touchAppVolumeAlertSeen unconditionally records "this over-threshold
// volume was observed right now", independent of the 24h email cooldown —
// mirrors touchAppHealthAlertSeen's rationale (P1-ALERTS-IN-UI-FRESHNESS):
// app_volume_alerts otherwise only gets written once per cooldown, so the
// console cannot distinguish a volume that is still filling from one that
// was resized 20 hours ago. Same epoch-sentinel INSERT path as the health
// touch, for the same reason: last_sent_at must stay untouched so the next
// claimAppVolumeAlertSlot call still fires the first real email. ratioKind
// is touched on every tick just like ratio, so the crash watcher's
// volumeInodesExhausted lookup (app_health_watcher.go) always reflects the
// most recent tick, not whichever dimension last crossed the 24h email
// cooldown.
func touchAppVolumeAlertSeen(ctx context.Context, pool *pgxpool.Pool, namespace, appName string, ratio float64, ratioKind string) {
	_, err := pool.Exec(ctx,
		`INSERT INTO app_volume_alerts (namespace, app_name, last_sent_at, last_seen_at, ratio, ratio_kind)
		 VALUES ($1, $2, to_timestamp(0), now(), $3, $4)
		 ON CONFLICT (namespace, app_name) DO UPDATE SET last_seen_at = now(), ratio = $3, ratio_kind = $4`,
		namespace, appName, ratio, ratioKind)
	if err != nil {
		log.Printf("app-volume: touch-seen for %s/%s failed: %v", namespace, appName, err)
	}
}

// volumeInodesExhausted reports whether the most recent app_volume_alerts
// tick for (namespace, appName), if still within appVolumeAlertFreshWindow,
// found this app's PVC over threshold specifically on inodes. The crash
// watcher (app_health_watcher.go) calls this to decide whether an ENOSPC
// crash should be classified notify.CauseKindPlatformStorageInodes instead
// of the generic notify.CauseKindPlatformStorage -- reusing this table
// rather than issuing a second live Prometheus query from the crash path,
// since the volume watcher already measures both dimensions on its own
// 15-minute tick. A stale or missing row (app never over threshold, or not
// within the fresh window) returns false, which keeps the existing
// byte-fill wording -- the honest default when there is no fresher evidence
// either way.
func (h *Handler) volumeInodesExhausted(ctx context.Context, namespace, appName string) bool {
	var kind string
	err := h.pool.QueryRow(ctx,
		`SELECT ratio_kind FROM app_volume_alerts
		 WHERE namespace = $1 AND app_name = $2
		   AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $3)`,
		namespace, appName, appVolumeAlertFreshWindow.Seconds(),
	).Scan(&kind)
	if err != nil {
		return false
	}
	return kind == ratioKindInodes
}

// maybeNotify sends the owner alert for one over-threshold volume, gated by
// the per-app 24h cooldown persisted in app_volume_alerts. The recipient is
// resolved BEFORE the cooldown is claimed (P1-ALERT-OWNERLESS-DROP: claiming
// first meant a project with no resolvable owner burned its 24h cooldown slot
// on a drop, muting real alerts even after ownership got fixed). The cooldown
// is still claimed before the actual send, so a slow/failing SMTP relay
// cannot cause a retry storm on the next tick. The unconditional seen-touch
// runs first, ahead of recipient resolution and the cooldown claim, so the
// console's "is this still over threshold" signal never depends on whether
// an email actually goes out this tick.
func (w *appVolumeWatcher) maybeNotify(ctx context.Context, projectID uuid.UUID, namespace, appName string, ratio float64, ratioKind string) {
	touchAppVolumeAlertSeen(ctx, w.h.pool, namespace, appName, ratio, ratioKind)

	to, source := w.h.resolveAlertRecipient(ctx, projectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		log.Printf("app-volume: no owner/member/org/operator recipient for project %s, dropping alert for app=%s kind=%s ratio=%.3f", projectID, appName, ratioKind, ratio)
		return
	}

	if !claimAppVolumeAlertSlot(ctx, w.h.pool, namespace, appName, ratio, ratioKind, appVolumeAlertCooldown) {
		return
	}

	size := w.h.declaredVolumeSize(ctx, projectID, appName)
	consoleLink := fmt.Sprintf("%s/projects/%s/apps/%s/settings?tab=storage", w.h.cfg.PublicBaseURL, projectID, appName)
	subject, body := notify.ComposeVolumeAlert(appName, ratio, ratioKind, size, consoleLink)
	if source == alertSourceOperator {
		log.Printf("app-volume: WARN no reachable owner for project %s, falling back to operator for app=%s kind=%s ratio=%.3f", projectID, appName, ratioKind, ratio)
		subject, body = notify.ComposeNoOwnerFallback(projectID.String(), w.h.projectDisplayName(ctx, projectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("app-volume: send to %s failed for app=%s: %v", to, appName, err)
		w.h.recordNotifySend(ctx, projectID, "VolumeAlert", appName, source, err)
		return
	}
	w.h.recordNotifySend(ctx, projectID, "VolumeAlert", appName, source, nil)
	log.Printf("app-volume: alerted %s (source=%s) for app=%s ratio=%.3f", to, source, appName, ratio)
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
