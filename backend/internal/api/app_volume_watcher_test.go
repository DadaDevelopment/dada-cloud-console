package api

import (
	"math"
	"testing"

	"github.com/dada-tuda/console/backend/internal/prometheus"
)

func sample(namespace, pvc string, ratio float64) prometheus.Sample {
	return prometheus.Sample{
		Metric: map[string]string{"namespace": namespace, "persistentvolumeclaim": pvc},
		Point:  prometheus.Point{V: ratio},
	}
}

func TestParseVolumeUsageSamples(t *testing.T) {
	samples := []prometheus.Sample{
		sample("acme-prod", "fonbet-value-pvc", 0.995),
		{Metric: map[string]string{"persistentvolumeclaim": "no-namespace-pvc"}, Point: prometheus.Point{V: 0.5}},
		{Metric: map[string]string{"namespace": "acme-prod"}, Point: prometheus.Point{V: 0.5}},
	}
	out := parseVolumeUsageSamples(samples)
	if len(out) != 1 {
		t.Fatalf("expected 1 sample after dropping unlabeled ones, got %d: %+v", len(out), out)
	}
	if out[0].Namespace != "acme-prod" || out[0].PVCName != "fonbet-value-pvc" || out[0].Ratio != 0.995 {
		t.Fatalf("unexpected sample: %+v", out[0])
	}
}

func TestParseVolumeUsageSamplesDropsNonFiniteRatio(t *testing.T) {
	samples := []prometheus.Sample{
		sample("acme-prod", "zero-capacity-pvc", math.Inf(1)),
		sample("acme-prod", "nan-pvc", math.NaN()),
		sample("acme-prod", "ok-pvc", 0.5),
	}
	out := parseVolumeUsageSamples(samples)
	if len(out) != 1 || out[0].PVCName != "ok-pvc" {
		t.Fatalf("expected only the finite-ratio sample to survive, got %+v", out)
	}
}

func TestOverThreshold(t *testing.T) {
	samples := []volumeUsageSample{
		{Namespace: "acme-prod", PVCName: "hot-pvc", Ratio: 0.995},
		{Namespace: "acme-prod", PVCName: "warm-pvc", Ratio: 0.85},
		{Namespace: "acme-prod", PVCName: "cold-pvc", Ratio: 0.2},
	}
	out := overThreshold(samples, appVolumeAlertThreshold)
	if len(out) != 2 {
		t.Fatalf("expected 2 samples at/above threshold, got %d: %+v", len(out), out)
	}
	names := map[string]bool{}
	for _, s := range out {
		names[s.PVCName] = true
	}
	if !names["hot-pvc"] || !names["warm-pvc"] {
		t.Fatalf("expected hot-pvc and warm-pvc, got %+v", out)
	}
}

func TestOverThresholdEmptyOnNoMatches(t *testing.T) {
	samples := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "cold-pvc", Ratio: 0.1}}
	out := overThreshold(samples, appVolumeAlertThreshold)
	if len(out) != 0 {
		t.Fatalf("expected no matches, got %+v", out)
	}
}

// TestHotVolumeSamplesBytesOnly is the pre-inode-tracking case: a PVC hot on
// bytes with no inode signal at all is still reported, tagged bytes.
func TestHotVolumeSamplesBytesOnly(t *testing.T) {
	bytes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "hot-pvc", Ratio: 0.9}}
	out := hotVolumeSamples(bytes, nil, appVolumeAlertThreshold)
	if len(out) != 1 || out[0].Kind != ratioKindBytes || out[0].Ratio != 0.9 {
		t.Fatalf("expected one bytes-tagged alert at 0.9, got %+v", out)
	}
}

// TestHotVolumeSamplesInodesOnly is the fonbet-value bug this whole change
// exists to fix: a PVC whose byte ratio never crosses threshold but whose
// inode ratio does must still fire, tagged inodes so the owner isn't told to
// enlarge a disk that has room.
func TestHotVolumeSamplesInodesOnly(t *testing.T) {
	bytes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "fonbet-value-pvc", Ratio: 0.73}}
	inodes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "fonbet-value-pvc", Ratio: 1.0}}
	out := hotVolumeSamples(bytes, inodes, appVolumeAlertThreshold)
	if len(out) != 1 {
		t.Fatalf("expected exactly one alert for the one hot pvc, got %+v", out)
	}
	if out[0].Kind != ratioKindInodes || out[0].Ratio != 1.0 {
		t.Fatalf("expected inodes-tagged alert at ratio 1.0, got %+v", out[0])
	}
}

// TestHotVolumeSamplesInodesOnlyNoByteSample covers a PVC that never shows up
// in the byte-hot list at all (byte ratio far under threshold) but is
// inode-hot: it must still be reported via the second loop in
// hotVolumeSamples, not silently dropped because it was never "seen" by the
// byte pass.
func TestHotVolumeSamplesInodesOnlyNoByteSample(t *testing.T) {
	bytes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "quiet-pvc", Ratio: 0.1}}
	inodes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "quiet-pvc", Ratio: 0.99}}
	out := hotVolumeSamples(bytes, inodes, appVolumeAlertThreshold)
	if len(out) != 1 || out[0].Kind != ratioKindInodes {
		t.Fatalf("expected one inodes-tagged alert for the inode-hot pvc, got %+v", out)
	}
}

// TestHotVolumeSamplesBothHotPrioritizesInodes locks in the deliberate
// asymmetry documented on hotVolumeSamples: when a PVC is hot on BOTH
// dimensions, the alert must name inodes, never bytes, because "increase the
// disk" is the wrong fix for inode exhaustion and offering it anyway would
// send the owner down a dead end.
func TestHotVolumeSamplesBothHotPrioritizesInodes(t *testing.T) {
	bytes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "both-hot-pvc", Ratio: 0.92}}
	inodes := []volumeUsageSample{{Namespace: "acme-prod", PVCName: "both-hot-pvc", Ratio: 0.97}}
	out := hotVolumeSamples(bytes, inodes, appVolumeAlertThreshold)
	if len(out) != 1 || out[0].Kind != ratioKindInodes || out[0].Ratio != 0.97 {
		t.Fatalf("expected inodes to win with ratio 0.97, got %+v", out)
	}
}
