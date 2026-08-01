package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// appAutoscaleWatchInterval is the poll period for the vertical autoscaler.
// Matched to appVolumeWatchInterval: both read the same class of slow-moving
// resource-pressure signal, and a shorter period would only raise the chance
// of sampling a transient spike.
const appAutoscaleWatchInterval = 15 * time.Minute

// appAutoscaleCPUThreshold is the CFS throttled/elapsed period ratio above
// which an app is considered CPU-starved. Calibrated against the 2026-08-01
// fonbet-value incident, where the app served no HTTP at all while sitting at
// 0.298 over a 20m window (0.637 lifetime): a quarter of scheduling periods
// spent throttled is already enough to make request handling unusable, and
// nothing healthy in the cluster sat above 0.25 when this was measured.
const appAutoscaleCPUThreshold = 0.25

// appAutoscaleMemThreshold is the working-set/limit ratio above which an app
// is considered memory-starved. Below 1.0 on purpose: the kernel starts
// reclaiming hard well before the OOM killer fires, so waiting for an actual
// OOM would mean only ever resizing apps after they have already crashed.
// fonbet-value sat at 0.9967 while live.
const appAutoscaleMemThreshold = 0.90

// appAutoscaleCooldown is the minimum spacing between two resizes of the same
// app. Much longer than the watch interval because a resize re-renders the
// workload chart and rolls the pod: the new size needs time to be observed
// under real traffic before the watcher is allowed to judge it again. It also
// bounds the blast radius of a mis-calibrated threshold to one rollout per app
// per 6h.
const appAutoscaleCooldown = 6 * time.Hour

// appAutoscaleFreshWindow is how recently last_seen_at must have been touched
// for the console to still treat an app as currently starved, mirroring
// appVolumeAlertFreshWindow's 3x margin over the tick interval.
const appAutoscaleFreshWindow = 3 * appAutoscaleWatchInterval

// autoscaleLookback is the range window for both pressure queries.
//
// last_over_time wraps the memory gauge for the same reason volumeUsageLookback
// exists: a starved pod is a pod that gets OOM-killed and rescheduled, which
// staleness-marks its series on the old node before the new node's first scrape
// lands. A bare instant query then drops precisely the pods the watcher exists
// to catch. The CPU expression is already a rate() over the same window and so
// is inherently gap-tolerant.
const autoscaleLookback = "20m"

// cpuThrottleQuery is the fraction of CFS periods in which the container was
// throttled. Container-level series only (container!="" drops the pod-level
// rollup and the pause container), summed per pod so a multi-container pod is
// judged as one workload.
var cpuThrottleQuery = fmt.Sprintf(
	`sum by (namespace,pod) (rate(container_cpu_cfs_throttled_periods_total{container!="",container!="POD"}[%[1]s]))
	 / sum by (namespace,pod) (rate(container_cpu_cfs_periods_total{container!="",container!="POD"}[%[1]s]))`,
	autoscaleLookback,
)

// memPressureQuery is working set over declared memory limit.
//
// The denominator comes from kube-state-metrics, not from cAdvisor's
// container_spec_memory_limit_bytes: that series does not exist in this
// cluster's Prometheus (verified live — the selector returns zero series), so
// the obvious cAdvisor-only formulation would silently evaluate to an empty
// vector and the watcher would never fire on memory at all.
var memPressureQuery = fmt.Sprintf(
	`sum by (namespace,pod) (last_over_time(container_memory_working_set_bytes{container!="",container!="POD"}[%[1]s]))
	 / sum by (namespace,pod) (last_over_time(kube_pod_container_resource_limits{resource="memory"}[%[1]s]))`,
	autoscaleLookback,
)

// autoscaleProfileLadder is the ordered resize path. It intentionally has a
// top: an app that is still starved on "large" is not resized further but
// reported to its owner, because past this point the cause is far more often a
// leak or a runaway loop than a genuine need for more hardware, and silently
// growing it forever would hide that.
var autoscaleProfileLadder = []string{"small", "medium", "large"}

