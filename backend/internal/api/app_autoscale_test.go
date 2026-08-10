package api

import (
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func promSample(ns, pod string, v float64) prometheus.Sample {
	return prometheus.Sample{
		Metric: map[string]string{"namespace": ns, "pod": pod},
		Point:  prometheus.Point{V: v},
	}
}

func TestParsePressureSamplesKeepsWellFormedRows(t *testing.T) {
	got := parsePressureSamples([]prometheus.Sample{
		promSample("proj-prod", "web-1", 0.42),
		promSample("proj-prod", "web-2", 0),
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 samples, got %+v", got)
	}
	if got[0].Namespace != "proj-prod" || got[0].Pod != "web-1" || got[0].Ratio != 0.42 {
		t.Fatalf("unexpected first sample: %+v", got[0])
	}
}

func TestParsePressureSamplesDropsUnusableRows(t *testing.T) {
	cases := []struct {
		name   string
		sample prometheus.Sample
	}{
		{"missing namespace", prometheus.Sample{Metric: map[string]string{"pod": "web-1"}, Point: prometheus.Point{V: 0.9}}},
		{"missing pod", prometheus.Sample{Metric: map[string]string{"namespace": "proj-prod"}, Point: prometheus.Point{V: 0.9}}},
		{"no labels at all", prometheus.Sample{Metric: map[string]string{}, Point: prometheus.Point{V: 0.9}}},
		{"NaN ratio", promSample("proj-prod", "web-1", math.NaN())},
		{"positive infinity from a zero limit", promSample("proj-prod", "web-1", math.Inf(1))},
		{"negative infinity", promSample("proj-prod", "web-1", math.Inf(-1))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePressureSamples([]prometheus.Sample{tc.sample}); len(got) != 0 {
				t.Fatalf("expected the row to be dropped, got %+v", got)
			}
		})
	}
}

func sortStarved(in []starvedPod) []starvedPod {
	out := append([]starvedPod(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Pod < out[j].Pod
	})
	return out
}

func TestCollectStarvedAppliesThresholds(t *testing.T) {
	cpu := []pressureSample{
		{Namespace: "a-prod", Pod: "over", Ratio: 0.30},
		{Namespace: "a-prod", Pod: "at-threshold", Ratio: appAutoscaleCPUThreshold},
		{Namespace: "a-prod", Pod: "under", Ratio: 0.24},
	}
	mem := []pressureSample{
		{Namespace: "b-prod", Pod: "over", Ratio: 0.995},
		{Namespace: "b-prod", Pod: "under", Ratio: 0.89},
	}

	got := sortStarved(collectStarved(cpu, mem, appAutoscaleCPUThreshold, appAutoscaleMemThreshold))

	if len(got) != 3 {
		t.Fatalf("expected 3 starved pods, got %+v", got)
	}
	want := []starvedPod{
		{Namespace: "a-prod", Pod: "at-threshold", Reason: "cpu", Ratio: appAutoscaleCPUThreshold},
		{Namespace: "a-prod", Pod: "over", Reason: "cpu", Ratio: 0.30},
		{Namespace: "b-prod", Pod: "over", Reason: "memory", Ratio: 0.995},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pod %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCollectStarvedPrefersMemoryWhenBothTrip(t *testing.T) {
	cpu := []pressureSample{{Namespace: "a-prod", Pod: "web", Ratio: 0.61}}
	mem := []pressureSample{{Namespace: "a-prod", Pod: "web", Ratio: 0.9994}}

	got := collectStarved(cpu, mem, appAutoscaleCPUThreshold, appAutoscaleMemThreshold)

	if len(got) != 1 {
		t.Fatalf("expected the two dimensions to collapse into one pod, got %+v", got)
	}
	if got[0].Reason != "memory" || got[0].Ratio != 0.9994 {
		t.Fatalf("expected the memory reason to win, got %+v", got[0])
	}
}

func TestCollectStarvedEmptyWhenNothingOverThreshold(t *testing.T) {
	cpu := []pressureSample{{Namespace: "a-prod", Pod: "web", Ratio: 0.01}}
	mem := []pressureSample{{Namespace: "a-prod", Pod: "web", Ratio: 0.30}}

	if got := collectStarved(cpu, mem, appAutoscaleCPUThreshold, appAutoscaleMemThreshold); len(got) != 0 {
		t.Fatalf("expected no starved pods, got %+v", got)
	}
}

func TestGrowEnvelopeDoublesTheStarvedDimensionOnly(t *testing.T) {
	from := resourceEnvelope{CPULimit: "500m", MemoryLimit: "512Mi", CPUReq: "100m", MemoryReq: "256Mi"}

	cases := []struct {
		dimension string
		want      resourceEnvelope
	}{
		{"cpu", resourceEnvelope{CPULimit: "1", MemoryLimit: "512Mi", CPUReq: "200m", MemoryReq: "256Mi"}},
		{"memory", resourceEnvelope{CPULimit: "500m", MemoryLimit: "1Gi", CPUReq: "100m", MemoryReq: "512Mi"}},
	}

	for _, tc := range cases {
		t.Run(tc.dimension, func(t *testing.T) {
			got, grew, _, err := growEnvelope(from, tc.dimension, nil)
			if err != nil {
				t.Fatalf("growEnvelope: %v", err)
			}
			if !grew {
				t.Fatal("expected room to grow well below the platform cap")
			}
			if got != tc.want {
				t.Fatalf("growEnvelope(%v, %q) = %v, want %v", from, tc.dimension, got, tc.want)
			}
		})
	}
}

// The request/limit ratio is what keeps the scheduler's view of an app honest:
// doubling only the limit would let a starved app grow its burst ceiling while
// still being packed onto a node as if it were tiny.
func TestGrowEnvelopePreservesTheRequestToLimitRatio(t *testing.T) {
	from := resourceEnvelope{CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "256Mi"}

	got, _, _, err := growEnvelope(from, "cpu", nil)
	if err != nil {
		t.Fatalf("growEnvelope: %v", err)
	}
	if got.CPULimit != "2" || got.CPUReq != "500m" {
		t.Fatalf("cpu grew to %s/%s, want request to stay at a quarter of the limit", got.CPUReq, got.CPULimit)
	}

	got, _, _, err = growEnvelope(from, "memory", nil)
	if err != nil {
		t.Fatalf("growEnvelope: %v", err)
	}
	if got.MemoryLimit != "2Gi" || got.MemoryReq != "512Mi" {
		t.Fatalf("memory grew to %s/%s, want request to stay at a quarter of the limit", got.MemoryReq, got.MemoryLimit)
	}
}

// Without a cap a crash-looping app that never stops being starved would grow
// every cooldown until it cannot be scheduled on any node at all.
func TestGrowEnvelopeStopsAtThePlatformCap(t *testing.T) {
	at := resourceEnvelope{CPULimit: appAutoscaleMaxCPULimit, MemoryLimit: appAutoscaleMaxMemoryLimit, CPUReq: "2", MemoryReq: "4Gi"}

	for _, dimension := range []string{"cpu", "memory"} {
		got, grew, _, err := growEnvelope(at, dimension, nil)
		if err != nil {
			t.Fatalf("%s: growEnvelope: %v", dimension, err)
		}
		if grew {
			t.Fatalf("%s: grew past the platform cap to %v", dimension, got)
		}
	}
}

// A doubling that overshoots must land exactly on the cap rather than be
// refused: an app one step below the ceiling still deserves the last step.
func TestGrowEnvelopeClampsTheLastStepToTheCap(t *testing.T) {
	from := resourceEnvelope{CPULimit: "6", MemoryLimit: "12Gi", CPUReq: "3", MemoryReq: "6Gi"}

	got, grew, _, err := growEnvelope(from, "cpu", nil)
	if err != nil || !grew {
		t.Fatalf("growEnvelope(cpu) = (%v, %v, %v), want a clamped step", got, grew, err)
	}
	if got.CPULimit != appAutoscaleMaxCPULimit {
		t.Fatalf("cpu limit = %q, want the cap %q", got.CPULimit, appAutoscaleMaxCPULimit)
	}

	got, grew, _, err = growEnvelope(from, "memory", nil)
	if err != nil || !grew {
		t.Fatalf("growEnvelope(memory) = (%v, %v, %v), want a clamped step", got, grew, err)
	}
	if got.MemoryLimit != appAutoscaleMaxMemoryLimit {
		t.Fatalf("memory limit = %q, want the cap %q", got.MemoryLimit, appAutoscaleMaxMemoryLimit)
	}
}

// A snapshot written by hand can carry nonsense. Growing off a zero or an
// unparsable limit would silently produce a zero envelope, which the API server
// reads as "no limit at all".
func TestGrowEnvelopeRefusesAnUnusableEnvelope(t *testing.T) {
	for name, from := range map[string]resourceEnvelope{
		"unparsable limit": {CPULimit: "lots", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "256Mi"},
		"zero limit":       {CPULimit: "0", MemoryLimit: "1Gi", CPUReq: "0", MemoryReq: "256Mi"},
		"empty":            {},
	} {
		if _, grew, _, err := growEnvelope(from, "cpu", nil); err == nil && grew {
			t.Fatalf("%s: grew off an unusable envelope %v", name, from)
		}
	}
}

// An app that has never been sized falls back to its legacy profile; one that
// carries an explicit envelope must never be dragged back onto the ladder.
func TestEnvelopeFallsBackToTheProfileOnlyWhenUnsized(t *testing.T) {
	explicit := resourceEnvelope{CPULimit: "8", MemoryLimit: "16Gi", CPUReq: "2", MemoryReq: "4Gi"}
	got, ok := appProfileState{Profile: "small", Resources: &explicit}.Envelope()
	if !ok || got != explicit {
		t.Fatalf("Envelope() = (%v, %v), want the explicit envelope", got, ok)
	}

	got, ok = appProfileState{Profile: "medium"}.Envelope()
	if !ok || got != autoscaleProfileRequirements["medium"] {
		t.Fatalf("Envelope() = (%v, %v), want the medium profile", got, ok)
	}

	if _, ok := (appProfileState{Profile: "xlarge-handedited"}).Envelope(); ok {
		t.Fatal("an unknown profile with no envelope must be left alone, not guessed at")
	}
}

func quotaList(cpuLim, memLim, cpuReq, memReq string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceLimitsCPU:      resource.MustParse(cpuLim),
		corev1.ResourceLimitsMemory:   resource.MustParse(memLim),
		corev1.ResourceRequestsCPU:    resource.MustParse(cpuReq),
		corev1.ResourceRequestsMemory: resource.MustParse(memReq),
	}
}

func TestQuotaHeadroomAllowsBumpThatFits(t *testing.T) {
	hard := quotaList("16", "12Gi", "16", "12Gi")
	used := quotaList("500m", "512Mi", "100m", "256Mi")

	ok, reason := quotaHeadroom(hard, used, autoscaleProfileRequirements["medium"], autoscaleProfileRequirements["large"])

	if !ok {
		t.Fatalf("expected headroom in a nearly empty quota, blocked by %q", reason)
	}
	if reason != "" {
		t.Fatalf("expected an empty reason on success, got %q", reason)
	}
}

func TestQuotaHeadroomBlocksWhenMemoryLimitWouldOverflow(t *testing.T) {
	hard := quotaList("16", "1Gi", "16", "12Gi")
	used := quotaList("500m", "1Gi", "100m", "256Mi")

	ok, reason := quotaHeadroom(hard, used, autoscaleProfileRequirements["medium"], autoscaleProfileRequirements["large"])

	if ok {
		t.Fatal("expected the bump to be blocked: used memory limits are already at hard, and large asks for 512Mi more")
	}
	if !strings.Contains(reason, "limits.memory") {
		t.Fatalf("expected the reason to name limits.memory, got %q", reason)
	}
}

func TestQuotaHeadroomAllowsExactFit(t *testing.T) {
	hard := quotaList("16", "1Gi", "16", "12Gi")
	used := quotaList("500m", "512Mi", "100m", "256Mi")

	ok, reason := quotaHeadroom(hard, used, autoscaleProfileRequirements["medium"], autoscaleProfileRequirements["large"])

	if !ok {
		t.Fatalf("512Mi used - 512Mi from + 1Gi to == 1Gi hard should fit exactly, blocked by %q", reason)
	}
}

func TestQuotaHeadroomSkipsDimensionsTheQuotaDoesNotConstrain(t *testing.T) {
	hard := corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("16")}
	used := corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("500m")}

	ok, reason := quotaHeadroom(hard, used, autoscaleProfileRequirements["medium"], autoscaleProfileRequirements["large"])

	if !ok {
		t.Fatalf("a quota that only caps limits.cpu must not block on unconstrained dimensions, blocked by %q", reason)
	}
}

