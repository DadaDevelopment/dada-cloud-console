package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// srcAppValuesYAML is a values.yaml as a real PublicApi Vite app carries it: a
// non-default servicePort (5173, not the chart's 8080 fallback) plus an ingress
// block the renderer's commonValues struct does not even model — so it can only
// survive a move if the file is carried verbatim, never re-rendered from the
// resource_snapshots.summary_json subset.
const srcAppValuesYAML = `common:
  image:
    name: harbor.dada-tuda.ru/example-project/dada-development-site
    tag: ab67a06d
  servicePort: 5173
  replicas: 1
  useDotEnv: "false"
  ingress:
    enabled: true
    host: development.dada-tuda.ru
  resources:
    requests:
      cpu: 10m
      memory: 128Mi
    limits:
      cpu: 250m
      memory: 256Mi
`

// TestLoadAppValuesVerbatim_PreservesServicePortAndIngress proves the MoveApp
// values fix: the target values.yaml must be the source file byte-for-byte
// (keeping servicePort 5173 and the ingress block), NOT a re-render from the
// partial summary spec — which is what silently dropped servicePort and left a
// moved app 502'ing on :8080. No DB needed; git.Manager.ReadFile is a plain
// os.ReadFile over the local worktree. The partial AppSpec (Port 0) is exactly
// what a move rebuilds from summary_json once the detected port is lost, and the
// rerendered contrast at the end guards against silently reverting to it.
func TestLoadAppValuesVerbatim_PreservesServicePortAndIngress(t *testing.T) {
	base := t.TempDir()
	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://bitbucket.dada-tuda.ru/scm/dada/dada-argo.git",
		Branch:    "develop",
		LocalBase: base,
	})

	srcValuesPath := renderer.AppHelmValuesGitPath("example-project", "prod", "dada-development-site")
	full := filepath.Join(mgr.LocalPath(), srcValuesPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(full, []byte(srcAppValuesYAML), 0o644); err != nil {
		t.Fatalf("write src values.yaml: %v", err)
	}

	partial := renderer.AppSpec{
		Name:      "dada-development-site",
		Image:     "harbor.dada-tuda.ru/example-project/dada-development-site:ab67a06d",
		Framework: "vite",
		Port:      0,
	}

	got, err := loadAppValuesVerbatim(mgr, srcValuesPath, partial)
	if err != nil {
		t.Fatalf("loadAppValuesVerbatim: %v", err)
	}

	if got != srcAppValuesYAML {
		t.Errorf("values.yaml not carried verbatim:\n--- got ---\n%s\n--- want ---\n%s", got, srcAppValuesYAML)
	}
	for _, want := range []string{"servicePort: 5173", "ingress:", "host: development.dada-tuda.ru"} {
		if !strings.Contains(got, want) {
			t.Errorf("carried values.yaml missing %q", want)
		}
	}

	rerendered, err := renderer.RenderAppValues(partial)
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	if strings.Contains(rerendered, "servicePort: 5173") || strings.Contains(rerendered, "ingress:") {
		t.Fatalf("re-render unexpectedly kept servicePort/ingress; contrast is meaningless:\n%s", rerendered)
	}
}

// TestLoadAppValuesVerbatim_FallsBackWhenMissing covers the pre-values.yaml app:
// when the source file is genuinely absent, the carry falls back to rendering
// from the spec rather than erroring.
func TestLoadAppValuesVerbatim_FallsBackWhenMissing(t *testing.T) {
	base := t.TempDir()
	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://bitbucket.dada-tuda.ru/scm/dada/dada-argo.git",
		LocalBase: base,
	})

	fallback := renderer.AppSpec{
		Name:    "legacy",
		Image:   "harbor.dada-tuda.ru/p/legacy:v1",
		Port:    8000,
		Profile: "medium",
	}
	got, err := loadAppValuesVerbatim(mgr, "does/not/exist/values.yaml", fallback)
	if err != nil {
		t.Fatalf("loadAppValuesVerbatim fallback: %v", err)
	}
	if !strings.Contains(got, "servicePort: 8000") {
		t.Errorf("fallback render missing servicePort 8000:\n%s", got)
	}
}

