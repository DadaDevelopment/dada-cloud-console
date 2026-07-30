package db

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestHandoffDeploy_StubSnapshotRoutesToCreateApp is the P0 regression test
// for the fonbet-value/pr-7 OOM incident: a DB-owning preview's App snapshot
// used to be a bare {"name","kind":"App"} stub (no "image" key). HandoffDeploy's
// old bare-EXISTS check treated that stub as "app already exists" and routed
// the first build to DeployImageVersion, which never carries git_repos.Profile
// -- so the app deployed on the "small" (256Mi) default and OOMKilled. With the
// "image" key required, the same stub must route to CreateApp instead, which
// does carry repo.Profile.
func TestHandoffDeploy_StubSnapshotRoutesToCreateApp(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "medium")

	appName := "fonbet-value"
	stub, err := json.Marshal(map[string]any{"name": appName, "kind": "App"})
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	exec(t, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Pending', $4, NOW())`,
		projectID, envID, appName, stub)

	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "medium")
	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "medium"}
	b := seedBuild(t, pool, gitRepoID, envID, appName, "abc123")

	opID, err := HandoffDeploy(context.Background(), pool, b, repo, "harbor.example.com/p/fonbet-value@sha256:abc", DeployDetection{}, DefaultDomainOpts{})
	if err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}

	action, payload := readOperation(t, pool, opID)
	if action != "CreateApp" {
		t.Fatalf("action = %q, want %q (a spec-less stub snapshot must NOT count as an existing app)", action, "CreateApp")
	}
	var p createAppPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal CreateApp payload: %v", err)
	}
	if p.Profile != "medium" {
		t.Errorf("CreateApp payload profile = %q, want %q (repo.Profile)", p.Profile, "medium")
	}
}

// TestHandoffDeploy_FullSnapshotRoutesToDeployImageVersion proves the non-stub
// side of the same predicate: once an App snapshot carries a real "image" key
// (e.g. the verbatim parent-App copy gitops-agent's previewOwnerAppSnapshot
// writes for a DB-owning preview), HandoffDeploy correctly treats the app as
// existing and enqueues DeployImageVersion, not a second CreateApp.
func TestHandoffDeploy_FullSnapshotRoutesToDeployImageVersion(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "medium")

	appName := "fonbet-value"
	full, err := json.Marshal(map[string]any{
		"name":    appName,
		"kind":    "App",
		"image":   "harbor.example.com/p/fonbet-value@sha256:old",
		"profile": "medium",
		"port":    float64(8080),
		"status":  "Pending",
	})
	if err != nil {
		t.Fatalf("marshal full snapshot: %v", err)
	}
	exec(t, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Pending', $4, NOW())`,
		projectID, envID, appName, full)

	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "medium")
	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "medium"}
	b := seedBuild(t, pool, gitRepoID, envID, appName, "def456")

	opID, err := HandoffDeploy(context.Background(), pool, b, repo, "harbor.example.com/p/fonbet-value@sha256:new", DeployDetection{}, DefaultDomainOpts{})
	if err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}

	action, payload := readOperation(t, pool, opID)
	if action != "DeployImageVersion" {
		t.Fatalf("action = %q, want %q (a full snapshot with an image IS an existing app)", action, "DeployImageVersion")
	}
	var p deployImageVersionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal DeployImageVersion payload: %v", err)
	}
	if p.Image != "harbor.example.com/p/fonbet-value@sha256:new" {
		t.Errorf("DeployImageVersion payload image = %q, want the new build's image", p.Image)
	}
}

func readOperation(t *testing.T, pool *pgxpool.Pool, opID uuid.UUID) (action string, payload []byte) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT action, payload FROM operations WHERE id = $1`, opID,
	).Scan(&action, &payload); err != nil {
		t.Fatalf("read operation %s: %v", opID, err)
	}
	return action, payload
}

func seedProjectEnv(t *testing.T, pool *pgxpool.Pool, profile string) (projectID, envID uuid.UUID) {
	t.Helper()
	projectID = uuid.New()
	exec(t, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	envID = uuid.New()
	exec(t, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'pr-7-fonbet-value', $3, 'preview')`,
		envID, projectID, "ns-"+envID.String()[:8])
	return projectID, envID
}

func seedGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, profile string) uuid.UUID {
	t.Helper()
	gitRepoID := uuid.New()
	exec(t, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url, profile)
		 VALUES ($1, $2, $3, $4, 'github', 'org/fonbet-value', 'https://example.com/org/fonbet-value.git', $5)`,
		gitRepoID, projectID, envID, appName, profile)
	return gitRepoID
}

func seedBuild(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA string) *Build {
	t.Helper()
	buildID := uuid.New()
	exec(t, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status)
		 VALUES ($1, $2, $3, $4, $5, 'main', 'push', 'success')`,
		buildID, gitRepoID, envID, appName, commitSHA)
	return &Build{ID: buildID, GitRepoID: gitRepoID, EnvironmentID: envID, AppName: appName, CommitSHA: commitSHA, Branch: "main", Trigger: "push"}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	applyMigrations(t, pool)
	return pool
}

func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join("..", "..", "..", "backend", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dada') THEN CREATE ROLE dada; END IF; END $$;`,
	); err != nil {
		t.Fatalf("create role dada: %v", err)
	}
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed exec failed: %v\nsql: %s", err, sql)
	}
}

// TestHandoffDeploy_WorkerRepoGetsNoDefaultHostname pins the second half of the
// upload false-green defect: an archive whose detection found no listening port
// is a bot or a queue consumer, and the platform must not mint it a surrogate
// domain. Default-domain knobs are ON here on purpose — the worker flag has to
// win over them, otherwise the console shows the user a link that can only 502.
func TestHandoffDeploy_WorkerRepoGetsNoDefaultHostname(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")

	appName := "demo-bot"
	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "small")
	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "small", Worker: true}
	b := seedBuild(t, pool, gitRepoID, envID, appName, "bot123")

	opID, err := HandoffDeploy(context.Background(), pool, b, repo,
		"nexus.example.com/p/demo-bot@sha256:abc", DeployDetection{Framework: "python"},
		DefaultDomainOpts{Enabled: true, Base: "dada-tuda.ru"})
	if err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}

	action, payload := readOperation(t, pool, opID)
	if action != "CreateApp" {
		t.Fatalf("action = %q, want CreateApp", action)
	}
	var p createAppPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal CreateApp payload: %v", err)
	}
	if p.DefaultHostname != "" {
		t.Errorf("DefaultHostname = %q, want empty (a worker listens on nothing)", p.DefaultHostname)
	}
	if !p.Worker {
		t.Error("Worker = false, want true (the flag must reach gitops-agent, which records it on the snapshot)")
	}
}
