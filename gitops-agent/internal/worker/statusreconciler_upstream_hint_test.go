package worker

import (
	"context"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func svc(namespace, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

// TestUpstreamServiceEnvsSkipsListWhenNothingIsContested keeps the tie-break
// free for the normal estate: without a duplicate snapshot name there is no tie
// to break, and a cluster-wide Service LIST every 30s would be pure cost.
func TestUpstreamServiceEnvsSkipsListWhenNothingIsContested(t *testing.T) {
	client := fake.NewSimpleClientset(svc("platform-prod", "n8n"))
	r := &StatusReconciler{client: client, ambiguous: map[string]bool{}}

	got := r.upstreamServiceEnvs(context.Background(), map[string][]uuid.UUID{"n8n": {uuid.New()}}, map[string]uuid.UUID{"platform-prod": uuid.New()})
	if got != nil {
		t.Fatalf("uncontested estate must not build a hint map, got %v", got)
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("uncontested estate must not list services, actions: %v", client.Actions())
	}
}

// TestUpstreamServiceEnvsResolvesOnlyUniqueNames: a Service name that lives in
// one console-known namespace names its environment; a name present in two
// environments, or in a namespace no environment owns, proves nothing and must
// leave the ambiguity unresolved rather than guess.
func TestUpstreamServiceEnvsResolvesOnlyUniqueNames(t *testing.T) {
	platform, internal := uuid.New(), uuid.New()
	client := fake.NewSimpleClientset(
		svc("platform-prod", "n8n"),
		svc("internal-prod", "svod-landing-service"),
		svc("platform-prod", "shared"),
		svc("internal-prod", "shared"),
		svc("kube-system", "kube-dns"),
	)
	r := &StatusReconciler{client: client, ambiguous: map[string]bool{}}

	got := r.upstreamServiceEnvs(context.Background(),
		map[string][]uuid.UUID{"n8n": {platform, internal}},
		map[string]uuid.UUID{"platform-prod": platform, "internal-prod": internal},
	)

	if got["n8n"] != platform {
		t.Fatalf("n8n hint = %v, want %v (its Service lives in platform-prod)", got["n8n"], platform)
	}
	if got["svod-landing-service"] != internal {
		t.Fatalf("svod-landing-service hint = %v, want %v", got["svod-landing-service"], internal)
	}
	if _, ok := got["shared"]; ok {
		t.Fatal("a Service name present in two environments must not produce a hint")
	}
	if _, ok := got["kube-dns"]; ok {
		t.Fatal("a Service outside every console environment must not produce a hint")
	}
}
