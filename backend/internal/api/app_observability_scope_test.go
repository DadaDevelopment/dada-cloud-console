package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func observabilityScopePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping app observability scope DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAdoptedApp reproduces the shape an adopted ArgoCD app has (ADR-013): the
// snapshot is filed under an environment whose namespace is NOT where the pods
// run, and the status reconciler recorded the real namespaces and images.
func seedAdoptedApp(t *testing.T, pool *pgxpool.Pool, summary string) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	appName := "adopted-" + uuid.NewString()[:8]

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, 'org-obs') RETURNING id`,
		"obs-scope-"+uuid.NewString()[:8],
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, "platform-prod-"+uuid.NewString()[:6],
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4::jsonb)`,
		projectID, envID, appName, summary,
	); err != nil {
		t.Fatalf("seed app snapshot: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})
	return projectID, envID, appName
}

// TestK8sAppNamespacesIncludesObservedNamespaces is the log-search fix: before
// it, an app whose pods run outside its environment's namespace searched the
// wrong index prefix and returned zero hits while logging constantly.
func TestK8sAppNamespacesIncludesObservedNamespaces(t *testing.T) {
	pool := observabilityScopePool(t)
	projectID, envID, appName := seedAdoptedApp(t, pool,
		`{"namespaces": ["argocd-prod"], "images": ["ghcr.io/dada/app:1"]}`)

	var envNamespace string
	if err := pool.QueryRow(context.Background(),
		`SELECT namespace FROM environments WHERE id = $1`, envID).Scan(&envNamespace); err != nil {
		t.Fatalf("read env namespace: %v", err)
	}

	h := &Handler{pool: pool}
	got, err := h.k8sAppNamespaces(context.Background(), projectID, appName)
	if err != nil {
		t.Fatalf("k8sAppNamespaces: %v", err)
	}
	seen := map[string]bool{}
	for _, ns := range got {
		seen[ns] = true
	}
	if !seen["argocd-prod"] {
		t.Fatalf("k8sAppNamespaces = %v, want the observed namespace argocd-prod", got)
	}
	if !seen[envNamespace] {
		t.Fatalf("k8sAppNamespaces = %v, want the environment namespace %q kept", got, envNamespace)
	}
}

// TestK8sAppNamespacesToleratesMissingSet keeps snapshots written before the
// reconciler reported namespaces working: the environment namespace alone.
func TestK8sAppNamespacesToleratesMissingSet(t *testing.T) {
	pool := observabilityScopePool(t)
	projectID, _, appName := seedAdoptedApp(t, pool, `{"image": "ghcr.io/dada/app:1"}`)

	got, err := h0(pool).k8sAppNamespaces(context.Background(), projectID, appName)
	if err != nil {
		t.Fatalf("k8sAppNamespaces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("k8sAppNamespaces = %v, want exactly the environment namespace", got)
	}
}

// TestK8sAppNamespacesToleratesScalarSet proves the jsonb guard: a snapshot
// whose "namespaces" key is not an array must not error the whole query.
func TestK8sAppNamespacesToleratesScalarSet(t *testing.T) {
	pool := observabilityScopePool(t)
	projectID, _, appName := seedAdoptedApp(t, pool, `{"namespaces": "argocd-prod"}`)

	got, err := h0(pool).k8sAppNamespaces(context.Background(), projectID, appName)
	if err != nil {
		t.Fatalf("k8sAppNamespaces on a scalar namespaces key: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("k8sAppNamespaces = %v, want exactly the environment namespace", got)
	}
}

// TestAppMetricsScopeQueryReadsObservedSets exercises the exact SELECT
// GetAppMetrics runs, including the jsonb_typeof guard, so a malformed set can
// never turn the metrics endpoint into a 500.
func TestAppMetricsScopeQueryReadsObservedSets(t *testing.T) {
	pool := observabilityScopePool(t)
	projectID, envID, appName := seedAdoptedApp(t, pool,
		`{"image": "ghcr.io/dada/app:1", "namespaces": ["argocd-prod"], "images": ["ghcr.io/dada/app:1", "ghcr.io/dada/gw:2"]}`)

	var runtime, namespace, image string
	var liveNamespaces, liveImages []string
	err := pool.QueryRow(context.Background(),
		`SELECT e.runtime, e.namespace, COALESCE(rs.summary_json->>'image', ''),
		        ARRAY(SELECT jsonb_array_elements_text(
		          CASE WHEN jsonb_typeof(rs.summary_json->'namespaces') = 'array'
		               THEN rs.summary_json->'namespaces' ELSE '[]'::jsonb END)),
		        ARRAY(SELECT jsonb_array_elements_text(
		          CASE WHEN jsonb_typeof(rs.summary_json->'images') = 'array'
		               THEN rs.summary_json->'images' ELSE '[]'::jsonb END))
		 FROM environments e
		 JOIN resource_snapshots rs
		   ON rs.environment_id = e.id AND rs.kind = 'App' AND rs.name = $3
		 WHERE e.id = $2 AND rs.project_id = $1`,
		projectID, envID, appName,
	).Scan(&runtime, &namespace, &image, &liveNamespaces, &liveImages)
	if err != nil {
		t.Fatalf("metrics scope query: %v", err)
	}
	if len(liveNamespaces) != 1 || liveNamespaces[0] != "argocd-prod" {
		t.Fatalf("observed namespaces = %v, want [argocd-prod]", liveNamespaces)
	}
	if len(liveImages) != 2 {
		t.Fatalf("observed images = %v, want both images", liveImages)
	}
	if got := mergeNonEmpty(liveNamespaces, namespace); len(got) != 2 {
		t.Fatalf("merged namespaces = %v, want the observed one plus the environment one", got)
	}
}

func h0(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }
