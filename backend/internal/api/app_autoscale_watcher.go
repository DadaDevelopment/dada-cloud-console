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

// appAutoscaleGrowthFactor is how much of the starved dimension one resize
// buys. Doubling rather than stepping a fixed amount because pressure is
// multiplicative: an app throttled in 60% of its CFS periods does not need 10%
// more CPU, and a series of small steps costs one 6h cooldown each while the
// app stays unusable the whole time.
const appAutoscaleGrowthFactor = 2

// appAutoscaleMaxCPULimit and appAutoscaleMaxMemoryLimit are the platform's
// absolute per-app cap, NOT the plan's. The plan's limit is the namespace
// ResourceQuota, which is checked separately and is the number a customer can
// actually raise by paying. This cap exists only so an app leaking in a
// namespace that carries no quota at all cannot grow until it evicts its
// neighbours off the node.
const (
	appAutoscaleMaxCPULimit    = "8"
	appAutoscaleMaxMemoryLimit = "16Gi"
)

// resourceEnvelope is one app's CPU/memory request and limit, in Kubernetes
// quantity notation. Mirrors renderer.AppResources in the gitops-agent module,
// which cannot be imported from here.
type resourceEnvelope struct {
	CPULimit    string
	MemoryLimit string
	CPUReq      string
	MemoryReq   string
}

// autoscaleProfileRequirements resolves the legacy small/medium/large names to
// the envelope the renderer still emits for apps that predate explicit sizing.
// Mirrors profileResources in gitops-agent/internal/renderer/renderer.go; the
// two must move together, and a unit test asserts the values match.
//
// This is a starting point, not a ladder. Once an app has been resized it
// carries its own envelope and never consults this table again.
var autoscaleProfileRequirements = map[string]resourceEnvelope{
	"small":  {CPULimit: "250m", MemoryLimit: "256Mi", CPUReq: "10m", MemoryReq: "128Mi"},
	"medium": {CPULimit: "500m", MemoryLimit: "512Mi", CPUReq: "100m", MemoryReq: "256Mi"},
	"large":  {CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "512Mi"},
}

// String renders an envelope for a log line, an audit row and the owner's mail.
func (e resourceEnvelope) String() string {
	return fmt.Sprintf("cpu %s/%s, mem %s/%s", e.CPUReq, e.CPULimit, e.MemoryReq, e.MemoryLimit)
}

// growEnvelope returns the envelope one resize up from cur along the starved
// dimension, and whether there was any room left to grow.
//
// Only the dimension under pressure moves. A CPU-throttled app given twice the
// memory it was not short of is billed for hardware it cannot use, and the
// resize still would not have relieved anything.
//
// Growth stops at the platform cap, clamping to it rather than refusing when
// the doubled value would overshoot: an app at 6 CPU that needs more should get
// the remaining 2, not nothing. Only an app already sitting at the cap reports
// no room. Pure arithmetic over parsed quantities, so it is unit-tested.
func growEnvelope(cur resourceEnvelope, dimension string) (resourceEnvelope, bool, error) {
	next := cur
	switch dimension {
	case "memory":
		limit, req, grew, err := growPair(cur.MemoryLimit, cur.MemoryReq, appAutoscaleMaxMemoryLimit, resource.BinarySI)
		if err != nil || !grew {
			return cur, false, err
		}
		next.MemoryLimit, next.MemoryReq = limit, req
	default:
		limit, req, grew, err := growPair(cur.CPULimit, cur.CPUReq, appAutoscaleMaxCPULimit, resource.DecimalSI)
		if err != nil || !grew {
			return cur, false, err
		}
		next.CPULimit, next.CPUReq = limit, req
	}
	return next, true, nil
}

// growPair doubles a limit and moves its request with it, clamped to max.
//
// The request keeps the ratio it had to the limit rather than being doubled on
// its own. That ratio is the platform's packing economics: the default envelope
// requests 10m against a 250m limit, so the scheduler fits roughly 25x more
// pods on a node than the limits alone would suggest. Growing the limit while
// pinning the request would let a starved app widen that ratio without bound;
// growing the request to match the limit would collapse it to 1 and cost a
// node. Deriving from the ratio also means a limit clamped at the cap does not
// leave the request overshooting past it.
//
// The ratio is computed in milli-units for both formats so a sub-unit CPU
// request survives the round trip; the memory result is converted back to whole
// bytes, since BinarySI has no milli notation worth emitting.
func growPair(limitQ, reqQ, maxQ string, format resource.Format) (limit, req string, grew bool, err error) {
	curLimit, err := resource.ParseQuantity(limitQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse limit %q: %w", limitQ, err)
	}
	curReq, err := resource.ParseQuantity(reqQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse request %q: %w", reqQ, err)
	}
	capQ, err := resource.ParseQuantity(maxQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse cap %q: %w", maxQ, err)
	}
	if curLimit.MilliValue() <= 0 {
		return "", "", false, fmt.Errorf("limit %q is not a positive quantity", limitQ)
	}
	if curLimit.Cmp(capQ) >= 0 {
		return "", "", false, nil
	}
	newLimit := scaleQuantity(curLimit, appAutoscaleGrowthFactor, format)
	if newLimit.Cmp(capQ) > 0 {
		newLimit = capQ.DeepCopy()
	}
	ratio := float64(curReq.MilliValue()) / float64(curLimit.MilliValue())
	newReqMilli := int64(float64(newLimit.MilliValue()) * ratio)
	newReq := resource.NewMilliQuantity(newReqMilli, format)
	if format == resource.BinarySI {
		newReq = resource.NewQuantity(newReqMilli/1000, resource.BinarySI)
	}
	return newLimit.String(), newReq.String(), true, nil
}

