package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

const noManagedDBTestBranch = "master"

func seedNoManagedDBRemote(t *testing.T) string {
	t.Helper()
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	seedDir := filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("add seed file: %v", err)
	}
	if _, err := wt.Commit("seed", &gogit.CommitOptions{
		Author: &object.Signature{Name: "seed", Email: "seed@test", When: time.Now()},
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := seedRepo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs: []gogitconfig.RefSpec{
			gogitconfig.RefSpec("refs/heads/" + noManagedDBTestBranch + ":refs/heads/" + noManagedDBTestBranch),
		},
	}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	return remoteDir
}

// TestDoCreatePreviewEnv_WorkerVolumeAppWithoutManagedDB_SeedsAppSnapshot is the
// regression test for the field-loss bug: a parent app with worker:true and a
// volume, but NO managed ServiceDatabaseV2, used to get no pre-seeded App
// resource_snapshot at all (previewOwnerAppSnapshot / ensureAppExists only ran
// inside the `len(previewDBs) > 0` branch). Its first build then hit
// build-agent's HandoffDeploy, whose `summary_json ? 'image'` existence check
// came back false, routing it to CreateApp with build-agent's bare
// Name/Image/Framework/Port/Replicas/Profile payload -- dropping
// worker/volume/workload_type on the floor and rendering the preview as a plain
// stateless Deployment.
//
// After the fix, doCreatePreviewEnv pre-seeds the preview's App snapshot from
// the parent's own App snapshot unconditionally (whenever a parent snapshot
// exists), independent of previewDBs. This proves both halves: the preview's
// own App resource_snapshot carries worker/volume/workload_type, and the exact
// predicate HandoffDeploy runs now evaluates true (DeployImageVersion path, not
// CreateApp).
func TestDoCreatePreviewEnv_WorkerVolumeAppWithoutManagedDB_SeedsAppSnapshot(t *testing.T) {
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

	remoteDir := seedNoManagedDBRemote(t)

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	parentID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		parentID, projectID, "ns-parent-"+parentID.String()[:8])

	appName := "worker-app"
	parentSummary := map[string]any{
		"image":         "registry.example.com/worker-app:sha-abc123",
		"framework":     "nodejs",
		"port":          float64(8080),
		"replicas":      float64(1),
		"profile":       "medium",
		"status":        "Ready",
		"argo_name":     "worker-app-prod-aabbccdd",
		"worker":        true,
		"workload_type": "Worker",
		"volume": map[string]any{
			"path": "/data",
			"size": "5Gi",
		},
	}
	parentSummaryJSON, err := json.Marshal(parentSummary)
	if err != nil {
		t.Fatalf("marshal parent summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4, NOW())`,
		projectID, parentID, appName, parentSummaryJSON)

	cfg := &config.Config{
		EncryptionKey:  testEncryptionKey,
		DefaultRepoURL: remoteDir,
		DefaultBranch:  noManagedDBTestBranch,
		BotName:        "test-bot",
		BotEmail:       "test-bot@test",
		PreviewEnvTTL:  time.Hour,
	}
	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    noManagedDBTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	w := &DBWatcher{
		pool: pool,
		cfg:  cfg,
		managers: map[string]*git.Manager{
			remoteDir: mgr,
		},
	}

	prNumber := 9
	envName := "pr-9-worker-app"
	namespace := "ns-preview-" + uuid.New().String()[:8]
	payload, err := json.Marshal(map[string]any{
		"env_name":      envName,
		"namespace":     namespace,
		"pr_number":     prNumber,
		"head_branch":   "feature/worker",
		"parent_env_id": parentID.String(),
		"app_name":      appName,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	op := db.Operation{
		ID:        uuid.New(),
		ProjectID: projectID,
		Action:    "CreatePreviewEnv",
		Payload:   payload,
	}

	if err := w.doCreatePreviewEnv(ctx, op); err != nil {
		t.Fatalf("doCreatePreviewEnv: %v", err)
	}

	var previewEnvID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM environments WHERE project_id = $1 AND name = $2`,
		projectID, envName).Scan(&previewEnvID); err != nil {
		t.Fatalf("find preview environment: %v", err)
	}

	var summaryRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, previewEnvID, appName).Scan(&summaryRaw); err != nil {
		t.Fatalf("preview App snapshot was not seeded (worker/volume app with no managed DB): %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(summaryRaw, &got); err != nil {
		t.Fatalf("unmarshal preview App snapshot: %v", err)
	}
	if image, _ := got["image"].(string); image != "registry.example.com/worker-app:sha-abc123" {
		t.Errorf("image = %q, want the parent's image (field loss)", image)
	}
	if worker, _ := got["worker"].(bool); !worker {
		t.Errorf("worker = %v, want true (field loss)", got["worker"])
	}
	if workloadType, _ := got["workload_type"].(string); workloadType != "Worker" {
		t.Errorf("workload_type = %q, want %q (field loss)", workloadType, "Worker")
	}
	if _, hasVolume := got["volume"]; !hasVolume {
		t.Errorf("volume key missing entirely (field loss): %+v", got)
	}

	var appExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM resource_snapshots
			WHERE environment_id = $1 AND kind = 'App' AND name = $2
			  AND summary_json ? 'image'
		)
	`, previewEnvID, appName).Scan(&appExists); err != nil {
		t.Fatalf("run HandoffDeploy predicate: %v", err)
	}
	if !appExists {
		t.Errorf("HandoffDeploy's appExists predicate = false, want true -- first preview build would fall onto build-agent's bare CreateApp fallback and drop worker/volume/workload_type")
	}
}
