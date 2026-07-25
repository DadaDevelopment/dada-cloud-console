package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestRerenderServiceDatabaseForMoveSetsDstNamespace(t *testing.T) {
	var src serviceDatabaseManifest
	src.Metadata.Name = "n8n"
	src.Spec.AppRef = "n8n"
	src.Spec.Database = "n8n"
	src.Spec.Backup.Enabled = true
	src.Spec.Backup.Frequency = "daily"
	src.Spec.Backup.Retention = "7d"

	got, err := rerenderServiceDatabaseForMove(src, "platform", "prod", "platform-prod", "op-123")
	if err != nil {
		t.Fatalf("rerender: %v", err)
	}
	for _, want := range []string{
		"kind: ServiceDatabaseV2",
		"namespace: platform-prod",
		"appRef: n8n",
		"database: n8n",
		"dada.io/project: platform",
		"dada.io/environment: prod",
		"dada.io/operation: op-123",
		"retention: 7d",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered DB missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "example-project") {
		t.Errorf("rendered DB still carries source namespace\n---\n%s", got)
	}
}

func TestRerenderServiceDatabaseForMoveRejectsUnnamed(t *testing.T) {
	var src serviceDatabaseManifest
	if _, err := rerenderServiceDatabaseForMove(src, "platform", "prod", "platform-prod", "op-1"); err == nil {
		t.Fatal("expected error for source ServiceDatabaseV2 with no metadata.name")
	}
}

const resourcesValuesWithDB = `manifests:
    - apiVersion: platform.dada-tuda.ru/v1alpha1
      kind: PublicApi
      metadata:
        name: n8n
        namespace: example-project-prod
      spec:
        upstream:
          serviceName: n8n-service
          servicePort: 5678
    - apiVersion: platform.dada-tuda.ru/v1alpha1
      kind: ServiceDatabaseV2
      metadata:
        name: n8n
        labels:
          dada.io/project: example-project
          dada.io/environment: prod
          dada.io/operation: old-op
      spec:
        appRef: n8n
        namespace: example-project-prod
        engine: postgresql
        database: n8n
        backup:
          enabled: true
          frequency: daily
          retention: 7d
`

func TestRepointResourcesValuesDBReplacesInPlace(t *testing.T) {
	rv, err := renderer.ParseResourcesValues(resourcesValuesWithDB)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := repointResourcesValuesDB(rv, "platform", "prod", "platform-prod", "op-123"); err != nil {
		t.Fatalf("repoint: %v", err)
	}
	out, err := rv.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		"kind: ServiceDatabaseV2",
		"namespace: platform-prod",
		"database: n8n",
		"retention: 7d",
		"dada.io/project: platform",
		"dada.io/operation: op-123",
		"kind: PublicApi",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("repointed values missing %q\n---\n%s", want, out)
		}
	}
	if n := strings.Count(out, "example-project-prod"); n != 1 {
		t.Errorf("expected only the untouched PublicApi to keep the source namespace (repoint is DB-only), got %d occurrences\n---\n%s", n, out)
	}
	if strings.Contains(out, "dada.io/operation: old-op") {
		t.Errorf("repointed DB kept the source operation id\n---\n%s", out)
	}
	if n := strings.Count(out, "kind: ServiceDatabaseV2"); n != 1 {
		t.Errorf("expected exactly one ServiceDatabaseV2 after repoint, got %d\n---\n%s", n, out)
	}
}

func TestMoveVolumeAllowedTracksFlag(t *testing.T) {
	if moveVolumeAllowed(false) {
		t.Error("moveVolumeAllowed(false) = true; a volume-bearing app must abort the move when MOVE_VOLUME_ENABLED is off")
	}
	if !moveVolumeAllowed(true) {
		t.Error("moveVolumeAllowed(true) = false; the flag must unlock the enabled path (which still aborts, with a distinct message, until Phase 2 copy ships)")
	}
}

