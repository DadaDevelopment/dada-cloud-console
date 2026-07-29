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