// profileIndex is the position of a profile on the ladder, or -1 when the app
// is not on the ladder at all.
//
// Off-ladder is a real state, not a data error: an app can carry a hand-tuned
// resources block that matches no profile. The first version of this watcher
// treated off-ladder as "assume medium" and on its very first prod tick pushed
// a live app from a deliberate 100m/384Mi/1Gi-ephemeral spec onto the medium
// profile, shrinking its ephemeral storage from 1Gi to 500Mi. Guessing a
// position on a ladder the app was never on can move it DOWN.
func profileIndex(current string) int {
	for i, p := range autoscaleProfileLadder {
		if p == current {
			return i
		}
	}
	return -1
}

// nextProfile returns the next profile up the ladder and whether one exists.
// Only meaningful for a profile already on the ladder; callers must check
// profileIndex first, since a false here means "already at the top", not
// "unknown".
func nextProfile(current string) (string, bool) {
	i := profileIndex(current)
	if i < 0 || i+1 >= len(autoscaleProfileLadder) {
		return "", false
	}
	return autoscaleProfileLadder[i+1], true
}

// profileRequirement is the CPU/memory a profile asks the namespace quota for.
// Mirrors profileResources in gitops-agent/internal/renderer/renderer.go; the
// two must move together, and the unit test asserts the values match that
// renderer's ladder.
type profileRequirement struct {
	CPULimit    string
	MemoryLimit string
	CPUReq      string
	MemoryReq   string
}

var autoscaleProfileRequirements = map[string]profileRequirement{
	"small":  {CPULimit: "250m", MemoryLimit: "256Mi", CPUReq: "10m", MemoryReq: "128Mi"},
	"medium": {CPULimit: "500m", MemoryLimit: "512Mi", CPUReq: "100m", MemoryReq: "256Mi"},
	"large":  {CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "512Mi"},
}

// pressureSample is one pod's pressure ratio for a single dimension.
type pressureSample struct {
	Namespace string
	Pod       string
	Ratio     float64
}

// parsePressureSamples extracts (namespace, pod, ratio) from raw Prometheus
// samples, dropping rows with a missing label or a non-finite ratio (a zero
// limit divides to +Inf/NaN, which is not a real pressure level). Pure, so it
// is unit-tested without a live Prometheus.
func parsePressureSamples(samples []prometheus.Sample) []pressureSample {
	out := make([]pressureSample, 0, len(samples))
	for _, s := range samples {
		ns := s.Metric["namespace"]
		pod := s.Metric["pod"]
		if ns == "" || pod == "" {
			continue
		}
		if math.IsNaN(s.Point.V) || math.IsInf(s.Point.V, 0) {
			continue
		}
		out = append(out, pressureSample{Namespace: ns, Pod: pod, Ratio: s.Point.V})
	}
	return out
}

// starvedPod is one pod that crossed at least one pressure threshold, carrying
// which dimension tripped and by how much for the audit trail and the email.
type starvedPod struct {
	Namespace string
	Pod       string
	Reason    string
	Ratio     float64
}

// collectStarved merges the CPU and memory result sets into one list of pods
// over threshold. When a pod trips both, memory wins the label: an app pinned
// against its memory limit is being actively reclaimed by the kernel, which is
// the more destructive of the two and the one an owner should read first.
// Pure and unit-tested.
func collectStarved(cpu, mem []pressureSample, cpuThreshold, memThreshold float64) []starvedPod {
	byKey := map[string]starvedPod{}
	for _, s := range cpu {
		if s.Ratio < cpuThreshold {
			continue
		}
		byKey[s.Namespace+"/"+s.Pod] = starvedPod{Namespace: s.Namespace, Pod: s.Pod, Reason: "cpu", Ratio: s.Ratio}
	}
	for _, s := range mem {
		if s.Ratio < memThreshold {
			continue
		}
		byKey[s.Namespace+"/"+s.Pod] = starvedPod{Namespace: s.Namespace, Pod: s.Pod, Reason: "memory", Ratio: s.Ratio}
	}
	out := make([]starvedPod, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, v)
	}
	return out
}