// TestRepointMovedAppSnapshots_DuplicateChildIsIdempotent exercises the repoint
// fix against a real Postgres: the target already holds a same-named child
// snapshot (an Ingress/web left from a prior partial move). The colliding UPDATE
// must roll back inside its savepoint and drop the src duplicate, WITHOUT
// poisoning the transaction — so every non-colliding sibling still repoints and
// nothing is scattered across projects. Before the fix this returned "current
// transaction is aborted" and left the app's snapshots split between projects.
// Skipped unless TEST_DATABASE_URL is set (CI runs Docker-less). The source is
// seeded with the App row plus two owned children; the target is pre-seeded with
// the colliding Ingress/web.
func TestRepointMovedAppSnapshots_DuplicateChildIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	srcProj, srcEnv := seedMoveProjectEnv(t, ctx, pool, "src")
	dstProj, dstEnv := seedMoveProjectEnv(t, ctx, pool, "dst")

	const app = "web"
	seedSnapshot(t, ctx, pool, srcProj, srcEnv, "App", app, `{}`)
	seedSnapshot(t, ctx, pool, srcProj, srcEnv, "PublicApi", app, `{"app_ref":"web"}`)
	seedSnapshot(t, ctx, pool, srcProj, srcEnv, "Ingress", app, `{"app_ref":"web"}`)
	dupID := seedSnapshot(t, ctx, pool, dstProj, dstEnv, "Ingress", app, `{"app_ref":"web"}`)

	w := &DBWatcher{pool: pool}
	if err := w.repointMovedAppSnapshots(ctx, srcProj, srcEnv, dstProj, dstEnv, app); err != nil {
		t.Fatalf("repoint returned error (tx should not abort on a duplicate child): %v", err)
	}

	if n := countSnapshots(t, ctx, pool, srcProj, srcEnv, app); n != 0 {
		t.Errorf("source still holds %d snapshot(s) for %q; want 0 (snapshots scattered)", n, app)
	}
	for _, kind := range []string{"App", "PublicApi", "Ingress"} {
		if n := countSnapshotsKind(t, ctx, pool, dstProj, dstEnv, kind, app); n != 1 {
			t.Errorf("target holds %d %s/%s; want exactly 1", n, kind, app)
		}
	}
	if id := ingressID(t, ctx, pool, dstProj, dstEnv, app); id != dupID {
		t.Errorf("surviving Ingress id = %s; want the pre-existing target row %s", id, dupID)
	}
}

func seedMoveProjectEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tag string) (projectID, envID uuid.UUID) {
	t.Helper()
	projectID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, $3)`,
		projectID, tag+"-"+projectID.String()[:8], "Move "+tag)
	envID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		envID, projectID, "ns-"+envID.String()[:8])
	return projectID, envID
}

func seedSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, kind, name, summaryJSON string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (id, project_id, environment_id, kind, name, summary_json)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		id, projectID, envID, kind, name, summaryJSON)
	return id
}

func countSnapshots(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND name = $3`,
		projectID, envID, name,
	).Scan(&n); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

func countSnapshotsKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, kind, name string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = $3 AND name = $4`,
		projectID, envID, kind, name,
	).Scan(&n); err != nil {
		t.Fatalf("count snapshots by kind: %v", err)
	}
	return n
}

func ingressID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'Ingress' AND name = $3`,
		projectID, envID, name,
	).Scan(&id); err != nil {
		t.Fatalf("ingress id: %v", err)
	}
	return id
}

