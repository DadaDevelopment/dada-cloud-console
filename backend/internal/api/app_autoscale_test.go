package api

import (
	"math"
	"os"
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

func TestNextProfileWalksTheLadder(t *testing.T) {
	cases := []struct {
		current string
		want    string
		wantOK  bool
	}{
		{"small", "medium", true},
		{"medium", "large", true},
		{"large", "", false},
		{"", "", false},
		{"xlarge-handedited", "", false},
	}

	for _, tc := range cases {
		t.Run("from="+tc.current, func(t *testing.T) {
			got, ok := nextProfile(tc.current)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("nextProfile(%q) = (%q, %v), want (%q, %v)", tc.current, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestProfileIndexSeparatesOffLadderFromTopOfLadder(t *testing.T) {
	cases := []struct {
		profile string
		want    int
	}{
		{"small", 0},
		{"medium", 1},
		{"large", 2},
		{"", -1},
		{"xlarge-handedited", -1},
	}

	for _, tc := range cases {
		t.Run("profile="+tc.profile, func(t *testing.T) {
			if got := profileIndex(tc.profile); got != tc.want {
				t.Fatalf("profileIndex(%q) = %d, want %d", tc.profile, got, tc.want)
			}
		})
	}
}

// An off-ladder app is one somebody gave a hand-tuned resources block. Guessing
// a rung for it can shrink a limit it depends on, so the watcher must leave it
// alone rather than assume a position.
func TestOffLadderProfileIsNeverGuessedIntoTheLadder(t *testing.T) {
	for _, profile := range []string{"", "xlarge-handedited", "custom"} {
		if profileIndex(profile) >= 0 {
			t.Fatalf("profile %q must be off-ladder", profile)
		}
		if to, ok := nextProfile(profile); ok {
			t.Fatalf("nextProfile(%q) offered %q; an off-ladder app must not be moved", profile, to)
		}
	}
}

func TestNextProfileTargetsAreAllKnownRequirements(t *testing.T) {
	for _, p := range autoscaleProfileLadder {
		if _, ok := autoscaleProfileRequirements[p]; !ok {
			t.Fatalf("profile %q is on the ladder but has no quota requirement, so quotaHeadroom would silently compare zeroes", p)
		}
	}
	if len(autoscaleProfileRequirements) != len(autoscaleProfileLadder) {
		t.Fatalf("requirements (%d) and ladder (%d) disagree on the set of profiles", len(autoscaleProfileRequirements), len(autoscaleProfileLadder))
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