func TestRepointResourcesValuesDBNoDatabaseIsNoop(t *testing.T) {
	const noDB = `manifests:
    - apiVersion: platform.dada-tuda.ru/v1alpha1
      kind: PublicApi
      metadata:
        name: web
        namespace: example-project-prod
      spec:
        upstream:
          serviceName: web-service
          servicePort: 8080
`
	rv, err := renderer.ParseResourcesValues(noDB)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := repointResourcesValuesDB(rv, "platform", "prod", "platform-prod", "op-1"); err != nil {
		t.Fatalf("repoint no-op: %v", err)
	}
	out, err := rv.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(out, "ServiceDatabaseV2") {
		t.Errorf("no-op repoint invented a database\n---\n%s", out)
	}
	if !strings.Contains(out, "kind: PublicApi") {
		t.Errorf("no-op repoint dropped the PublicApi\n---\n%s", out)
	}
}

// serviceDatabaseGVR mirrors pgvr("servicedatabasesv2"). ServiceDatabaseV2 is a
// cluster-scoped composite (its serving namespace lives in spec.namespace, per
// discovery.go), so a stateful move must hand it off to the target Argo app just
// like a PublicApi — otherwise the source-folder prune drops the live DB
// composite and its credentials secret before the target reconciles.
var serviceDatabaseGVR = schema.GroupVersionResource{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Resource: "servicedatabasesv2"}

func serviceDBTrackingID(instance, namespace, name string) string {
	return fmt.Sprintf("%s:platform.dada-tuda.ru/ServiceDatabaseV2:%s/%s", instance, namespace, name)
}

func newServiceDatabaseV2(name, instance, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Kind: "ServiceDatabaseV2"})
	u.SetName(name)
	u.SetLabels(map[string]string{argoInstanceLabel: instance})
	u.SetAnnotations(map[string]string{argoTrackingIDAnnotation: serviceDBTrackingID(instance, namespace, name)})
	return u
}

// TestPreAdoptClusterScopedResources_HandsOffServiceDatabaseV2 is the stateful-move
// analogue of the PublicApi handoff: before the source folder is pruned, the
// cluster-scoped ServiceDatabaseV2 the app carries must have BOTH ArgoCD ownership
// markers re-stamped to the target app, and the tracking-id annotation must equal
// the exact value the target app will compute (target instance + target
// namespace, since the DB was re-pointed to spec.namespace=dstNamespace). Without
// this the source prune tears down the live DB composite and its credentials
// secret. The DB fixture reuses resourcesValuesWithDB (ServiceDatabaseV2 "n8n").
func TestPreAdoptClusterScopedResources_HandsOffServiceDatabaseV2(t *testing.T) {
	ctx := context.Background()
	const (
		srcInstance  = "n8n-prod-aaaa1111"
		dstInstance  = "n8n-prod-b9addbae"
		srcNamespace = "example-project-prod"
		dstNamespace = "platform-prod"
	)

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			publicApiGVR:       "PublicApiList",
			serviceDatabaseGVR: "ServiceDatabaseV2List",
		},
	)
	if _, err := dyn.Resource(serviceDatabaseGVR).Create(ctx, newServiceDatabaseV2("n8n", srcInstance, srcNamespace), metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed ServiceDatabaseV2 under its real (irregular) plural GVR: %v", err)
	}
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: dyn}}
	rv, err := renderer.ParseResourcesValues(resourcesValuesWithDB)
	if err != nil {
		t.Fatalf("parse resources.values.yaml: %v", err)
	}

	w.preAdoptClusterScopedResources(ctx, rv, srcInstance, dstInstance, dstNamespace)

	live, err := dyn.Resource(serviceDatabaseGVR).Get(ctx, "n8n", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ServiceDatabaseV2 after pre-adopt: %v", err)
	}
	if got := live.GetLabels()[argoInstanceLabel]; got != dstInstance {
		t.Errorf("DB instance label = %q; want target %q", got, dstInstance)
	}
	wantTID := serviceDBTrackingID(dstInstance, dstNamespace, "n8n")
	if got := live.GetAnnotations()[argoTrackingIDAnnotation]; got != wantTID {
		t.Errorf("DB tracking-id = %q; want target %q (source prune keys off this)", got, wantTID)
	}
}