// resourcesValuesTwoPublicApi is an app's resources.values.yaml carrying two
// cluster-scoped PublicApi objects (surrogate + custom domain) plus an unrelated
// Secret — the shape MoveApp enumerates to know which live objects it must
// re-adopt onto the target Argo app before pruning the source.
const resourcesValuesTwoPublicApi = `manifests:
  - apiVersion: platform.dada-tuda.ru/v1alpha1
    kind: PublicApi
    metadata:
      name: dada-development-site
    spec:
      appRef: dada-development-site
  - apiVersion: platform.dada-tuda.ru/v1alpha1
    kind: PublicApi
    metadata:
      name: dada-development-site-custom
    spec:
      appRef: dada-development-site
      domain: development.dada-tuda.ru
  - apiVersion: v1
    kind: Secret
    metadata:
      name: dada-development-site-env
`

// publicApiGVR mirrors pgvr("publicapis"): the cluster-scoped custom-domain
// object the move must hand off.
var publicApiGVR = schema.GroupVersionResource{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Resource: "publicapis"}

// TestNamesOfKind_ReturnsEveryPublicApi proves the enumerator MoveApp relies on:
// it returns the metadata.name of every PublicApi in the file (both objects) and
// none of the other kinds, so the pre-adopt loop patches exactly the cluster-
// scoped resources that would otherwise be pruned.
func TestNamesOfKind_ReturnsEveryPublicApi(t *testing.T) {
	rv, err := renderer.ParseResourcesValues(resourcesValuesTwoPublicApi)
	if err != nil {
		t.Fatalf("parse resources.values.yaml: %v", err)
	}
	got := rv.NamesOfKind("PublicApi")
	want := []string{"dada-development-site", "dada-development-site-custom"}
	if len(got) != len(want) {
		t.Fatalf("NamesOfKind(PublicApi) = %v; want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("NamesOfKind(PublicApi)[%d] = %q; want %q", i, got[i], w)
		}
	}
	if n := rv.NamesOfKind("Secret"); len(n) != 1 || n[0] != "dada-development-site-env" {
		t.Errorf("NamesOfKind(Secret) = %v; want [dada-development-site-env]", n)
	}
	if n := rv.NamesOfKind("ServiceDatabaseV2"); len(n) != 0 {
		t.Errorf("NamesOfKind(absent kind) = %v; want empty", n)
	}
}

// trackingID builds the argocd.argoproj.io/tracking-id value exactly as ArgoCD
// stamps it on a cluster-scoped PublicApi under resourceTrackingMethod=
// annotation+label (verified live), so tests assert against the real format
// rather than a guess.
func trackingID(instance, namespace, name string) string {
	return fmt.Sprintf("%s:platform.dada-tuda.ru/PublicApi:%s/%s", instance, namespace, name)
}

// newPublicApi builds a live cluster-scoped PublicApi as ArgoCD leaves it under
// annotation+label tracking: named, carrying BOTH the instance label and the
// tracking-id annotation for the owning app in the given namespace.
func newPublicApi(name, instance, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Kind: "PublicApi"})
	u.SetName(name)
	u.SetLabels(map[string]string{argoInstanceLabel: instance})
	u.SetAnnotations(map[string]string{argoTrackingIDAnnotation: trackingID(instance, namespace, name)})
	return u
}