// quotaHeadroom reports whether a namespace's ResourceQuota can absorb the
// delta between two profiles.
//
// This check is why the autoscaler cannot wedge an app: a resize that exceeds
// the namespace quota is not rejected at write time, it is accepted into git
// and then rejected by the API server at pod-creation time, leaving the app
// with zero running pods. Checking first turns that outage into a logged
// skip. Pure arithmetic over the parsed quantities, so it is unit-tested
// without a cluster.
func quotaHeadroom(hard, used corev1.ResourceList, from, to profileRequirement) (bool, string) {
	dims := []struct {
		name     string
		resource corev1.ResourceName
		fromQ    string
		toQ      string
	}{
		{"limits.cpu", corev1.ResourceLimitsCPU, from.CPULimit, to.CPULimit},
		{"limits.memory", corev1.ResourceLimitsMemory, from.MemoryLimit, to.MemoryLimit},
		{"requests.cpu", corev1.ResourceRequestsCPU, from.CPUReq, to.CPUReq},
		{"requests.memory", corev1.ResourceRequestsMemory, from.MemoryReq, to.MemoryReq},
	}
	for _, d := range dims {
		hardQ, ok := hard[d.resource]
		if !ok {
			continue
		}
		usedQ := used[d.resource]
		fromQ, err := resource.ParseQuantity(d.fromQ)
		if err != nil {
			return false, fmt.Sprintf("parse %s from-quantity: %v", d.name, err)
		}
		toQ, err := resource.ParseQuantity(d.toQ)
		if err != nil {
			return false, fmt.Sprintf("parse %s to-quantity: %v", d.name, err)
		}
		projected := usedQ.DeepCopy()
		projected.Sub(fromQ)
		projected.Add(toQ)
		if projected.Cmp(hardQ) > 0 {
			return false, fmt.Sprintf("%s would reach %s of %s", d.name, projected.String(), hardQ.String())
		}
	}
	return true, ""
}

// appAutoscaleWatcher polls Prometheus for CPU-throttle and memory-pressure
// ratios across user namespaces and moves a starved app one step up the
// resource-profile ladder, at most once per app per cooldown window.
//
// Vertical rather than horizontal on purpose. The pressure this exists to
// relieve is per-pod (a throttled cgroup, a reclaiming memcg); adding replicas
// leaves every replica just as starved, and would silently change the
// concurrency model of apps that never asked to run more than once.
type appAutoscaleWatcher struct {
	clientset kubernetes.Interface
	h         *Handler
}

// StartAppAutoscaleWatcher launches the autoscaler goroutine. No-op when
// Prometheus is unconfigured or off-cluster, so local dev and tests never
// spawn it. Mail is not required: resizing is useful even where the notifier
// is not configured, unlike the pure-alert watchers which have nothing to do
// without it.
func (h *Handler) StartAppAutoscaleWatcher(ctx context.Context) {
	if h.prometheus == nil {
		return
	}
	clientset := newAppHealthClientset()
	if clientset == nil {
		log.Printf("app-autoscale: no in-cluster client, watcher disabled")
		return
	}
	w := &appAutoscaleWatcher{clientset: clientset, h: h}
	log.Printf("app-autoscale: watcher started interval=%s cpu>=%.2f mem>=%.2f cooldown=%s",
		appAutoscaleWatchInterval, appAutoscaleCPUThreshold, appAutoscaleMemThreshold, appAutoscaleCooldown)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyAppAutoscaleWatch, "app-autoscale", w.tick)
		t := time.NewTicker(appAutoscaleWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyAppAutoscaleWatch, "app-autoscale", w.tick)
			}
		}
	}()
}

// podAppLabels maps pod names in namespace to the app they belong to, via the
// dada.io/app label the workload chart stamps on every pod template.
func (w *appAutoscaleWatcher) podAppLabels(ctx context.Context, namespace string) map[string]string {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pods, err := w.clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{LabelSelector: "dada.io/app"})
	if err != nil {
		log.Printf("app-autoscale: list pods in %s failed: %v", namespace, err)
		return nil
	}
	out := map[string]string{}
	for i := range pods.Items {
		if name := pods.Items[i].Labels["dada.io/app"]; name != "" {
			out[pods.Items[i].Name] = name
		}
	}
	return out
}