// scaleQuantity multiplies a quantity, keeping it in the notation its dimension
// is normally written in.
func scaleQuantity(q resource.Quantity, factor int64, format resource.Format) resource.Quantity {
	if format == resource.BinarySI {
		return *resource.NewQuantity(q.Value()*factor, resource.BinarySI)
	}
	return *resource.NewMilliQuantity(q.MilliValue()*factor, resource.DecimalSI)
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
func quotaHeadroom(hard, used corev1.ResourceList, from, to resourceEnvelope) (bool, string) {
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
	Resources     *resourceEnvelope
}

// Envelope resolves the app's current sizing: its own explicit envelope when it
// has been sized, otherwise the legacy profile's. The second return is false
// for an app that has neither -- a hand-maintained values.yaml whose profile
// name matches nothing the console knows.
//
// That case is a refusal, not a default. The first version of this watcher
// treated an unknown profile as "assume medium" and on its very first
// production tick pushed a live app from a deliberate 100m/384Mi spec onto the
// medium profile, shrinking its ephemeral storage from 1Gi to 500Mi. Guessing
// where an app sits can move it DOWN.
func (s appProfileState) Envelope() (resourceEnvelope, bool) {
	if s.Resources != nil {
		return *s.Resources, true
	}
	e, ok := autoscaleProfileRequirements[s.Profile]
	return e, ok
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
		Profile   string             `json:"profile"`
		Image     string             `json:"image"`
		Resources *snapshotResources `json:"resources"`
	}
	if err := json.Unmarshal(summaryRaw, &cur); err != nil {
		return st, err
	}
	st.Profile = cur.Profile
	st.Image = cur.Image
	if r := cur.Resources; r != nil &&
		r.CPURequest != "" && r.MemoryRequest != "" && r.CPULimit != "" && r.MemoryLimit != "" {
		st.Resources = &resourceEnvelope{
			CPULimit: r.CPULimit, MemoryLimit: r.MemoryLimit,
			CPUReq: r.CPURequest, MemoryReq: r.MemoryRequest,
		}
	}
	return st, nil
}

// snapshotResources is the summary_json["resources"] shape, pinned to
// renderer.AppResources' json tags in the gitops-agent module. A test asserts
// the two stay identical; they are separate Go modules and cannot share the
// type, and a silent drift here means the renderer falls back to the profile
// ceiling on an app the console had already grown past it.
type snapshotResources struct {
	CPURequest    string `json:"cpu_request"`
	MemoryRequest string `json:"memory_request"`
	CPULimit      string `json:"cpu_limit"`
	MemoryLimit   string `json:"memory_limit"`
}

func (e resourceEnvelope) snapshot() snapshotResources {
	return snapshotResources{
		CPURequest:    e.CPUReq,
		MemoryRequest: e.MemoryReq,
		CPULimit:      e.CPULimit,
		MemoryLimit:   e.MemoryLimit,
	}
}

