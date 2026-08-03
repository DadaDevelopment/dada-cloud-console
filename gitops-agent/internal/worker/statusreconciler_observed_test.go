package worker

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPrimaryContainerSkipsLoggingSidecar(t *testing.T) {
	cs := []corev1.Container{
		{Name: "fluent-container", Image: "fluent/fluent-bit:2"},
		{Name: "app", Image: "ghcr.io/dada/app:1"},
	}
	got := primaryContainer(cs)
	if got == nil || got.Name != "app" {
		t.Fatalf("primaryContainer = %v, want the app container", got)
	}
	if primaryContainer(nil) != nil {
		t.Fatal("primaryContainer(nil) = non-nil, want nil")
	}
}

func TestObservedResourcesSumsOverDesiredPods(t *testing.T) {
	la := &liveApp{}
	c := corev1.Container{
		Name: "app",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
	}
	addScaled(&la.cpuRequest, c.Resources.Requests[corev1.ResourceCPU], 2)
	addScaled(&la.cpuLimit, c.Resources.Limits[corev1.ResourceCPU], 2)
	addScaled(&la.memRequest, c.Resources.Requests[corev1.ResourceMemory], 2)
	addScaled(&la.memLimit, c.Resources.Limits[corev1.ResourceMemory], 2)

	got := observedResources(la)
	want := map[string]string{
		"cpu_request":    "200m",
		"cpu_limit":      "1",
		"memory_request": "256Mi",
		"memory_limit":   "1Gi",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("observedResources()[%q] = %q, want %q (full: %v)", k, got[k], v, got)
		}
	}
}

func TestObservedResourcesOmitsUnsetFields(t *testing.T) {
	la := &liveApp{}
	addScaled(&la.cpuRequest, resource.MustParse("250m"), 1)

	got := observedResources(la)
	if got["cpu_request"] != "250m" {
		t.Fatalf("cpu_request = %q, want 250m", got["cpu_request"])
	}
	if _, ok := got["cpu_limit"]; ok {
		t.Fatalf("cpu_limit present for a container with no limit: %v", got)
	}
	if len(observedResources(&liveApp{})) != 0 {
		t.Fatal("observedResources of an empty envelope is non-empty")
	}
}

func TestSortedKeysIsStable(t *testing.T) {
	got := sortedKeys(map[string]bool{"platform-prod": true, "argocd-prod": true})
	if len(got) != 2 || got[0] != "argocd-prod" || got[1] != "platform-prod" {
		t.Fatalf("sortedKeys = %v, want [argocd-prod platform-prod]", got)
	}
	if len(sortedKeys(nil)) != 0 {
		t.Fatal("sortedKeys(nil) is non-empty")
	}
}

// TestDNSRecordLiveRequiresFQDN guards the promotion path from firing on a
// PublicApi that declares no DNS at all: without an fqdn there is nothing to
// verify, so the composite's own Pending verdict must stand.
func TestDNSRecordLiveRequiresFQDN(t *testing.T) {
	r := &StatusReconciler{dnsVerdicts: map[string]dnsVerdict{}}
	cr := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"dns": map[string]any{"enabled": true}},
	}}
	if r.dnsRecordLive(t.Context(), cr) {
		t.Fatal("dnsRecordLive = true for a PublicApi with no fqdn")
	}

	disabled := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"dns": map[string]any{"enabled": false, "fqdn": "x.dada-tuda.ru"}},
	}}
	if r.dnsRecordLive(t.Context(), disabled) {
		t.Fatal("dnsRecordLive = true for a PublicApi with DNS disabled")
	}
}

// TestDNSRecordLiveUsesCache proves a cached verdict short-circuits the lookup:
// the reconciler ticks every 30s against dozens of stuck endpoints, and each
// one must not turn into a resolver call per tick.
func TestDNSRecordLiveUsesCache(t *testing.T) {
	r := &StatusReconciler{dnsVerdicts: map[string]dnsVerdict{
		"cached.dada-tuda.ru": {live: true, at: time.Now()},
	}}
	cr := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"dns": map[string]any{
			"enabled": true,
			"fqdn":    "cached.dada-tuda.ru",
			"target":  "203.0.113.1",
		}},
	}}
	if !r.dnsRecordLive(t.Context(), cr) {
		t.Fatal("dnsRecordLive ignored a fresh cached verdict")
	}
}
