package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