// applyResourceGrowth writes the new envelope into the snapshot the renderer
// reads, then enqueues the same DeployImageVersion operation a console deploy
// uses, keeping the current image so gitops-agent re-renders the chart with the
// new numbers. One transaction: a snapshot updated without its operation would
// silently take effect on some unrelated later deploy.
//
// git_repos.profile is deliberately left alone. It is now only the fallback for
// apps that have never been sized, and rewriting it would make a grown app look
// like it still sits on a preset -- which is exactly the read the renderer uses
// to decide the app has no envelope of its own.
func (h *Handler) applyResourceGrowth(ctx context.Context, projectID, envID uuid.UUID, appName string, to resourceEnvelope, image string) (uuid.UUID, error) {
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
	cur["resources"] = to.snapshot()
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

// maybeResize grows one starved app's resource envelope, subject to the
// platform cap, the namespace quota and the cooldown.
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
		w.auditRefusal(ctx, projectID, st, namespace, appName, "no_deployed_image", s, nil)
		return
	}

	from, known := st.Envelope()
	if !known {
		log.Printf("app-autoscale: %s/%s carries neither an explicit envelope nor a known profile (%q), leaving its resources alone", namespace, appName, st.Profile)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "unsized_app", s, nil)
		return
	}

	to, room, err := growEnvelope(from, s.Reason)
	if err != nil {
		log.Printf("app-autoscale: %s/%s has an unreadable envelope (%s): %v", namespace, appName, from, err)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "envelope_unreadable", s, map[string]any{"error": err.Error()})
		return
	}
	if !room {
		w.auditRefusal(ctx, projectID, st, namespace, appName, "at_ceiling", s, map[string]any{"envelope": from.String()})
		w.notifyCeiling(ctx, projectID, namespace, appName, from, s)
		return
	}

	quota, err := w.namespaceQuota(ctx, namespace)
	if err != nil {
		log.Printf("app-autoscale: %s/%s needs %s -> %s but its quota could not be read, skipping: %v", namespace, appName, from, to, err)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "quota_unreadable", s, map[string]any{"to_envelope": to.String(), "error": err.Error()})
		return
	}
	if quota != nil {
		if fits, why := quotaHeadroom(quota.Status.Hard, quota.Status.Used, from, to); !fits {
			log.Printf("app-autoscale: %s/%s needs %s -> %s but quota blocks it (%s)", namespace, appName, from, to, why)
			w.auditRefusal(ctx, projectID, st, namespace, appName, "quota_blocked", s, map[string]any{"to_envelope": to.String(), "detail": why})
			w.notifyCeiling(ctx, projectID, namespace, appName, from, s)
			return
		}
	}

	if !claimAppAutoscaleSlot(ctx, w.h.pool, namespace, appName, from.String(), to.String(), s.Reason, s.Ratio, appAutoscaleCooldown) {
		return
	}

	opID, err := w.h.applyResourceGrowth(ctx, projectID, st.EnvironmentID, appName, to, st.Image)
	if err != nil {
		log.Printf("app-autoscale: resize %s/%s %s -> %s failed: %v", namespace, appName, from, to, err)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "resize_failed", s, map[string]any{"to_envelope": to.String(), "error": err.Error()})
		return
	}
	log.Printf("app-autoscale: resized %s/%s %s -> %s reason=%s ratio=%.4f op=%s",
		namespace, appName, from, to, s.Reason, s.Ratio, opID)

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: st.EnvironmentID,
		OperationID:   opID,
		Action:        auditActionAutoscaleApp,
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"from_envelope": from.String(), "to_envelope": to.String(),
			"dimension": s.Reason, "ratio": s.Ratio, "pod": s.Pod,
			"namespace":  namespace,
			"claimed_by": "app-autoscale-watcher",
		},
	})

	w.notifyResized(ctx, projectID, appName, from.String(), to.String(), s)
}

// auditRefusal records a starvation the watcher saw and deliberately did not
// act on. Every branch above it used to end in a log line and nothing else, so
// "the autoscaler never looked at this app" and "the autoscaler looked, and the
// namespace quota refused to let it grow" produced the same audit trail: none.
// That is the difference between a platform bug and a plan limit, and support
// could not tell them apart from the customer's history.
//
// The environment id comes from the profile state rather than being left NULL,
// so a refusal lands on the same axis as the deploy it failed to become.
//
// Gated by a shared Redis claim keyed on app AND reason, with the resize
// cooldown as its TTL: the watcher ticks every 15 minutes, and an app parked
// against its quota would otherwise write four identical rows an hour forever.
// A different reason claims its own key, because a refusal that changed cause
// is news. The claim deliberately does NOT touch app_autoscale_events: burning
// the resize slot on a decision that changed nothing is exactly what
// maybeResize's ordering exists to prevent. Redis down means the gate opens and
// the rows are written anyway -- duplicate history beats missing history.
func (w *appAutoscaleWatcher) auditRefusal(ctx context.Context, projectID uuid.UUID, st appProfileState, namespace, appName, reason string, s starvedPod, extra map[string]any) {
	if !w.h.cache.TryClaim(ctx, "audit:autoscale-refusal:"+namespace+"/"+appName+":"+reason, appAutoscaleCooldown) {
		return
	}
	meta := map[string]any{
		"refusal":      reason,
		"from_profile": st.Profile,
		"pressure":     s.Reason,
		"ratio":        s.Ratio,
		"pod":          s.Pod,
		"namespace":    namespace,
		"claimed_by":   "app-autoscale-watcher",
	}
	for k, v := range extra {
		meta[k] = v
	}
	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: st.EnvironmentID,
		Action:        auditActionAutoscaleApp,
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeFailure,
		Metadata:      meta,
	})
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

// notifyCeiling tells the owner their app is starved but cannot grow: either it
// already sits at the platform cap or the project quota has no headroom left.
// Gated by the same cooldown so it cannot become a 15-minute mail loop.
func (w *appAutoscaleWatcher) notifyCeiling(ctx context.Context, projectID uuid.UUID, namespace, appName string, at resourceEnvelope, s starvedPod) {
	if w.h.auditNotifier == nil {
		return
	}
	top := at.String()
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