// namespaceQuota returns the first ResourceQuota in a namespace. A nil quota
// with a nil error means the namespace genuinely has none and so has nothing
// to overflow; an error means the quota could not be read at all.
//
// The two must stay distinguishable. Collapsing them into a bare nil makes the
// headroom check fail OPEN, which is how the first prod tick resized two apps
// without ever consulting a quota: the ServiceAccount lacked list on
// resourcequotas, the error was logged and swallowed, and nil read as
// "unquotaed". Callers treat a read failure as a reason to skip the resize.
func (w *appAutoscaleWatcher) namespaceQuota(ctx context.Context, namespace string) (*corev1.ResourceQuota, error) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	quotas, err := w.clientset.CoreV1().ResourceQuotas(namespace).List(listCtx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	if len(quotas.Items) == 0 {
		return nil, nil
	}
	return &quotas.Items[0], nil
}

// tick runs one pass: query both pressure dimensions, restrict to user
// namespaces, resolve pods to apps, and resize whatever is starved and out of
// cooldown. Every failure is logged and swallowed — one bad namespace must
// never block the rest, and the watcher must never crash the backend pod it
// runs inside.
func (w *appAutoscaleWatcher) tick(ctx context.Context) {
	nsProjects, err := w.h.namespaceProjects(ctx)
	if err != nil {
		log.Printf("app-autoscale: load namespaces failed: %v", err)
		return
	}

	cpuSamples, err := w.h.prometheus.QueryInstant(ctx, cpuThrottleQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-autoscale: cpu query failed: %v", err)
		return
	}
	memSamples, err := w.h.prometheus.QueryInstant(ctx, memPressureQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-autoscale: memory query failed: %v", err)
		return
	}

	starved := collectStarved(
		parsePressureSamples(cpuSamples), parsePressureSamples(memSamples),
		appAutoscaleCPUThreshold, appAutoscaleMemThreshold,
	)

	byNamespace := map[string][]starvedPod{}
	for _, s := range starved {
		if _, ok := nsProjects[s.Namespace]; !ok {
			continue
		}
		byNamespace[s.Namespace] = append(byNamespace[s.Namespace], s)
	}
	log.Printf("app-autoscale: tick cpu_series=%d mem_series=%d starved=%d hot_user_ns=%d",
		len(cpuSamples), len(memSamples), len(starved), len(byNamespace))

	for ns, hot := range byNamespace {
		env := nsProjects[ns]
		podApp := w.podAppLabels(ctx, ns)
		if podApp == nil {
			continue
		}
		seen := map[string]bool{}
		for _, s := range hot {
			appName := podApp[s.Pod]
			if appName == "" || seen[appName] {
				continue
			}
			seen[appName] = true
			w.maybeResize(ctx, env.ProjectID, ns, appName, s)
		}
	}
}

// claimAppAutoscaleSlot atomically claims the right to resize (namespace, app)
// once per cooldown window, race-free across console replicas. Same shape as
// claimAppVolumeAlertSlot, against its own table so a resize and a volume
// alert for the same app never suppress one another.
func claimAppAutoscaleSlot(ctx context.Context, pool *pgxpool.Pool, namespace, appName, from, to, reason string, ratio float64, cooldown time.Duration) bool {
	ct, err := pool.Exec(ctx,
		`INSERT INTO app_autoscale_events (namespace, app_name, last_sent_at, from_profile, to_profile, reason, ratio)
		 VALUES ($1, $2, now(), $3, $4, $5, $6)
		 ON CONFLICT (namespace, app_name) DO UPDATE
		   SET last_sent_at = now(), from_profile = $3, to_profile = $4, reason = $5, ratio = $6
		 WHERE app_autoscale_events.last_sent_at <= now() - make_interval(secs => $7)`,
		namespace, appName, from, to, reason, ratio, cooldown.Seconds())
	if err != nil {
		log.Printf("app-autoscale: cooldown claim for %s/%s failed: %v", namespace, appName, err)
		return false
	}
	return ct.RowsAffected() > 0
}

// touchAppAutoscaleSeen records "this app was observed starved right now",
// independent of the resize cooldown, so the console can tell an app that is
// still starving from one that was resized hours ago. Same epoch-sentinel
// insert as touchAppVolumeAlertSeen, and for the same reason: last_sent_at
// must stay untouched so the next claim still fires the first real resize.
func touchAppAutoscaleSeen(ctx context.Context, pool *pgxpool.Pool, namespace, appName, reason string, ratio float64) {
	_, err := pool.Exec(ctx,
		`INSERT INTO app_autoscale_events (namespace, app_name, last_sent_at, last_seen_at, reason, ratio)
		 VALUES ($1, $2, to_timestamp(0), now(), $3, $4)
		 ON CONFLICT (namespace, app_name) DO UPDATE SET last_seen_at = now(), reason = $3, ratio = $4`,
		namespace, appName, reason, ratio)
	if err != nil {
		log.Printf("app-autoscale: touch-seen for %s/%s failed: %v", namespace, appName, err)
	}
}

