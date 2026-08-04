package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
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
// app. Much longer than the watch interval because the new size needs time to
// be observed under real traffic before the watcher is allowed to judge it
// again -- a doubling takes effect on the running pod within seconds, but the
// throttling ratio that justified it is a 20m rate and lags well behind. It also
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

// appAutoscaleShrinkThreshold is the peak-usage/limit ratio below which an app
// is considered oversized. A quarter, so halving the dimension still leaves the
// observed peak twice the room it ever used.
const appAutoscaleShrinkThreshold = 0.25

// appAutoscaleShrinkWindow is how far back the peak is measured. A week rather
// than the grow path's twenty minutes because the question is the opposite one:
// growing asks "is this app suffering right now", shrinking asks "has this app
// been provably idle long enough that nothing it does needs this size" — and a
// workload that only runs on Mondays is not idle.
const appAutoscaleShrinkWindow = "7d"

// appAutoscaleShrinkMinAge is the minimum age of both the pod and the app's last
// resize before shrinking is allowed.
//
// It exists because the ratio is measured against the CURRENT limit over a
// window that may predate it. An app grown an hour ago has spent 167 of the last
// 168 hours under its old, smaller limit, so its peak against the new limit
// reads as idle and the watcher would immediately undo the growth it just
// performed. A pod younger than the window has the same defect from the other
// side: its series only covers its own lifetime, so a pod born ten minutes ago
// looks like a week of idleness.
const appAutoscaleShrinkMinAge = 7 * 24 * time.Hour

// appAutoscaleShrinkCooldown is the minimum spacing between two shrinks of the
// same app, held in the shared cache rather than in app_autoscale_events on
// purpose: a shrink must never occupy the row whose age gates the next GROW.
// Starvation relief is urgent, shrinking is housekeeping, and housekeeping must
// not be able to mute the urgent path.
const appAutoscaleShrinkCooldown = 48 * time.Hour

// appAutoscaleShrinkPassInterval is how often the shrink sweep runs. Far slower
// than the grow tick: its query window is a week, so a more frequent pass would
// re-ask a question whose answer cannot have changed, at the price of a
// week-wide range query per pass.
const appAutoscaleShrinkPassInterval = 6 * time.Hour

// autoscaleFloorProfile is the envelope shrinking stops at: the size a brand new
// app is given. Below it the platform saves nothing worth the rollout, and an
// app that idles for a week is not evidence that the default is too generous.
const autoscaleFloorProfile = "small"

// cpuIdleQuery is the highest five-minute CPU rate the pod reached over the
// window, over its declared CPU limit.
//
// The peak, not the average: an app that runs a heavy job for two minutes every
// hour averages near zero and would be shrunk into permanent throttling. Summing
// per-container maxima rather than taking the max of the sum overstates usage
// slightly, which is the safe direction — it can only prevent a shrink.
var cpuIdleQuery = fmt.Sprintf(
	`sum by (namespace,pod) (max_over_time(rate(container_cpu_usage_seconds_total{container!="",container!="POD"}[5m])[%[1]s:5m]))
	 / sum by (namespace,pod) (last_over_time(kube_pod_container_resource_limits{resource="cpu"}[1h]))`,
	appAutoscaleShrinkWindow,
)

// memIdleQuery is the highest working set the pod reached over the window, over
// its declared memory limit. The denominator comes from kube-state-metrics for
// the same reason memPressureQuery's does.
var memIdleQuery = fmt.Sprintf(
	`sum by (namespace,pod) (max_over_time(container_memory_working_set_bytes{container!="",container!="POD"}[%[1]s]))
	 / sum by (namespace,pod) (last_over_time(kube_pod_container_resource_limits{resource="memory"}[1h]))`,
	appAutoscaleShrinkWindow,
)

