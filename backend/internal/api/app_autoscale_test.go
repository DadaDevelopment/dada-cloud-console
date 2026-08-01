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
			got, grew, err := growEnvelope(from, tc.dimension)
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

	got, _, err := growEnvelope(from, "cpu")
	if err != nil {
		t.Fatalf("growEnvelope: %v", err)
	}
	if got.CPULimit != "2" || got.CPUReq != "500m" {
		t.Fatalf("cpu grew to %s/%s, want request to stay at a quarter of the limit", got.CPUReq, got.CPULimit)
	}

	got, _, err = growEnvelope(from, "memory")
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
		got, grew, err := growEnvelope(at, dimension)
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

	got, grew, err := growEnvelope(from, "cpu")
	if err != nil || !grew {
		t.Fatalf("growEnvelope(cpu) = (%v, %v, %v), want a clamped step", got, grew, err)
	}
	if got.CPULimit != appAutoscaleMaxCPULimit {
		t.Fatalf("cpu limit = %q, want the cap %q", got.CPULimit, appAutoscaleMaxCPULimit)
	}

	got, grew, err = growEnvelope(from, "memory")
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
		if _, grew, err := growEnvelope(from, "cpu"); err == nil && grew {
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
	if len(want) != 4 {
		t.Fatalf("expected 4 json tags on renderer.AppResources, found %v", want)
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
