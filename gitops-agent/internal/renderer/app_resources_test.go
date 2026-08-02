package renderer

import (
	"strings"
	"testing"
)

func TestResolveResourcesPrefersTheExplicitEnvelope(t *testing.T) {
	got := resolveResources(&AppResources{
		CPURequest:    "500m",
		MemoryRequest: "1Gi",
		CPULimit:      "4",
		MemoryLimit:   "8Gi",
	}, "small")
	if got.Requests["cpu"] != "500m" || got.Requests["memory"] != "1Gi" {
		t.Fatalf("requests = %v, want the explicit envelope", got.Requests)
	}
	if got.Limits["cpu"] != "4" || got.Limits["memory"] != "8Gi" {
		t.Fatalf("limits = %v, want the explicit envelope", got.Limits)
	}
}

func TestResolveResourcesFallsBackToTheProfile(t *testing.T) {
	want := profileResources("large")
	for name, envelope := range map[string]*AppResources{
		"absent":               nil,
		"missing memory limit": {CPURequest: "250m", MemoryRequest: "512Mi", CPULimit: "1"},
		"empty":                {},
	} {
		got := resolveResources(envelope, "large")
		if got.Limits["cpu"] != want.Limits["cpu"] || got.Limits["memory"] != want.Limits["memory"] {
			t.Fatalf("%s: limits = %v, want the profile ladder %v", name, got.Limits, want.Limits)
		}
		if got.Requests["cpu"] != want.Requests["cpu"] || got.Requests["memory"] != want.Requests["memory"] {
			t.Fatalf("%s: requests = %v, want the profile ladder %v", name, got.Requests, want.Requests)
		}
	}
}

func TestRenderAppValuesEmitsResourcesAboveTheProfileCeiling(t *testing.T) {
	out, err := RenderAppValues(AppSpec{
		Name:      "transcoder",
		Namespace: "acme-prod",
		Image:     "repo/transcoder:v1",
		Port:      8080,
		Replicas:  1,
		Profile:   "large",
		Resources: &AppResources{
			CPURequest:    "2",
			MemoryRequest: "4Gi",
			CPULimit:      "8",
			MemoryLimit:   "16Gi",
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"cpu: \"8\"", "memory: 16Gi", "cpu: \"2\"", "memory: 4Gi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered values missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "1Gi") {
		t.Fatalf("the large-profile ceiling leaked into an explicitly sized app:\n%s", out)
	}
}

func TestResolveResourcesCarriesEphemeralStorageThrough(t *testing.T) {
	got := resolveResources(&AppResources{
		CPURequest:       "75m",
		MemoryRequest:    "256Mi",
		CPULimit:         "500m",
		MemoryLimit:      "384Mi",
		EphemeralRequest: "50Mi",
		EphemeralLimit:   "1Gi",
	}, "small")
	if got.Requests["ephemeral-storage"] != "50Mi" || got.Limits["ephemeral-storage"] != "1Gi" {
		t.Fatalf("ephemeral storage dropped: requests %v limits %v", got.Requests, got.Limits)
	}
}

// An app whose cpu/memory envelope is incomplete still falls back to the
// profile, but its ephemeral storage is a value nothing else can supply: losing
// it evicts the container the first time it writes past the node default.
func TestResolveResourcesKeepsEphemeralStorageOnTheProfileFallback(t *testing.T) {
	got := resolveResources(&AppResources{EphemeralLimit: "1Gi"}, "large")
	want := profileResources("large")
	if got.Limits["cpu"] != want.Limits["cpu"] || got.Limits["memory"] != want.Limits["memory"] {
		t.Fatalf("limits = %v, want the profile ladder %v", got.Limits, want.Limits)
	}
	if got.Limits["ephemeral-storage"] != "1Gi" {
		t.Fatalf("ephemeral storage dropped on the profile fallback: %v", got.Limits)
	}
}

func TestResolveResourcesEmitsNoEphemeralKeyWhenTheAppHasNone(t *testing.T) {
	got := resolveResources(nil, "small")
	if _, ok := got.Limits["ephemeral-storage"]; ok {
		t.Fatalf("invented an ephemeral limit: %v", got.Limits)
	}
	if _, ok := got.Requests["ephemeral-storage"]; ok {
		t.Fatalf("invented an ephemeral request: %v", got.Requests)
	}
}