// resourceEnvelope is one app's CPU/memory request and limit, in Kubernetes
// quantity notation. Mirrors renderer.AppResources in the gitops-agent module,
// which cannot be imported from here.
//
// EphemeralLimit and EphemeralReq are carried, never sized: the autoscaler has
// no signal for disk pressure, but a render that omits an ephemeral-storage
// limit an app was given by hand deletes it, and the container is then evicted
// the first time it writes past the node default.
type resourceEnvelope struct {
	CPULimit       string
	MemoryLimit    string
	CPUReq         string
	MemoryReq      string
	EphemeralLimit string
	EphemeralReq   string
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

// shrinkEnvelope halves every dimension the caller found idle, floored at the
// size a new app starts from, and reports whether anything actually moved.
//
// Both dimensions can move in one call on purpose: each resize costs the app a
// rollout, so an app idle on CPU and memory should pay for one restart, not two.
// Pure arithmetic over parsed quantities, so it is unit-tested.
func shrinkEnvelope(cur resourceEnvelope, dimensions []string) (resourceEnvelope, bool, error) {
	floor, ok := autoscaleProfileRequirements[autoscaleFloorProfile]
	if !ok {
		return cur, false, fmt.Errorf("floor profile %q is not defined", autoscaleFloorProfile)
	}
	next := cur
	moved := false
	for _, dim := range dimensions {
		switch dim {
		case "cpu":
			limit, req, shrank, err := shrinkPair(cur.CPULimit, cur.CPUReq, floor.CPULimit, floor.CPUReq, resource.DecimalSI)
			if err != nil {
				return cur, false, err
			}
			if shrank {
				next.CPULimit, next.CPUReq = limit, req
				moved = true
			}
		case "memory":
			limit, req, shrank, err := shrinkPair(cur.MemoryLimit, cur.MemoryReq, floor.MemoryLimit, floor.MemoryReq, resource.BinarySI)
			if err != nil {
				return cur, false, err
			}
			if shrank {
				next.MemoryLimit, next.MemoryReq = limit, req
				moved = true
			}
		}
	}
	return next, moved, nil
}

// shrinkPair halves a limit and moves its request with it, floored at the
// default envelope's own numbers.
//
// The request follows the limit's ratio exactly as it does when growing, so an
// app that grew and then idled walks back down the same path it came up. The
// floor is applied to the request independently because an envelope can reach
// the floor limit while its ratio would put the request below the floor's.
func shrinkPair(limitQ, reqQ, floorLimitQ, floorReqQ string, format resource.Format) (limit, req string, shrank bool, err error) {
	curLimit, err := resource.ParseQuantity(limitQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse limit %q: %w", limitQ, err)
	}
	curReq, err := resource.ParseQuantity(reqQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse request %q: %w", reqQ, err)
	}
	floorLimit, err := resource.ParseQuantity(floorLimitQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse floor limit %q: %w", floorLimitQ, err)
	}
	floorReq, err := resource.ParseQuantity(floorReqQ)
	if err != nil {
		return "", "", false, fmt.Errorf("parse floor request %q: %w", floorReqQ, err)
	}
	if curLimit.MilliValue() <= 0 {
		return "", "", false, fmt.Errorf("limit %q is not a positive quantity", limitQ)
	}
	if curLimit.Cmp(floorLimit) <= 0 {
		return "", "", false, nil
	}
	newLimit := halveQuantity(curLimit, format)
	if newLimit.Cmp(floorLimit) < 0 {
		newLimit = floorLimit.DeepCopy()
	}
	ratio := float64(curReq.MilliValue()) / float64(curLimit.MilliValue())
	newReqMilli := int64(float64(newLimit.MilliValue()) * ratio)
	newReq := resource.NewMilliQuantity(newReqMilli, format)
	if format == resource.BinarySI {
		newReq = resource.NewQuantity(newReqMilli/1000, resource.BinarySI)
	}
	if newReq.Cmp(floorReq) < 0 {
		atFloor := floorReq.DeepCopy()
		newReq = &atFloor
	}
	return newLimit.String(), newReq.String(), true, nil
}

// halveQuantity divides a quantity in two, keeping it in the notation its
// dimension is normally written in.
func halveQuantity(q resource.Quantity, format resource.Format) resource.Quantity {
	if format == resource.BinarySI {
		return *resource.NewQuantity(q.Value()/2, resource.BinarySI)
	}
	return *resource.NewMilliQuantity(q.MilliValue()/2, resource.DecimalSI)
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
// which dimension tripped and by how much for the audit trail.
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

// idlePod is one pod whose peak usage stayed far below what it holds, carrying
// which dimensions were idle and by how much for the audit trail.
type idlePod struct {
	Namespace  string
	Pod        string
	Dimensions []string
	Ratios     map[string]float64
}

// Detail renders the idle dimensions for a log line and an audit row.
func (p idlePod) Detail() string {
	parts := make([]string, 0, len(p.Dimensions))
	for _, d := range p.Dimensions {
		parts = append(parts, fmt.Sprintf("%s peak %.0f%% of limit", d, p.Ratios[d]*100))
	}
	return strings.Join(parts, ", ")
}

// collectIdle merges the CPU and memory peak result sets into one list of pods
// sitting below the threshold, per dimension.
//
// A missing sample is never idleness. A pod whose CPU series did not resolve
// simply has no CPU dimension in the result, so it keeps the CPU it has: the
// only thing worse than an oversized app is one shrunk on the strength of a
// query that returned nothing. Dimensions come out in a fixed order so the audit
// metadata reads the same way every time. Pure and unit-tested.
func collectIdle(cpu, mem []pressureSample, threshold float64) []idlePod {
	byKey := map[string]*idlePod{}
	add := func(s pressureSample, dimension string) {
		if s.Ratio > threshold {
			return
		}
		key := s.Namespace + "/" + s.Pod
		p, ok := byKey[key]
		if !ok {
			p = &idlePod{Namespace: s.Namespace, Pod: s.Pod, Ratios: map[string]float64{}}
			byKey[key] = p
		}
		p.Dimensions = append(p.Dimensions, dimension)
		p.Ratios[dimension] = s.Ratio
	}
	for _, s := range cpu {
		add(s, "cpu")
	}
	for _, s := range mem {
		add(s, "memory")
	}
	out := make([]idlePod, 0, len(byKey))
	for _, p := range byKey {
		sort.Strings(p.Dimensions)
		out = append(out, *p)
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

// podApp is the app a pod belongs to, how long that pod has been alive, and
// its health as of the same list call.
//
// Age is what lets the shrink path tell a week of measured idleness from a pod
// that was created ten minutes ago and has a week-shaped query window. Ready
// and CrashLooping are what let the grow path tell real starvation from a
// pod repeatedly failing its own startup: both come off the same list call
// podAppLabels already makes, so reading them costs no extra API round trip.
type podApp struct {
	App          string
	Age          time.Duration
	Ready        bool
	CrashLooping bool
	RestartCount int32
}

// podAppLabels maps pod names in namespace to the app they belong to, via the
// dada.io/app label the workload chart stamps on every pod template.
func (w *appAutoscaleWatcher) podAppLabels(ctx context.Context, namespace string) map[string]podApp {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pods, err := w.clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{LabelSelector: "dada.io/app"})
	if err != nil {
		log.Printf("app-autoscale: list pods in %s failed: %v", namespace, err)
		return nil
	}
	out := map[string]podApp{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		name := pod.Labels["dada.io/app"]
		if name == "" {
			continue
		}
		var age time.Duration
		if started := pod.CreationTimestamp.Time; !started.IsZero() {
			age = time.Since(started)
		}
		out[pod.Name] = podApp{
			App:          name,
			Age:          age,
			Ready:        podIsReady(pod),
			CrashLooping: podIsCrashLooping(pod),
			RestartCount: podRestartCount(pod),
		}
	}
	return out
}

// podIsReady reads the PodReady condition, the same signal the Service
// endpoint controller uses to decide whether a pod may receive traffic.
func podIsReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podIsCrashLooping reports whether any container is waiting behind
// CrashLoopBackOff. This is a stronger and slower-to-clear signal than
// readiness alone: a pod flaps between NotReady and momentarily Running on
// every restart, so a tick that happens to land during the brief Running
// window would otherwise read a crashlooping pod as fine.
func podIsCrashLooping(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

// podRestartCount sums restarts across containers, carried onto the audit row
// for a refused resize so the reason is legible without a live kubectl.
func podRestartCount(pod *corev1.Pod) int32 {
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
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

// adoptEnvelope reads an app's CURRENT sizing off the running pod, for apps
// whose snapshot carries neither an explicit envelope nor a known profile.
//
// Those apps predate console-side sizing: their numbers live only in a
// hand-maintained values.yaml. The watcher used to refuse them outright, which
// left every one of them permanently unmanaged -- on this cluster that was five
// of the seven apps the autoscaler had ever looked at, every one of them
// refused with "unsized_app" while starving.
//
// Refusing was right for the first version, which guessed a PROFILE and pushed
// a live app from a deliberate 100m/384Mi spec down onto a preset. Reading the
// live spec is the opposite of a guess: it is the number the app is running on
// right now, so adopting it and then growing the starved dimension can only
// move that dimension up.
//
// Adoption is also a repair. The renderer falls back to the SMALL preset for an
// app with no envelope, so the next deploy of one of these apps -- by anyone,
// for any reason -- silently downsizes it. Writing the live numbers into the
// snapshot is what stops that.
func (w *appAutoscaleWatcher) adoptEnvelope(ctx context.Context, namespace, pod string) (resourceEnvelope, bool) {
	getCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p, err := w.clientset.CoreV1().Pods(namespace).Get(getCtx, pod, metav1.GetOptions{})
	if err != nil {
		log.Printf("app-autoscale: reading live sizing of %s/%s failed: %v", namespace, pod, err)
		return resourceEnvelope{}, false
	}
	return envelopeFromPodSpec(p.Spec.Containers)
}

// envelopeFromPodSpec derives an envelope from a pod's containers. Pure, so it
// is unit-tested.
//
// Exactly one container, with all four of cpu/memory request and limit set.
// A pod with a sidecar is refused rather than summed: the snapshot renders a
// single container's resources, so summing would hand the app the sidecar's
// share as well and doubling would compound it. A container missing a limit is
// refused because there is no current value to grow from.
func envelopeFromPodSpec(containers []corev1.Container) (resourceEnvelope, bool) {
	if len(containers) != 1 {
		return resourceEnvelope{}, false
	}
	res := containers[0].Resources
	cpuLimit, okCPULimit := res.Limits[corev1.ResourceCPU]
	memLimit, okMemLimit := res.Limits[corev1.ResourceMemory]
	cpuReq, okCPUReq := res.Requests[corev1.ResourceCPU]
	memReq, okMemReq := res.Requests[corev1.ResourceMemory]
	if !okCPULimit || !okMemLimit || !okCPUReq || !okMemReq {
		return resourceEnvelope{}, false
	}
	if cpuLimit.IsZero() || memLimit.IsZero() {
		return resourceEnvelope{}, false
	}
	e := resourceEnvelope{
		CPULimit:    cpuLimit.String(),
		MemoryLimit: memLimit.String(),
		CPUReq:      cpuReq.String(),
		MemoryReq:   memReq.String(),
	}
	if q, ok := res.Limits[corev1.ResourceEphemeralStorage]; ok {
		e.EphemeralLimit = q.String()
	}
	if q, ok := res.Requests[corev1.ResourceEphemeralStorage]; ok {
		e.EphemeralReq = q.String()
	}
	return e, true
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
			ref := podApp[s.Pod]
			if ref.App == "" || seen[ref.App] {
				continue
			}
			seen[ref.App] = true
			w.maybeResize(ctx, env.ProjectID, ns, ref.App, s, ref)
		}
	}

	w.convergeLiveSizes(ctx, nsProjects)

	if w.h.cache.TryClaim(ctx, "autoscale:shrink-pass", appAutoscaleShrinkPassInterval) {
		w.shrinkPass(ctx, nsProjects)
	}
}

// convergeLiveSizes puts back a size the app already earned but lost.
//
// A grown app keeps its numbers in two places: in git, which is what a fresh
// pod is built from, and in the running pod, which is where the resize was
// actuated without a restart. Those two agree until the Deployment's template
// is the one that gets read -- a node drain, an eviction, a manual rollout --
// and the replacement pod comes back on whatever the template still says.
//
// Without this pass that regression waits for the app to starve again AND for
// its 6h cooldown to expire, so an app could sit throttled for hours on a size
// the platform had already decided it should not have. Converging is not a new
// decision and deliberately claims no cooldown, writes no audit row and sends
// nothing: it only re-asserts the envelope already recorded for the app, and
// only ever upwards.
func (w *appAutoscaleWatcher) convergeLiveSizes(ctx context.Context, nsProjects map[string]namespaceEnv) {
	for ns, env := range nsProjects {
		listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		pods, err := w.clientset.CoreV1().Pods(ns).List(listCtx, metav1.ListOptions{LabelSelector: "dada.io/app"})
		cancel()
		if err != nil {
			continue
		}
		live := map[string]resourceEnvelope{}
		for i := range pods.Items {
			pod := &pods.Items[i]
			appName := pod.Labels["dada.io/app"]
			if appName == "" || pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
				continue
			}
			if _, seen := live[appName]; seen {
				continue
			}
			if e, ok := envelopeFromPodSpec(pod.Spec.Containers); ok {
				live[appName] = e
			}
		}
		for appName, have := range live {
			st, err := w.h.loadAppProfileState(ctx, env.ProjectID, appName)
			if err != nil {
				continue
			}
			want, known := st.Envelope()
			if !known || !want.exceeds(have) {
				continue
			}
			out := w.resizeLivePods(ctx, ns, appName, want)
			log.Printf("app-autoscale: converged %s/%s %s -> %s (pod had drifted below its recorded envelope) %s",
				ns, appName, have, want, out)
		}
	}
}

// shrinkPass runs one housekeeping sweep: find apps whose peak usage over a week
// stayed far below what they hold, and hand the surplus back.
//
// This is the half of autoscaling that has no customer asking for it, and it is
// the half that decides whether the other half keeps working. Growth is one-way
// without it: every app that ever spiked keeps the size that spike bought it
// forever, the namespace ResourceQuota fills up with headroom nobody uses, and
// the next app that genuinely starves is refused with quota_blocked while an
// idle neighbour sits on eight cores. The customer's bill does not change either
// way — plans are priced per app, database and domain, not per core — so the
// only thing shrinking buys is exactly that: room for the app that needs it.
//
// Failures are logged and swallowed, like the grow tick's: this pass is
// housekeeping and must never take the watcher down with it.
func (w *appAutoscaleWatcher) shrinkPass(ctx context.Context, nsProjects map[string]namespaceEnv) {
	cpuSamples, err := w.h.prometheus.QueryInstant(ctx, cpuIdleQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-autoscale: cpu idle query failed: %v", err)
		return
	}
	memSamples, err := w.h.prometheus.QueryInstant(ctx, memIdleQuery, time.Time{}, "")
	if err != nil {
		log.Printf("app-autoscale: memory idle query failed: %v", err)
		return
	}

	idle := collectIdle(
		parsePressureSamples(cpuSamples), parsePressureSamples(memSamples),
		appAutoscaleShrinkThreshold,
	)

	byNamespace := map[string][]idlePod{}
	for _, p := range idle {
		if _, ok := nsProjects[p.Namespace]; !ok {
			continue
		}
		byNamespace[p.Namespace] = append(byNamespace[p.Namespace], p)
	}
	log.Printf("app-autoscale: shrink pass cpu_series=%d mem_series=%d idle=%d idle_user_ns=%d",
		len(cpuSamples), len(memSamples), len(idle), len(byNamespace))

	for ns, cold := range byNamespace {
		env := nsProjects[ns]
		pods := w.podAppLabels(ctx, ns)
		if pods == nil {
			continue
		}
		seen := map[string]bool{}
		for _, p := range cold {
			ref := pods[p.Pod]
			if ref.App == "" || seen[ref.App] {
				continue
			}
			if ref.Age < appAutoscaleShrinkMinAge {
				continue
			}
			seen[ref.App] = true
			w.maybeShrink(ctx, env.ProjectID, ns, ref.App, p)
		}
	}
}

// lastAutoscaleResize reports how long ago the watcher last resized this app.
// A missing row means it never has, which is reported as a very long time so a
// never-touched app is free to shrink.
func lastAutoscaleResize(ctx context.Context, pool *pgxpool.Pool, namespace, appName string) (time.Duration, error) {
	var since time.Duration
	var seconds float64
	err := pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (now() - last_sent_at))::float8 FROM app_autoscale_events
		 WHERE namespace = $1 AND app_name = $2`,
		namespace, appName).Scan(&seconds)
	if err == pgx.ErrNoRows {
		return time.Duration(math.MaxInt64), nil
	}
	if err != nil {
		return since, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// maybeShrink hands back the surplus of one idle app.
//
// The guards are all about not undoing a decision that was right. The app must
// carry a readable envelope (guessing where an app sits can move it DOWN, which
// is precisely the direction this function moves), its last resize must be older
// than the measurement window, and the shrink cooldown must be free. No quota
// check is needed in this direction: giving resources back cannot overflow
// anything.
func (w *appAutoscaleWatcher) maybeShrink(ctx context.Context, projectID uuid.UUID, namespace, appName string, p idlePod) {
	st, err := w.h.loadAppProfileState(ctx, projectID, appName)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		log.Printf("app-autoscale: load state for %s/%s failed: %v", namespace, appName, err)
		return
	}
	if st.Image == "" {
		return
	}

	from, known := st.Envelope()
	if !known {
		from, known = w.adoptEnvelope(ctx, namespace, p.Pod)
		if !known {
			return
		}
	}

	since, err := lastAutoscaleResize(ctx, w.h.pool, namespace, appName)
	if err != nil {
		log.Printf("app-autoscale: last-resize lookup for %s/%s failed: %v", namespace, appName, err)
		return
	}
	if since < appAutoscaleShrinkMinAge {
		return
	}

	to, moved, err := shrinkEnvelope(from, p.Dimensions)
	if err != nil {
		log.Printf("app-autoscale: %s/%s has an unreadable envelope (%s): %v", namespace, appName, from, err)
		return
	}
	if !moved {
		return
	}

	if !w.h.cache.TryClaim(ctx, "autoscale:shrink:"+namespace+"/"+appName, appAutoscaleShrinkCooldown) {
		return
	}

	opID, err := w.h.applyResourceEnvelope(ctx, projectID, st.EnvironmentID, appName, to)
	if err != nil {
		log.Printf("app-autoscale: shrink %s/%s %s -> %s failed: %v", namespace, appName, from, to, err)
		return
	}
	live := w.resizeLivePods(ctx, namespace, appName, to)
	log.Printf("app-autoscale: shrank %s/%s %s -> %s (%s) op=%s in_place=%s",
		namespace, appName, from, to, p.Detail(), opID, live)

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: st.EnvironmentID,
		OperationID:   opID,
		Action:        auditActionAutoscaleApp,
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"direction":     "down",
			"from_envelope": from.String(), "to_envelope": to.String(),
			"dimensions": p.Dimensions, "ratios": p.Ratios, "pod": p.Pod,
			"window":     appAutoscaleShrinkWindow,
			"namespace":  namespace,
			"claimed_by": "app-autoscale-watcher",
		},
	})
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
			EphemeralLimit: r.EphemeralLimit, EphemeralReq: r.EphemeralRequest,
		}
	}
	return st, nil
}

// snapshotResources is the summary_json["resources"] shape, pinned to
// renderer.AppResources' json tags in the gitops-agent module. A test asserts
// the two stay identical; they are separate Go modules and cannot share the
// type, and a silent drift here means the renderer falls back to the profile
// ceiling on an app the console had already grown past it.
// It is an alias rather than a copy so the shape written into the snapshot and
// the shape sent in a ResizeApp payload cannot drift apart.
type snapshotResources = models.AppResourceEnvelope

func (e resourceEnvelope) snapshot() snapshotResources {
	return snapshotResources{
		CPURequest:       e.CPUReq,
		MemoryRequest:    e.MemoryReq,
		CPULimit:         e.CPULimit,
		MemoryLimit:      e.MemoryLimit,
		EphemeralRequest: e.EphemeralReq,
		EphemeralLimit:   e.EphemeralLimit,
	}
}

// applyResourceEnvelope writes a new envelope into the snapshot the renderer
// reads, then enqueues a ResizeApp operation carrying the same numbers. One
// transaction: a snapshot updated without its operation would silently take
// effect on some unrelated later deploy.
//
// It deliberately does not enqueue a deploy. A deploy regenerates values.yaml
// out of the database, and for the hand-maintained apps on this cluster that
// render drops env vars, volumes and managed-database declarations the database
// never knew about -- so the agent's clobber guard refuses the operation, and
// the autoscaler resizes nothing. ResizeApp patches the resource scalars inside
// the file that is already in git, which has nothing to drop and nothing to
// refuse.
//
// Direction-agnostic: growing and shrinking differ only in the numbers handed
// in.
//
// The commit is durability, not delivery. What actually changes the app's size
// now is resizeLivePods, which patches the running pod through the resize
// subresource; this writes the same numbers where a pod built later will read
// them. The tenant ApplicationSet ignores container resources when deciding
// whether an app is out of sync precisely so that this commit does not roll a
// new ReplicaSet behind the in-place resize.
//
// git_repos.profile is deliberately left alone. It is now only the fallback for
// apps that have never been sized, and rewriting it would make a grown app look
// like it still sits on a preset -- which is exactly the read the renderer uses
// to decide the app has no envelope of its own.
func (h *Handler) applyResourceEnvelope(ctx context.Context, projectID, envID uuid.UUID, appName string, to resourceEnvelope) (uuid.UUID, error) {
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

	payloadBytes, err := json.Marshal(models.ResizeAppPayload{AppName: appName, Resources: to.snapshot()})
	if err != nil {
		return uuid.Nil, err
	}
	var opID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'ResizeApp', 'App', $4, 'Created', $5)
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

// maybeResize grows one starved app's resource envelope, subject to
// readiness, the platform cap, the namespace quota and the cooldown.
//
// Order matters. The seen-touch runs first and unconditionally, so the
// console's "still starved" signal never depends on whether a resize actually
// happened. The readiness gate runs right after state is loaded, before any
// of the growth arithmetic: a pod that is not Ready or is visibly
// crashlooping is not suffering under real traffic, it is failing its own
// startup, and the throttle/pressure ratio that got it into `hot` is an
// honest side effect of that failure (see the CPU-threshold comment above),
// not evidence the app needs more room. Growing it anyway is how a dead app
// on this cluster got resized upward five times while it had never served a
// request. Restart count alone is deliberately NOT a gate: an app that
// recovered from one crash and is Ready now must still grow under real
// pressure, or the fonbet-value case regresses. The ceiling and quota checks
// run BEFORE the cooldown is claimed: claiming first would burn the app's 6h
// slot on a decision that changed nothing, muting the real resize that
// becomes possible the moment the quota is raised.
func (w *appAutoscaleWatcher) maybeResize(ctx context.Context, projectID uuid.UUID, namespace, appName string, s starvedPod, pod podApp) {
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
	if !pod.Ready || pod.CrashLooping {
		log.Printf("app-autoscale: %s/%s is not ready (ready=%v crashlooping=%v restarts=%d), refusing to grow a pod that has never served traffic",
			namespace, appName, pod.Ready, pod.CrashLooping, pod.RestartCount)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "app_not_ready", s, map[string]any{
			"ready": pod.Ready, "crashlooping": pod.CrashLooping, "restart_count": pod.RestartCount,
		})
		return
	}

	from, known := st.Envelope()
	if !known {
		from, known = w.adoptEnvelope(ctx, namespace, s.Pod)
		if !known {
			log.Printf("app-autoscale: %s/%s carries neither an explicit envelope nor a known profile (%q), and its live pod has no readable sizing either", namespace, appName, st.Profile)
			w.auditRefusal(ctx, projectID, st, namespace, appName, "unsized_app", s, nil)
			return
		}
		log.Printf("app-autoscale: %s/%s had no envelope in its snapshot, adopting its live sizing (%s)", namespace, appName, from)
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

	if blocked, why := w.consumptionBlocked(ctx, projectID); blocked {
		log.Printf("app-autoscale: %s/%s needs %s -> %s but its org is over what the free plan includes (%s), refusing to grow it", namespace, appName, from, to, why)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "consumption_blocked", s, map[string]any{"to_envelope": to.String(), "detail": why})
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

	opID, err := w.h.applyResourceEnvelope(ctx, projectID, st.EnvironmentID, appName, to)
	if err != nil {
		log.Printf("app-autoscale: resize %s/%s %s -> %s failed: %v", namespace, appName, from, to, err)
		w.auditRefusal(ctx, projectID, st, namespace, appName, "resize_failed", s, map[string]any{"to_envelope": to.String(), "error": err.Error()})
		return
	}
	live := w.resizeLivePods(ctx, namespace, appName, to)
	log.Printf("app-autoscale: resized %s/%s %s -> %s reason=%s ratio=%.4f op=%s in_place=%s",
		namespace, appName, from, to, s.Reason, s.Ratio, opID, live)

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: st.EnvironmentID,
		OperationID:   opID,
		Action:        auditActionAutoscaleApp,
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"direction":     "up",
			"from_envelope": from.String(), "to_envelope": to.String(),
			"dimension": s.Reason, "ratio": s.Ratio, "pod": s.Pod,
			"namespace":     namespace,
			"claimed_by":    "app-autoscale-watcher",
			"in_place_pods": live.Resized + live.Pending,
			"restarted":     live.Total() == 0 || live.Failed > 0,
		},
	})
}

// consumptionBlocked reports whether the app's org has burned past what its
// free plan includes, with the numbers for the refusal record.
//
// The autoscaler is where an unpaid footprint actually grows. The console no
// longer offers sizes to users, so nobody clicks "make it bigger" -- the
// platform decides, silently, on its own money. Gating creation while leaving
// this open would block the one path that costs nothing to leave open and leave
// the one that spends open.
//
// It gates the GROWTH pass only. shrinkPass never asks: an account over its
// allowance must always be allowed to get smaller, and a gate that also blocked
// shrinking would pin the exact accounts it exists to slow down at their largest
// size.
//
// An unresolvable org opens the gate. The watcher runs unattended on every app
// on the cluster, and a lookup failure must not become "nothing on the platform
// may grow tonight".
func (w *appAutoscaleWatcher) consumptionBlocked(ctx context.Context, projectID uuid.UUID) (bool, string) {
	orgID, err := w.h.projectOrg(ctx, projectID)
	if err != nil || orgID == "" {
		return false, ""
	}
	var ce *consumptionExceededError
	if errors.As(w.h.checkConsumption(ctx, orgID), &ce) {
		return true, ce.Error()
	}
	return false, ""
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

// notifyCeiling tells the owner their app is starved but cannot grow: either it
// already sits at the platform cap or the project quota has no headroom left.
// Gated by the same cooldown so it cannot become a 15-minute mail loop.
//
// It is the ONLY email this watcher sends, and deliberately so. A resize that
// worked is not news: the owner never asked for a size, cannot set one, and has
// nothing to do about it, so announcing "we gave you another gigabyte" is spam
// that also invites the wrong worry about the bill. Successful resizes leave a
// log line and an audit row and nothing else. This one is different because the
// platform gave up: the app is degraded, nothing was changed, and the fix is in
// the owner's code. Do not add resize notifications back.
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
		w.h.recordNotifySend(ctx, projectID, "AutoscaleCeiling", appName, source, err)
		return
	}
	w.h.recordNotifySend(ctx, projectID, "AutoscaleCeiling", appName, source, nil)
}
