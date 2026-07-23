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

func TestAppKey(t *testing.T) {
	envNames := map[string]bool{"prod": true, "staging": true, "dev": true}
	cases := []struct {
		name   string
		labels map[string]string
		dep    string
		want   string
	}{
		{"label wins", map[string]string{"dada.io/app": "profi", "argocd.argoproj.io/instance": "profi-prod"}, "profi-deploy", "profi"},
		{"argocd instance strip env", map[string]string{"argocd.argoproj.io/instance": "cloud-console-prod"}, "dada-cloud-console-backend", "cloud-console"},
		{"argocd instance helm name", map[string]string{"argocd.argoproj.io/instance": "jira-prod"}, "jira-jira-software", "jira"},
		{"argocd non-env suffix kept", map[string]string{"argocd.argoproj.io/instance": "storage-class-longhorn-beget-beget"}, "x", "storage-class-longhorn-beget-beget"},
		{"strip -deploy", nil, "profi-deploy", "profi"},
		{"no suffix (n8n)", nil, "n8n", "n8n"},
		{"unrelated worker", nil, "n8n-worker", "n8n-worker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: c.dep, Labels: c.labels}}
			if got := appKey(d, envNames); got != c.want {
				t.Fatalf("appKey(%q,%v) = %q, want %q", c.dep, c.labels, got, c.want)
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

func TestReplicasOrDefault(t *testing.T) {
	if got := replicasOrDefault(ptr32(3)); got != 3 {
		t.Fatalf("explicit = %d, want 3", got)
	}
	if got := replicasOrDefault(nil); got != 1 {
		t.Fatalf("nil = %d, want default 1", got)
	}
	if got := replicasOrDefault(ptr32(0)); got != 0 {
		t.Fatalf("explicit zero = %d, want 0", got)
	}
}

// appKeyFromMeta must match a StatefulSet/DaemonSet by the same rules as a
// Deployment: label > argocd instance (env-stripped) > name minus -deploy.
func TestAppKeyFromMeta(t *testing.T) {
	envNames := map[string]bool{"prod": true}
	if got := appKeyFromMeta(map[string]string{"argocd.argoproj.io/instance": "mimir-prod"}, "mimir", envNames); got != "mimir" {
		t.Fatalf("sts instance strip = %q, want mimir", got)
	}
	if got := appKeyFromMeta(map[string]string{"dada.io/app": "fluent-bit"}, "fluent-bit", envNames); got != "fluent-bit" {
		t.Fatalf("ds label = %q, want fluent-bit", got)
	}
	if got := appKeyFromMeta(nil, "kubelet-eviction", envNames); got != "kubelet-eviction" {
		t.Fatalf("bare name = %q, want kubelet-eviction", got)
	}
}

// stripEnvSuffix must handle both infra Applications ("<app>-<env>") and tenant
// Applications ("<app>-<env>-<hash>", the ApplicationSet's collision-proofed
// name). An unstrippable label comes back unchanged so callers can detect
// "no app derived" and skip stamping app_name.
func TestStripEnvSuffix(t *testing.T) {
	envNames := map[string]bool{"prod": true}
	cases := []struct{ in, want string }{
		{"cloud-console-prod", "cloud-console"},
		{"nextjs-fhvx20-prod-3e0c7967", "nextjs-fhvx20"},
		{"my-prod-app-prod-3e0c7967", "my-prod-app"},
		{"no-env-here", "no-env-here"},
		{"prod", "prod"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripEnvSuffix(c.in, envNames); got != c.want {
			t.Errorf("stripEnvSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
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
