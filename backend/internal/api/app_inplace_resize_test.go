package api

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestEnvelopeRequirements(t *testing.T) {
	e := resourceEnvelope{
		CPULimit: "2", MemoryLimit: "1Gi",
		CPUReq: "500m", MemoryReq: "512Mi",
		EphemeralLimit: "2Gi", EphemeralReq: "1Gi",
	}
	got, err := e.requirements()
	if err != nil {
		t.Fatalf("requirements: %v", err)
	}
	if q := got.Limits[corev1.ResourceCPU]; q.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("cpu limit = %s", q.String())
	}
	if q := got.Requests[corev1.ResourceMemory]; q.Cmp(resource.MustParse("512Mi")) != 0 {
		t.Fatalf("memory request = %s", q.String())
	}
	if _, ok := got.Limits[corev1.ResourceEphemeralStorage]; ok {
		t.Fatal("ephemeral storage must stay out of an in-place resize; the kubelet rejects the whole patch with it")
	}
}

func TestEnvelopeRequirementsRejectsIncomplete(t *testing.T) {
	if _, err := (resourceEnvelope{CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "100m"}).requirements(); err == nil {
		t.Fatal("an envelope with no memory request must not be patched onto a live pod")
	}
	if _, err := (resourceEnvelope{CPULimit: "banana", MemoryLimit: "1Gi", CPUReq: "100m", MemoryReq: "128Mi"}).requirements(); err == nil {
		t.Fatal("unparseable quantity must be an error")
	}
}

func TestRequirementsMatchIsByValue(t *testing.T) {
	want, err := (resourceEnvelope{CPULimit: "1", MemoryLimit: "1Gi", CPUReq: "100m", MemoryReq: "128Mi"}).requirements()
	if err != nil {
		t.Fatal(err)
	}
	actuated := &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1000m"),
			corev1.ResourceMemory: resource.MustParse("1024Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("0.1"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
	}
	if !requirementsMatch(actuated, want) {
		t.Fatal("1000m/1024Mi is the same size as 1/1Gi and must count as actuated")
	}
	if requirementsMatch(nil, want) {
		t.Fatal("a container with no reported resources has not been resized")
	}

	stale := actuated.DeepCopy()
	stale.Limits[corev1.ResourceMemory] = resource.MustParse("512Mi")
	if requirementsMatch(stale, want) {
		t.Fatal("a pod still on the old memory limit must read as pending, not resized")
	}
}

func TestEnvelopeExceeds(t *testing.T) {
	small := resourceEnvelope{CPULimit: "250m", MemoryLimit: "256Mi", CPUReq: "10m", MemoryReq: "128Mi"}
	grown := resourceEnvelope{CPULimit: "250m", MemoryLimit: "512Mi", CPUReq: "10m", MemoryReq: "128Mi"}

	if !grown.exceeds(small) {
		t.Fatal("a recorded envelope above the live pod is exactly the drift convergence exists to repair")
	}
	if small.exceeds(grown) {
		t.Fatal("convergence must never move an app down; that is the shrink pass's decision to make")
	}
	if grown.exceeds(grown) {
		t.Fatal("an app already at its recorded size must not be patched every tick")
	}
	if (resourceEnvelope{CPULimit: "", MemoryLimit: ""}).exceeds(small) {
		t.Fatal("an unreadable envelope must not trigger a resize")
	}
}