func TestQuotaHeadroomTreatsAbsentUsageAsZero(t *testing.T) {
	hard := quotaList("16", "12Gi", "16", "12Gi")

	ok, reason := quotaHeadroom(hard, corev1.ResourceList{}, autoscaleProfileRequirements["medium"], autoscaleProfileRequirements["large"])

	if !ok {
		t.Fatalf("an empty used-list is a fresh namespace, not an overflow, blocked by %q", reason)
	}
}

const rendererProfilePath = "../../../gitops-agent/internal/renderer/renderer.go"

var rendererResourcesRe = regexp.MustCompile(`(?s)(Requests|Limits):\s*map\[string\]string\{"cpu": "([^"]+)", "memory": "([^"]+)"\}`)

// rendererProfile is one profile as the gitops-agent renderer actually writes
// it into values.yaml.
type rendererProfile struct {
	CPUReq, MemReq, CPULimit, MemLimit string
}

// parseRendererProfiles reads profileResources out of the renderer source. The
// gitops-agent is a separate Go module, so the values cannot be imported; the
// autoscaler's copy is checked against the source text instead. A drift here is
// not cosmetic — quotaHeadroom would admit a bump the API server then rejects.
func parseRendererProfiles(t *testing.T, src string) map[string]rendererProfile {
	t.Helper()
	start := strings.Index(src, "func profileResources(")
	if start < 0 {
		t.Fatal("profileResources not found in the renderer source; the autoscaler's profile ladder can no longer be cross-checked")
	}
	body := src[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	arms := map[string]string{}
	for _, split := range []struct{ profile, marker string }{
		{"medium", `case "medium":`},
		{"large", `case "large":`},
		{"small", "default:"},
	} {
		i := strings.Index(body, split.marker)
		if i < 0 {
			t.Fatalf("arm %q (%s) not found in profileResources", split.profile, split.marker)
		}
		arms[split.profile] = body[i:]
	}

	out := map[string]rendererProfile{}
	for profile, arm := range arms {
		m := rendererResourcesRe.FindAllStringSubmatch(arm, 2)
		if len(m) != 2 {
			t.Fatalf("arm %q: expected a Requests and a Limits map, found %d", profile, len(m))
		}
		p := rendererProfile{}
		for _, g := range m {
			switch g[1] {
			case "Requests":
				p.CPUReq, p.MemReq = g[2], g[3]
			case "Limits":
				p.CPULimit, p.MemLimit = g[2], g[3]
			}
		}
		out[profile] = p
	}
	return out
}

var rendererAppResourcesRe = regexp.MustCompile(`(?m)^\s*\w+\s+string\s+` + "`" + `json:"([^"]+)"` + "`")

// The autoscaler writes the envelope into resource_snapshots.summary_json and
// the gitops-agent reads it back out of the same column, but they live in
// separate Go modules and cannot share the struct. A renamed tag on either side
// would not fail to compile -- it would make the renderer silently fall back to
// the profile ceiling on an app that had already been grown past it.
func TestSnapshotResourceTagsMatchRenderer(t *testing.T) {
	raw, err := os.ReadFile(rendererProfilePath)
	if os.IsNotExist(err) {
		t.Skipf("renderer source not present at %s (partial checkout)", rendererProfilePath)
	}
	if err != nil {
		t.Fatalf("read renderer source: %v", err)
	}

	src := string(raw)
	start := strings.Index(src, "type AppResources struct {")
	if start < 0 {
		t.Fatal("AppResources not found in the renderer source; the snapshot contract can no longer be cross-checked")
	}
	body := src[start:]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatal("AppResources declaration is unterminated in the renderer source")
	}

	var want []string
	for _, m := range rendererAppResourcesRe.FindAllStringSubmatch(body[:end], -1) {
		want = append(want, m[1])
	}
	if len(want) != 6 {
		t.Fatalf("expected 6 json tags on renderer.AppResources, found %v", want)
	}

	typ := reflect.TypeOf(snapshotResources{})
	if typ.NumField() != len(want) {
		t.Fatalf("snapshotResources has %d fields, renderer.AppResources has %d", typ.NumField(), len(want))
	}
	for i, tag := range want {
		if got := typ.Field(i).Tag.Get("json"); got != tag {
			t.Fatalf("field %d: snapshotResources tag %q != renderer.AppResources tag %q", i, got, tag)
		}
	}
}

