package worker

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newOrphanGCTestPool connects to TEST_DATABASE_URL and resets the schema, or
// skips the test if the variable is not set (same convention as
// TestWipeProjectRows_NoOrphans in delete_project_test.go).
func newOrphanGCTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	applyMigrations(t, ctx, pool)
	return pool
}

func seedOrphanGCProjectEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, envName, namespace string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID := uuid.New()
	envID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, $2)`,
		projectID, slug); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, $3, $4, 'prod')`,
		envID, projectID, envName, namespace); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

func seedOrphanGCAppSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, phase string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'App', $3, $4)`,
		projectID, envID, name, phase); err != nil {
		t.Fatalf("seed app snapshot: %v", err)
	}
}

func infraDeployment(name, namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: name + ":latest"}}},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

// TestReconcile_AttributesLiveWorkloadToOrphanedOnlySnapshot is the
// falsification case for the orphan-GC one-way door: jenkins lives in
// devops-tools (not any env's own namespace), so the only way to attribute
// its live Deployment to a snapshot row is by name via the DB lookup. Once
// that row's phase flips to Orphaned (the observed 2026-08-08/09 incident:
// jenkins, nexus, portainer, neo4j under project "platform"), the exclusive
// lookup returns zero candidates for "jenkins" forever and the live pod can
// never re-claim its row — the next orphan-GC sweep purges it for real.
//
// Before the fix, reconcile() has no fallback once appEnvs (the exclusive,
// Orphaned-filtered map) misses, so this assertion fails: the live jenkins
// Deployment is never attributed to any snapshot key and the GC's "still
// alive" guard stays empty for it.
func TestReconcile_AttributesLiveWorkloadToOrphanedOnlySnapshot(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	platformProject, platformEnv := seedOrphanGCProjectEnv(t, ctx, pool, "platform", "prod", "platform-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, platformProject, platformEnv, "jenkins", "Orphaned")

	client := fake.NewSimpleClientset(infraDeployment("jenkins", "devops-tools"))
	r := &StatusReconciler{pool: pool, cfg: &config.Config{}, client: client}

	live := r.reconcile(ctx)

	want := snapKey{env: platformEnv, app: "jenkins"}
	if !live[want] {
		t.Fatalf("reconcile() live set = %v, want %v marked live — jenkins pod is running but its only snapshot row (phase=Orphaned) can never be reattached, so the GC will purge a live app", live, want)
	}
}

// TestReconcile_LiveTwinStillWinsOverOrphanedRow is the regression guard for
// the fallback added above: it must never let an Orphaned twin steal
// attribution away from a live row of the same name (the 2026-08-04
// incident this file's exclusive map already protects at the DB-query
// layer). The fallback in resolveEnv must only fire when the exclusive map
// found zero candidates, not when it already resolved unambiguously.
func TestReconcile_LiveTwinStillWinsOverOrphanedRow(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	newProject, newEnv := seedOrphanGCProjectEnv(t, ctx, pool, "observability", "prod", "observability-prod")
	oldProject, oldEnv := seedOrphanGCProjectEnv(t, ctx, pool, "platform2", "prod", "platform2-prod")
	seedOrphanGCAppSnapshot(t, ctx, pool, newProject, newEnv, "mimir", "Unknown")
	seedOrphanGCAppSnapshot(t, ctx, pool, oldProject, oldEnv, "mimir", "Orphaned")

	client := fake.NewSimpleClientset(infraDeployment("mimir", "some-infra-namespace"))
	r := &StatusReconciler{pool: pool, cfg: &config.Config{}, client: client}

	live := r.reconcile(ctx)

	wantLive := snapKey{env: newEnv, app: "mimir"}
	wantNotLive := snapKey{env: oldEnv, app: "mimir"}
	if !live[wantLive] {
		t.Fatalf("reconcile() live set = %v, want the live twin %v marked live", live, wantLive)
	}
	if live[wantNotLive] {
		t.Fatalf("reconcile() live set = %v, the Orphaned twin %v must not be revived by the fallback", live, wantNotLive)
	}
}