// appProfileState is the app's current profile, deployed image and environment,
// read from the snapshot the renderer itself consumes.
type appProfileState struct {
	EnvironmentID uuid.UUID
	Profile       string
	Image         string
}

// loadAppProfileState reads the app's live snapshot. Returns pgx.ErrNoRows
// when the app has no App snapshot, which is the normal case for a pod that
// belongs to something the console does not own.
func (h *Handler) loadAppProfileState(ctx context.Context, projectID uuid.UUID, appName string) (appProfileState, error) {
	var st appProfileState
	var summaryRaw []byte
	err := h.pool.QueryRow(ctx,
		`SELECT environment_id, summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND kind = 'App' AND name = $2
		 LIMIT 1`,
		projectID, appName,
	).Scan(&st.EnvironmentID, &summaryRaw)
	if err != nil {
		return st, err
	}
	var cur struct {
		Profile string `json:"profile"`
		Image   string `json:"image"`
	}
	if err := json.Unmarshal(summaryRaw, &cur); err != nil {
		return st, err
	}
	st.Profile = cur.Profile
	st.Image = cur.Image
	return st, nil
}

// applyProfileBump writes the new profile into both the snapshot the renderer
// reads and git_repos, then enqueues the same DeployImageVersion operation
// UpdateAppProfile uses, keeping the current image so gitops-agent re-renders
// the chart with the new limits. One transaction: a snapshot updated without
// its operation would silently take effect on some unrelated later deploy.
func (h *Handler) applyProfileBump(ctx context.Context, projectID, envID uuid.UUID, appName, toProfile, image string) (uuid.UUID, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var summaryRaw []byte
	if err := tx.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3
		 FOR UPDATE`,
		projectID, envID, appName,
	).Scan(&summaryRaw); err != nil {
		return uuid.Nil, err
	}
	var cur map[string]any
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur == nil {
		cur = map[string]any{}
	}
	cur["profile"] = toProfile
	updatedJSON, err := json.Marshal(cur)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE resource_snapshots SET summary_json = $1
		 WHERE project_id = $2 AND environment_id = $3 AND kind = 'App' AND name = $4`,
		updatedJSON, projectID, envID, appName); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE git_repos SET profile = $1
		 WHERE project_id = $2 AND environment_id = $3 AND app_name = $4`,
		toProfile, projectID, envID, appName); err != nil {
		return uuid.Nil, err
	}

	payloadBytes, err := json.Marshal(models.DeployImageVersionPayload{AppName: appName, Image: image})
	if err != nil {
		return uuid.Nil, err
	}
	var opID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id`,
		systemDeployActorID, projectID, envID, appName, payloadBytes,
	).Scan(&opID); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return opID, nil
}