func TestAutoscaleProfileRequirementsMatchRenderer(t *testing.T) {
	raw, err := os.ReadFile(rendererProfilePath)
	if os.IsNotExist(err) {
		t.Skipf("renderer source not present at %s (partial checkout)", rendererProfilePath)
	}
	if err != nil {
		t.Fatalf("read renderer source: %v", err)
	}

	rendered := parseRendererProfiles(t, string(raw))

	for profile, want := range rendered {
		got, ok := autoscaleProfileRequirements[profile]
		if !ok {
			t.Fatalf("renderer knows profile %q but the autoscaler does not", profile)
		}
		if got.CPULimit != want.CPULimit || got.MemoryLimit != want.MemLimit ||
			got.CPUReq != want.CPUReq || got.MemoryReq != want.MemReq {
			t.Fatalf("profile %q drifted from the renderer:\n autoscaler: %+v\n renderer:   %+v", profile, got, want)
		}
	}
}

// An app idle on both dimensions must pay for one rollout, not two, so both
// halve in the same call.
func TestShrinkEnvelopeHalvesEveryIdleDimensionInOneStep(t *testing.T) {
	from := resourceEnvelope{CPULimit: "2", MemoryLimit: "2Gi", CPUReq: "500m", MemoryReq: "512Mi"}
	want := resourceEnvelope{CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "256Mi"}

	got, moved, err := shrinkEnvelope(from, []string{"cpu", "memory"})
	if err != nil {
		t.Fatalf("shrinkEnvelope: %v", err)
	}
	if !moved {
		t.Fatal("expected room to shrink well above the floor")
	}
	if got != want {
		t.Fatalf("shrinkEnvelope(%v) = %v, want %v", from, got, want)
	}
}