// TestPreAdoptClusterScopedResources_FlipsBothMarkersToTarget is the core fix:
// before the source folder is pruned, every cluster-scoped PublicApi the app
// carries must have BOTH ArgoCD ownership markers re-stamped to the target app —
// the instance label AND the authoritative tracking-id annotation. Under
// annotation+label tracking ArgoCD keys prune ownership off the annotation, so a
// label-only flip would leave the source app still owning (and pruning) the
// shared object, 502'ing the live domain. Both objects (surrogate + custom
// domain) must flip, and the annotation must equal the exact value the target app
// will itself compute (target instance + target namespace). Fake dynamic client
// stands in for the cluster.
func TestPreAdoptClusterScopedResources_FlipsBothMarkersToTarget(t *testing.T) {
	ctx := context.Background()
	const (
		srcInstance  = "dada-development-site-prod-aaaa1111"
		dstInstance  = "dada-development-site-prod-b9addbae"
		srcNamespace = "example-project-prod"
		dstNamespace = "internal-prod"
	)

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{publicApiGVR: "PublicApiList"},
		newPublicApi("dada-development-site", srcInstance, srcNamespace),
		newPublicApi("dada-development-site-custom", srcInstance, srcNamespace),
	)
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: dyn}}
	rv, err := renderer.ParseResourcesValues(resourcesValuesTwoPublicApi)
	if err != nil {
		t.Fatalf("parse resources.values.yaml: %v", err)
	}

	w.preAdoptClusterScopedResources(ctx, rv, srcInstance, dstInstance, dstNamespace)

	for _, name := range []string{"dada-development-site", "dada-development-site-custom"} {
		live, err := dyn.Resource(publicApiGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get %q after pre-adopt: %v", name, err)
		}
		if got := live.GetLabels()[argoInstanceLabel]; got != dstInstance {
			t.Errorf("%q instance label = %q; want target %q", name, got, dstInstance)
		}
		wantTID := trackingID(dstInstance, dstNamespace, name)
		if got := live.GetAnnotations()[argoTrackingIDAnnotation]; got != wantTID {
			t.Errorf("%q tracking-id = %q; want target %q (source prune keys off this)", name, got, wantTID)
		}
	}
}

// TestPreAdoptClusterScopedResources_Idempotent covers the re-drive: an object
// already fully owned by the target (both markers, from a prior partial move) is
// left untouched, and a PublicApi named in git but not yet created in the cluster
// is a best-effort skip that neither errors nor blocks the surviving objects from
// flipping.
func TestPreAdoptClusterScopedResources_Idempotent(t *testing.T) {
	ctx := context.Background()
	const (
		srcInstance  = "app-prod-aaaa1111"
		dstInstance  = "app-prod-b9addbae"
		dstNamespace = "internal-prod"
	)

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{publicApiGVR: "PublicApiList"},
		newPublicApi("dada-development-site", dstInstance, dstNamespace),
	)
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: dyn}}
	rv, err := renderer.ParseResourcesValues(resourcesValuesTwoPublicApi)
	if err != nil {
		t.Fatalf("parse resources.values.yaml: %v", err)
	}

	w.preAdoptClusterScopedResources(ctx, rv, srcInstance, dstInstance, dstNamespace)

	live, err := dyn.Resource(publicApiGVR).Get(ctx, "dada-development-site", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after idempotent pre-adopt: %v", err)
	}
	if got := live.GetLabels()[argoInstanceLabel]; got != dstInstance {
		t.Errorf("already-adopted object instance = %q; want unchanged %q", got, dstInstance)
	}
	if got := live.GetAnnotations()[argoTrackingIDAnnotation]; got != trackingID(dstInstance, dstNamespace, "dada-development-site") {
		t.Errorf("already-adopted object tracking-id = %q; want unchanged", got)
	}
}

// TestPreAdoptClusterScopedResources_NoClientIsNoop guards the local-dev path:
// with no in-cluster client the pre-adopt must return silently, never panic, so a
// move on a workstation still completes (degrading only to the pre-fix blip in
// prod).
func TestPreAdoptClusterScopedResources_NoClientIsNoop(t *testing.T) {
	rv, err := renderer.ParseResourcesValues(resourcesValuesTwoPublicApi)
	if err != nil {
		t.Fatalf("parse resources.values.yaml: %v", err)
	}
	(&DBWatcher{}).preAdoptClusterScopedResources(context.Background(), rv, "src", "dst", "internal-prod")
	(&DBWatcher{clients: &dadak8s.Clients{}}).preAdoptClusterScopedResources(context.Background(), rv, "src", "dst", "internal-prod")
}
