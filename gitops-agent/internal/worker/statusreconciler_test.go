package worker

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ptr32(v int32) *int32 { return &v }

func TestLivePhase(t *testing.T) {
	cases := []struct {
		name    string
		desired int32
		ready   int32
		want    string
	}{
		{"all ready", 2, 2, "Ready"},
		{"over ready", 1, 2, "Ready"},
		{"partial", 2, 1, "Pending"},
		{"none ready", 1, 0, "Pending"},
		{"scaled to zero", 0, 0, "Stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := livePhase(&liveApp{desired: c.desired, ready: c.ready})
			if got != c.want {
				t.Fatalf("livePhase(desired=%d ready=%d) = %q, want %q", c.desired, c.ready, got, c.want)
			}
		})
	}
}

func TestDesiredReplicas(t *testing.T) {
	if got := desiredReplicas(&appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: ptr32(3)}}); got != 3 {
		t.Fatalf("explicit replicas = %d, want 3", got)
	}
	if got := desiredReplicas(&appsv1.Deployment{}); got != 1 {
		t.Fatalf("nil replicas = %d, want default 1", got)
	}
}

func TestPrimaryImage(t *testing.T) {
	d := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "fluent-container", Image: "fluent/fluent-bit:latest"},
			{Name: "profi", Image: "nexus.dada-tuda.ru/dada/profi:master-1.0.0-231"},
		}},
	}}}
	if got := primaryImage(d); got != "nexus.dada-tuda.ru/dada/profi:master-1.0.0-231" {
		t.Fatalf("primaryImage skipped sidecar wrong: %q", got)
	}

	empty := &appsv1.Deployment{}
	if got := primaryImage(empty); got != "" {
		t.Fatalf("primaryImage(empty) = %q, want empty", got)
	}
}