// Shrinking is the exact reverse of growing, so an app that grew on CPU and
// then went idle on CPU alone must keep every byte of the memory it holds.
func TestShrinkEnvelopeTouchesOnlyTheIdleDimensions(t *testing.T) {
	from := resourceEnvelope{CPULimit: "2", MemoryLimit: "2Gi", CPUReq: "500m", MemoryReq: "512Mi"}

	got, moved, err := shrinkEnvelope(from, []string{"cpu"})
	if err != nil || !moved {
		t.Fatalf("shrinkEnvelope(cpu) = (%v, %v, %v), want a step down", got, moved, err)
	}
	if got.MemoryLimit != from.MemoryLimit || got.MemoryReq != from.MemoryReq {
		t.Fatalf("memory moved to %s/%s on a cpu-only shrink", got.MemoryReq, got.MemoryLimit)
	}
	if got.CPULimit != "1" || got.CPUReq != "250m" {
		t.Fatalf("cpu shrank to %s/%s, want 250m/1", got.CPUReq, got.CPULimit)
	}
}

// Without a floor an app that is idle because nobody uses it yet would be
// shrunk until its first real request cannot start at all.
func TestShrinkEnvelopeStopsAtTheFloor(t *testing.T) {
	at := autoscaleProfileRequirements[autoscaleFloorProfile]

	got, moved, err := shrinkEnvelope(at, []string{"cpu", "memory"})
	if err != nil {
		t.Fatalf("shrinkEnvelope: %v", err)
	}
	if moved {
		t.Fatalf("shrank below the floor to %v", got)
	}
}