// maybeResize moves one starved app up the ladder, subject to the ceiling, the
// namespace quota and the cooldown.
//
// Order matters. The seen-touch runs first and unconditionally, so the
// console's "still starved" signal never depends on whether a resize actually
// happened. The ceiling and quota checks run BEFORE the cooldown is claimed:
// claiming first would burn the app's 6h slot on a decision that changed
// nothing, muting the real resize that becomes possible the moment the quota
// is raised.
func (w *appAutoscaleWatcher) maybeResize(ctx context.Context, projectID uuid.UUID, namespace, appName string, s starvedPod) {
	touchAppAutoscaleSeen(ctx, w.h.pool, namespace, appName, s.Reason, s.Ratio)

	st, err := w.h.loadAppProfileState(ctx, projectID, appName)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		log.Printf("app-autoscale: load state for %s/%s failed: %v", namespace, appName, err)
		return
	}
	if st.Image == "" {
		log.Printf("app-autoscale: %s/%s has no deployed image, skipping", namespace, appName)
		return
	}

	if profileIndex(st.Profile) < 0 {
		log.Printf("app-autoscale: %s/%s runs off-ladder profile %q, leaving its resources alone", namespace, appName, st.Profile)
		return
	}

	to, ok := nextProfile(st.Profile)
	if !ok {
		w.notifyCeiling(ctx, projectID, namespace, appName, s)
		return
	}

	fromReq := autoscaleProfileRequirements[st.Profile]
	toReq := autoscaleProfileRequirements[to]
	quota, err := w.namespaceQuota(ctx, namespace)
	if err != nil {
		log.Printf("app-autoscale: %s/%s needs %s->%s but its quota could not be read, skipping: %v", namespace, appName, st.Profile, to, err)
		return
	}
	if quota != nil {
		if fits, why := quotaHeadroom(quota.Status.Hard, quota.Status.Used, fromReq, toReq); !fits {
			log.Printf("app-autoscale: %s/%s needs %s->%s but quota blocks it (%s)", namespace, appName, st.Profile, to, why)
			return
		}
	}

	if !claimAppAutoscaleSlot(ctx, w.h.pool, namespace, appName, st.Profile, to, s.Reason, s.Ratio, appAutoscaleCooldown) {
		return
	}

	opID, err := w.h.applyProfileBump(ctx, projectID, st.EnvironmentID, appName, to, st.Image)
	if err != nil {
		log.Printf("app-autoscale: resize %s/%s %s->%s failed: %v", namespace, appName, st.Profile, to, err)
		return
	}
	log.Printf("app-autoscale: resized %s/%s %s->%s reason=%s ratio=%.4f op=%s",
		namespace, appName, st.Profile, to, s.Reason, s.Ratio, opID)

	auditMeta, _ := json.Marshal(map[string]any{
		"from_profile": st.Profile, "to_profile": to,
		"reason": s.Reason, "ratio": s.Ratio, "pod": s.Pod,
		"claimed_by": "app-autoscale-watcher",
	})
	if _, err := w.h.pool.Exec(ctx,
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'AutoscaleApp', 'App', $4, $5)`,
		systemDeployActorID, projectID, opID, appName, auditMeta); err != nil {
		log.Printf("app-autoscale: audit for %s/%s failed: %v", namespace, appName, err)
	}

	w.notifyResized(ctx, projectID, appName, st.Profile, to, s)
}

// notifyResized tells the owner their app was resized. Best-effort: a resize
// that happened must not be rolled back because an email could not be sent.
func (w *appAutoscaleWatcher) notifyResized(ctx context.Context, projectID uuid.UUID, appName, from, to string, s starvedPod) {
	if w.h.auditNotifier == nil {
		return
	}
	to_, source := w.h.resolveAlertRecipient(ctx, projectID)
	if to_ == "" {
		to_ = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to_ == "" {
		return
	}
	link := fmt.Sprintf("%s/projects/%s/apps/%s/settings", w.h.cfg.PublicBaseURL, projectID, appName)
	subject, body := notify.ComposeAutoscaleNotice(appName, from, to, s.Reason, s.Ratio, link)
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(projectID.String(), w.h.projectDisplayName(ctx, projectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to_, subject, body); err != nil {
		log.Printf("app-autoscale: notice send to %s failed for app=%s: %v", to_, appName, err)
	}
}

// notifyCeiling tells the owner their app is starved at the top of the ladder,
// where the platform deliberately stops resizing. Gated by the same cooldown
// so it cannot become a 15-minute mail loop.
func (w *appAutoscaleWatcher) notifyCeiling(ctx context.Context, projectID uuid.UUID, namespace, appName string, s starvedPod) {
	if w.h.auditNotifier == nil {
		return
	}
	top := autoscaleProfileLadder[len(autoscaleProfileLadder)-1]
	if !claimAppAutoscaleSlot(ctx, w.h.pool, namespace, appName, top, top, s.Reason, s.Ratio, appAutoscaleCooldown) {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, projectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		return
	}
	link := fmt.Sprintf("%s/projects/%s/apps/%s/settings", w.h.cfg.PublicBaseURL, projectID, appName)
	subject, body := notify.ComposeAutoscaleCeiling(appName, top, s.Reason, s.Ratio, link)
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(projectID.String(), w.h.projectDisplayName(ctx, projectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("app-autoscale: ceiling send to %s failed for app=%s: %v", to, appName, err)
	}
}
