package worker

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

func routerConfig() *config.Config {
	return &config.Config{
		DBRouterHost:         "pg-router.databases.svc.cluster.local",
		DBRouterPort:         "5432",
		DBRouterDirectShards: []string{"shard-0"},
	}
}

func TestRouterTargetForShardDisabledByDefault(t *testing.T) {
	if _, ok := routerTargetForShard(&config.Config{}, "shard-1"); ok {
		t.Fatal("router must be off when DB_ROUTER_HOST is unset")
	}
}

func TestRouterTargetForShardSkipsPlatformShard(t *testing.T) {
	if _, ok := routerTargetForShard(routerConfig(), "shard-0"); ok {
		t.Fatal("platform shard must keep its direct address")
	}
	target, ok := routerTargetForShard(routerConfig(), "shard-1")
	if !ok || target.Host != "pg-router.databases.svc.cluster.local" || target.Port != "5432" {
		t.Fatalf("tenant shard must route: %+v ok=%v", target, ok)
	}
}

func TestPlanRouterEndpointPatchStampsDirectAddress(t *testing.T) {
	plan := planRouterEndpointPatch(
		map[string][]byte{"endpoint": []byte("postgresql.databases.svc.cluster.local"), "port": []byte("5432")},
		nil,
		routerTarget{Host: "pg-router.databases.svc.cluster.local", Port: "5432"},
	)
	if !plan.Patch {
		t.Fatal("shard address must be rewritten")
	}
	if plan.DirectEndpoint != "postgresql.databases.svc.cluster.local:5432" {
		t.Fatalf("direct address lost: %q", plan.DirectEndpoint)
	}
}

func TestPlanRouterEndpointPatchIdempotent(t *testing.T) {
	plan := planRouterEndpointPatch(
		map[string][]byte{"endpoint": []byte("pg-router.databases.svc.cluster.local"), "port": []byte("5432")},
		map[string]string{dbDirectEndpointAnnotation: "postgresql.databases.svc.cluster.local:5432"},
		routerTarget{Host: "pg-router.databases.svc.cluster.local", Port: "5432"},
	)
	if plan.Patch {
		t.Fatal("an already-routed secret must not be patched again")
	}
}

func TestPlanRouterEndpointPatchKeepsFirstDirectAddress(t *testing.T) {
	plan := planRouterEndpointPatch(
		map[string][]byte{"endpoint": []byte("postgresql.databases.svc.cluster.local"), "port": []byte("5432")},
		map[string]string{dbDirectEndpointAnnotation: "pg-shard-2-postgresql.databases.svc.cluster.local:5432"},
		routerTarget{Host: "pg-router.databases.svc.cluster.local", Port: "5432"},
	)
	if !plan.Patch {
		t.Fatal("a secret re-published with the shard address must be re-routed")
	}
	if plan.DirectEndpoint != "" {
		t.Fatalf("recorded direct address must not be overwritten, got %q", plan.DirectEndpoint)
	}
}

func TestPlanRouterEndpointPatchHonoursOptOut(t *testing.T) {
	plan := planRouterEndpointPatch(
		map[string][]byte{"endpoint": []byte("postgresql.databases.svc.cluster.local"), "port": []byte("5432")},
		map[string]string{dbRouterOptOutAnnotation: "true"},
		routerTarget{Host: "pg-router.databases.svc.cluster.local", Port: "5432"},
	)
	if plan.Patch {
		t.Fatal("opted-out database must keep its direct address")
	}
}

func serviceDatabaseCR(shard, appRef, namespace string) *unstructured.Unstructured {
	spec := map[string]any{"appRef": appRef, "namespace": namespace, "database": "app"}
	if shard != "" {
		spec["shard"] = shard
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "platform.dada-tuda.ru/v1alpha1",
		"kind":       "ServiceDatabaseV2",
		"metadata":   map[string]any{"name": "app-db"},
		"spec":       spec,
	}}
}

func TestSyncRouterEndpointRewritesSecret(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-db-credentials", Namespace: "proj-prod"},
		Data: map[string][]byte{
			"endpoint": []byte("postgresql.databases.svc.cluster.local"),
			"port":     []byte("5432"),
			"username": []byte("svc-app-db"),
			"password": []byte("secret"),
		},
	})
	r := &StatusReconciler{cfg: routerConfig(), client: client}
	r.syncRouterEndpoint(context.Background(), serviceDatabaseCR("shard-1", "app", "proj-prod"))

	got, err := client.CoreV1().Secrets("proj-prod").Get(context.Background(), "app-db-credentials", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(got.Data["endpoint"]) != "pg-router.databases.svc.cluster.local" {
		t.Fatalf("endpoint = %q", got.Data["endpoint"])
	}
	if string(got.Data["password"]) != "secret" || string(got.Data["username"]) != "svc-app-db" {
		t.Fatal("credentials must survive the endpoint rewrite")
	}
	if got.Annotations[dbDirectEndpointAnnotation] != "postgresql.databases.svc.cluster.local:5432" {
		t.Fatalf("direct address annotation = %q", got.Annotations[dbDirectEndpointAnnotation])
	}
}

func TestSyncRouterEndpointLeavesPlatformShardAlone(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "keycloak-db-credentials", Namespace: "k8s-components"},
		Data: map[string][]byte{
			"endpoint": []byte("pg-shard-0-postgresql.databases.svc.cluster.local"),
			"port":     []byte("5432"),
		},
	})
	r := &StatusReconciler{cfg: routerConfig(), client: client}
	r.syncRouterEndpoint(context.Background(), serviceDatabaseCR("shard-0", "keycloak", "k8s-components"))

	got, _ := client.CoreV1().Secrets("k8s-components").Get(context.Background(), "keycloak-db-credentials", metav1.GetOptions{})
	if string(got.Data["endpoint"]) != "pg-shard-0-postgresql.databases.svc.cluster.local" {
		t.Fatalf("platform secret was rewritten: %q", got.Data["endpoint"])
	}
}

func TestSyncRouterEndpointNoopWhenDisabled(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-db-credentials", Namespace: "proj-prod"},
		Data:       map[string][]byte{"endpoint": []byte("postgresql.databases.svc.cluster.local"), "port": []byte("5432")},
	})
	r := &StatusReconciler{cfg: &config.Config{}, client: client}
	r.syncRouterEndpoint(context.Background(), serviceDatabaseCR("", "app", "proj-prod"))

	got, _ := client.CoreV1().Secrets("proj-prod").Get(context.Background(), "app-db-credentials", metav1.GetOptions{})
	if string(got.Data["endpoint"]) != "postgresql.databases.svc.cluster.local" {
		t.Fatalf("secret changed while the router is disabled: %q", got.Data["endpoint"])
	}
}