// A halving that undershoots must land exactly on the floor rather than be
// refused, the same way growth clamps to the cap.
func TestShrinkEnvelopeClampsTheLastStepToTheFloor(t *testing.T) {
	from := resourceEnvelope{CPULimit: "300m", MemoryLimit: "384Mi", CPUReq: "150m", MemoryReq: "192Mi"}
	floor := autoscaleProfileRequirements[autoscaleFloorProfile]

	got, moved, err := shrinkEnvelope(from, []string{"cpu", "memory"})
	if err != nil || !moved {
		t.Fatalf("shrinkEnvelope = (%v, %v, %v), want a clamped step", got, moved, err)
	}
	if got.CPULimit != floor.CPULimit || got.MemoryLimit != floor.MemoryLimit {
		t.Fatalf("limits landed at %s/%s, want the floor %s/%s", got.CPULimit, got.MemoryLimit, floor.CPULimit, floor.MemoryLimit)
	}
}

// An envelope can reach the floor limit while its own ratio would put the
// request below what a new app is given, which the scheduler would read as an
// app that needs nothing.
func TestShrinkEnvelopeFloorsTheRequestIndependently(t *testing.T) {
	from := resourceEnvelope{CPULimit: "1", MemoryLimit: "2Gi", CPUReq: "10m", MemoryReq: "512Mi"}
	floor := autoscaleProfileRequirements[autoscaleFloorProfile]

	got, moved, err := shrinkEnvelope(from, []string{"cpu"})
	if err != nil || !moved {
		t.Fatalf("shrinkEnvelope(cpu) = (%v, %v, %v), want a step down", got, moved, err)
	}
	if got.CPULimit != "500m" {
		t.Fatalf("cpu limit = %q, want 500m", got.CPULimit)
	}
	if got.CPUReq != floor.CPUReq {
		t.Fatalf("cpu request = %q, want the floor %q", got.CPUReq, floor.CPUReq)
	}
}

// Shrinking off a zero or unparsable limit would write a zero envelope, which
// the API server reads as "no limit at all" — the opposite of housekeeping.
func TestShrinkEnvelopeRefusesAnUnusableEnvelope(t *testing.T) {
	for name, from := range map[string]resourceEnvelope{
		"unparsable limit": {CPULimit: "lots", MemoryLimit: "1Gi", CPUReq: "250m", MemoryReq: "256Mi"},
		"zero limit":       {CPULimit: "0", MemoryLimit: "1Gi", CPUReq: "0", MemoryReq: "256Mi"},
		"empty":            {},
	} {
		if _, moved, err := shrinkEnvelope(from, []string{"cpu"}); err == nil && moved {
			t.Fatalf("%s: shrank an unusable envelope %v", name, from)
		}
	}
}

func sortIdle(in []idlePod) []idlePod {
	out := append([]idlePod(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Pod < out[j].Pod
	})
	return out
}

func TestCollectIdleAppliesTheThreshold(t *testing.T) {
	cpu := []pressureSample{
		{Namespace: "a-prod", Pod: "at-threshold", Ratio: appAutoscaleShrinkThreshold},
		{Namespace: "a-prod", Pod: "busy", Ratio: 0.80},
		{Namespace: "a-prod", Pod: "idle", Ratio: 0.05},
	}
	mem := []pressureSample{
		{Namespace: "a-prod", Pod: "idle", Ratio: 0.10},
		{Namespace: "a-prod", Pod: "busy", Ratio: 0.90},
	}

	got := sortIdle(collectIdle(cpu, mem, appAutoscaleShrinkThreshold))

	if len(got) != 2 {
		t.Fatalf("expected 2 idle pods, got %+v", got)
	}
	if got[0].Pod != "at-threshold" || !reflect.DeepEqual(got[0].Dimensions, []string{"cpu"}) {
		t.Fatalf("at-threshold: got %+v, want cpu idle", got[0])
	}
	if got[1].Pod != "idle" || !reflect.DeepEqual(got[1].Dimensions, []string{"cpu", "memory"}) {
		t.Fatalf("idle: got %+v, want both dimensions", got[1])
	}
}

// The one failure mode that costs an owner real availability is shrinking on a
// query that returned nothing, so a dimension with no sample is never idle.
func TestCollectIdleTreatsAMissingSampleAsNotIdle(t *testing.T) {
	cpu := []pressureSample{{Namespace: "a-prod", Pod: "web", Ratio: 0.02}}

	got := collectIdle(cpu, nil, appAutoscaleShrinkThreshold)

	if len(got) != 1 {
		t.Fatalf("expected 1 idle pod, got %+v", got)
	}
	if !reflect.DeepEqual(got[0].Dimensions, []string{"cpu"}) {
		t.Fatalf("dimensions = %v, want cpu alone when memory did not resolve", got[0].Dimensions)
	}
	if _, ok := got[0].Ratios["memory"]; ok {
		t.Fatal("a memory ratio appeared out of an empty result set")
	}
}

func TestIdlePodDetailReadsAsEvidence(t *testing.T) {
	p := idlePod{
		Namespace:  "a-prod",
		Pod:        "web",
		Dimensions: []string{"cpu", "memory"},
		Ratios:     map[string]float64{"cpu": 0.08, "memory": 0.12},
	}

	if got, want := p.Detail(), "cpu peak 8% of limit, memory peak 12% of limit"; got != want {
		t.Fatalf("Detail() = %q, want %q", got, want)
	}
}

func container(name string, limits, requests map[corev1.ResourceName]string) corev1.Container {
	c := corev1.Container{Name: name}
	if limits != nil {
		c.Resources.Limits = corev1.ResourceList{}
		for k, v := range limits {
			c.Resources.Limits[k] = resource.MustParse(v)
		}
	}
	if requests != nil {
		c.Resources.Requests = corev1.ResourceList{}
		for k, v := range requests {
			c.Resources.Requests[k] = resource.MustParse(v)
		}
	}
	return c
}

func TestEnvelopeFromPodSpecAdoptsTheLiveSizing(t *testing.T) {
	got, ok := envelopeFromPodSpec([]corev1.Container{container("gateway-container",
		map[corev1.ResourceName]string{"cpu": "500m", "memory": "384Mi", "ephemeral-storage": "200Mi"},
		map[corev1.ResourceName]string{"cpu": "75m", "memory": "256Mi", "ephemeral-storage": "50Mi"})})
	if !ok {
		t.Fatal("a single fully specified container is exactly what adoption is for")
	}
	want := resourceEnvelope{
		CPULimit: "500m", MemoryLimit: "384Mi", CPUReq: "75m", MemoryReq: "256Mi",
		EphemeralLimit: "200Mi", EphemeralReq: "50Mi",
	}
	if got != want {
		t.Fatalf("adopted %+v, want %+v", got, want)
	}
}

func TestEnvelopeFromPodSpecCarriesNoEphemeralWhenTheAppHasNone(t *testing.T) {
	got, ok := envelopeFromPodSpec([]corev1.Container{container("app-container",
		map[corev1.ResourceName]string{"cpu": "250m", "memory": "256Mi"},
		map[corev1.ResourceName]string{"cpu": "10m", "memory": "128Mi"})})
	if !ok {
		t.Fatal("ephemeral storage is optional, not required")
	}
	if got.EphemeralLimit != "" || got.EphemeralReq != "" {
		t.Fatalf("invented an ephemeral envelope out of nothing: %+v", got)
	}
}

func TestEnvelopeFromPodSpecRefusesASidecarPod(t *testing.T) {
	sized := map[corev1.ResourceName]string{"cpu": "250m", "memory": "256Mi"}
	if _, ok := envelopeFromPodSpec([]corev1.Container{
		container("app-container", sized, sized),
		container("sidecar", sized, sized),
	}); ok {
		t.Fatal("a two-container pod cannot be attributed to the single container the snapshot renders")
	}
}

func TestEnvelopeFromPodSpecRefusesAContainerWithoutLimits(t *testing.T) {
	if _, ok := envelopeFromPodSpec([]corev1.Container{container("app-container",
		map[corev1.ResourceName]string{"cpu": "250m"},
		map[corev1.ResourceName]string{"cpu": "10m", "memory": "128Mi"})}); ok {
		t.Fatal("a missing memory limit leaves nothing to grow from")
	}
	if _, ok := envelopeFromPodSpec(nil); ok {
		t.Fatal("no containers is not an envelope")
	}
}

func TestGrowKeepsTheEphemeralEnvelopeUntouched(t *testing.T) {
	from := resourceEnvelope{
		CPULimit: "500m", MemoryLimit: "384Mi", CPUReq: "75m", MemoryReq: "256Mi",
		EphemeralLimit: "1Gi", EphemeralReq: "200Mi",
	}
	to, room, _, err := growEnvelope(from, "memory", nil)
	if err != nil || !room {
		t.Fatalf("growEnvelope(%+v) = room %v, err %v", from, room, err)
	}
	if to.EphemeralLimit != "1Gi" || to.EphemeralReq != "200Mi" {
		t.Fatalf("growing memory rewrote the ephemeral envelope: %+v", to)
	}
	if to.MemoryLimit != "768Mi" {
		t.Fatalf("memory limit = %q, want 768Mi", to.MemoryLimit)
	}
}

// Mirrors the fonbet-value incident: a namespace LimitRange caps memory
// tighter than the platform's own ceiling, so an app throttled on memory must
// stop at the LimitRange, not at appAutoscaleMaxMemoryLimit, and the refusal
// must say why in a way the audit trail can tell apart from an ordinary
// at-cap refusal.
func TestGrowEnvelopeStopsAtALimitRangeTighterThanThePlatformCap(t *testing.T) {
	from := resourceEnvelope{CPULimit: "500m", MemoryLimit: "2Gi", CPUReq: "100m", MemoryReq: "1Gi"}
	limitMax := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}

	got, grew, cappedBy, err := growEnvelope(from, "memory", limitMax)
	if err != nil {
		t.Fatalf("growEnvelope: %v", err)
	}
	if grew {
		t.Fatalf("grew past the namespace LimitRange to %v", got)
	}
	if cappedBy != "limitrange" {
		t.Fatalf("cappedBy = %q, want %q", cappedBy, "limitrange")
	}
}

// A namespace with no LimitRange (nil map) must behave exactly as before
// LimitRange existed: bounded only by the platform cap.
func TestGrowEnvelopeIgnoresAnAbsentLimitRange(t *testing.T) {
	from := resourceEnvelope{CPULimit: "500m", MemoryLimit: "512Mi", CPUReq: "100m", MemoryReq: "256Mi"}

	got, grew, cappedBy, err := growEnvelope(from, "memory", nil)
	if err != nil || !grew {
		t.Fatalf("growEnvelope(%+v, nil) = (%v, %v, %v, %v), want an ordinary doubling", from, got, grew, cappedBy, err)
	}
	if got.MemoryLimit != "1Gi" {
		t.Fatalf("memory limit = %q, want 1Gi", got.MemoryLimit)
	}
	if cappedBy != "" {
		t.Fatalf("cappedBy = %q, want empty on a successful grow", cappedBy)
	}
}

// A LimitRange looser than the platform cap must never let an app grow past
// the platform's own absolute ceiling.
func TestGrowEnvelopePlatformCapWinsOverALooserLimitRange(t *testing.T) {
	at := resourceEnvelope{CPULimit: appAutoscaleMaxCPULimit, MemoryLimit: appAutoscaleMaxMemoryLimit, CPUReq: "2", MemoryReq: "4Gi"}
	limitMax := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("32"),
		corev1.ResourceMemory: resource.MustParse("64Gi"),
	}

	for _, dimension := range []string{"cpu", "memory"} {
		got, grew, cappedBy, err := growEnvelope(at, dimension, limitMax)
		if err != nil {
			t.Fatalf("%s: growEnvelope: %v", dimension, err)
		}
		if grew {
			t.Fatalf("%s: grew past the platform cap to %v", dimension, got)
		}
		if cappedBy != "platform" {
			t.Fatalf("%s: cappedBy = %q, want %q", dimension, cappedBy, "platform")
		}
	}
}

// This is the self-healing case: an app's committed envelope already exceeds
// its namespace's LimitRange (fonbet-value sat at a 4Gi memory limit against a
// 2Gi max), so it must be clamped straight down to max, not merely refused
// from growing further.
func TestClampToLimitRangeShrinksAnEnvelopeAlreadyOverMax(t *testing.T) {
	from := resourceEnvelope{CPULimit: "1", MemoryLimit: "4Gi", CPUReq: "250m", MemoryReq: "2Gi"}
	max := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}

	got, moved, err := clampToLimitRange(from, max)
	if err != nil {
		t.Fatalf("clampToLimitRange: %v", err)
	}
	if !moved {
		t.Fatal("expected the over-max memory limit to move")
	}
	if got.MemoryLimit != "2Gi" {
		t.Fatalf("memory limit = %q, want 2Gi", got.MemoryLimit)
	}
	if got.MemoryReq != "1Gi" {
		t.Fatalf("memory request = %q, want 1Gi (ratio preserved)", got.MemoryReq)
	}
	if got.CPULimit != "1" || got.CPUReq != "250m" {
		t.Fatalf("cpu moved on a memory-only violation: %+v", got)
	}
}

// An envelope already within the LimitRange must be left untouched — this is
// what makes the repair pass a no-op on every tick after the first fix.
func TestClampToLimitRangeLeavesACompliantEnvelopeAlone(t *testing.T) {
	from := resourceEnvelope{CPULimit: "500m", MemoryLimit: "1Gi", CPUReq: "100m", MemoryReq: "512Mi"}
	max := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}

	got, moved, err := clampToLimitRange(from, max)
	if err != nil {
		t.Fatalf("clampToLimitRange: %v", err)
	}
	if moved {
		t.Fatalf("moved a compliant envelope: %+v", got)
	}
	if got != from {
		t.Fatalf("clampToLimitRange(%v) = %v, want it unchanged", from, got)
	}
}

// A resource the LimitRange says nothing about must never be touched, the
// same as quotaHeadroom skipping dimensions the quota does not constrain.
func TestClampToLimitRangeSkipsUnconstrainedDimensions(t *testing.T) {
	from := resourceEnvelope{CPULimit: "8", MemoryLimit: "1Gi", CPUReq: "2", MemoryReq: "512Mi"}
	max := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}

	got, moved, err := clampToLimitRange(from, max)
	if err != nil {
		t.Fatalf("clampToLimitRange: %v", err)
	}
	if moved {
		t.Fatalf("moved despite no LimitRange entry for cpu: %+v", got)
	}
}

// A ready replica must block the LimitRange repair -- this is the safety net
// distinguishing a dead app like fonbet-value from a live one that would be
// OOMKilled by shrinking it down to the LimitRange max.
func TestDecideLimitRangeRepairSkipsWhenAReplicaIsReady(t *testing.T) {
	if got := decideLimitRangeRepair(true, 1); got != limitRangeRepairSkipLive {
		t.Fatalf("decideLimitRangeRepair(found=true, ready=1) = %v, want limitRangeRepairSkipLive", got)
	}
}

// More than one ready replica must skip for the same reason as exactly one.
func TestDecideLimitRangeRepairSkipsWithMultipleReadyReplicas(t *testing.T) {
	if got := decideLimitRangeRepair(true, 3); got != limitRangeRepairSkipLive {
		t.Fatalf("decideLimitRangeRepair(found=true, ready=3) = %v, want limitRangeRepairSkipLive", got)
	}
}

// Zero ready replicas on an identified Deployment is the fonbet-value case:
// nothing is running, so clamping the envelope down is a pure repair.
func TestDecideLimitRangeRepairProceedsWhenNoReplicaIsReady(t *testing.T) {
	if got := decideLimitRangeRepair(true, 0); got != limitRangeRepairProceed {
		t.Fatalf("decideLimitRangeRepair(found=true, ready=0) = %v, want limitRangeRepairProceed", got)
	}
}

// The Deployment could not be identified at all -- no match, more than one
// match, or the read failed. This must never be treated as "dead": fail
// closed rather than guess.
func TestDecideLimitRangeRepairFailsClosedWhenDeploymentUnreadable(t *testing.T) {
	if got := decideLimitRangeRepair(false, 0); got != limitRangeRepairSkipUnknown {
		t.Fatalf("decideLimitRangeRepair(found=false, ready=0) = %v, want limitRangeRepairSkipUnknown", got)
	}
}

func TestSnapshotRoundTripsTheEphemeralEnvelope(t *testing.T) {
	e := resourceEnvelope{
		CPULimit: "500m", MemoryLimit: "384Mi", CPUReq: "75m", MemoryReq: "256Mi",
		EphemeralLimit: "1Gi", EphemeralReq: "200Mi",
	}
	s := e.snapshot()
	if s.EphemeralLimit != "1Gi" || s.EphemeralRequest != "200Mi" {
		t.Fatalf("snapshot dropped the ephemeral envelope: %+v", s)
	}
}
