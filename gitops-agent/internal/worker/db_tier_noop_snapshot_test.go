package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// seedTierProjectEnv inserts the minimum a tier operation needs to run: a
// project, an owning environment, and a resource_snapshots row for a
// ServiceDatabaseV2 whose recorded tier is deliberately stale relative to what
// git will carry, reproducing a snapshot that never got refreshed by a prior
// no-op. Returns the ids the test asserts against.
func seedTierProjectEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dbName, staleTier string) (projectID, environmentID uuid.UUID) {
	t.Helper()

	projectID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	environmentID = uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		environmentID, projectID, "ns-"+environmentID.String()[:8])

	summary, err := json.Marshal(map[string]any{"tier": staleTier})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, summary_json)
		 VALUES ($1, $2, 'ServiceDatabaseV2', $3, $4)`,
		projectID, environmentID, dbName, summary)

	return projectID, environmentID
}

// seedSetDatabaseTierOperation inserts a Processing SetDatabaseTier operation,
// the shape doSetDatabaseTier is dispatched with in production.
func seedSetDatabaseTierOperation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, dbName, appRef, tier string) db.Operation {
	t.Helper()

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at DESC LIMIT 1`).Scan(&actorID); err != nil {
		actorID = uuid.New()
		exec(t, ctx, pool,
			`INSERT INTO users (id, username, email, password_hash, display_name)
			 VALUES ($1, $2, $3, 'x', 'Test')`,
			actorID, "u-"+actorID.String(), actorID.String()+"@test.local")
	}

	opID := uuid.New()
	payload, err := json.Marshal(map[string]string{"name": dbName, "app_ref": appRef, "tier": tier})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, 'SetDatabaseTier', 'ServiceDatabaseV2', $5, 'Processing', $6)`,
		opID, actorID, projectID, environmentID, dbName, payload)

	return db.Operation{
		ID:            opID,
		ProjectID:     projectID,
		EnvironmentID: &environmentID,
		Action:        "SetDatabaseTier",
		Payload:       payload,
	}
}

// newTierManifestRepo seeds a bare remote carrying one resources.values.yaml
// with a single ServiceDatabaseV2 manifest already at wantTier, and returns a
// Manager cloned from it -- a git state that already agrees with the operation
// before doSetDatabaseTier ever runs, matching the case that only had the tier
// applied once (by a prior operation) and now must be a no-op forever after.
func newTierManifestRepo(t *testing.T, valuesPath, dbName, appRef, wantTier string) *git.Manager {
	t.Helper()

	rv, err := renderer.ParseResourcesValues("")
	if err != nil {
		t.Fatalf("parse empty resources.values.yaml: %v", err)
	}
	manifest := "apiVersion: platform.dada-tuda.ru/v1alpha1\n" +
		"kind: ServiceDatabaseV2\n" +
		"metadata:\n" +
		"  name: " + dbName + "\n" +
		"spec:\n" +
		"  appRef: " + appRef + "\n" +
		"  database: mlflow\n" +
		"  tier: " + wantTier + "\n"
	if err := rv.Upsert(manifest); err != nil {
		t.Fatalf("upsert manifest: %v", err)
	}
	content, err := rv.Marshal()
	if err != nil {
		t.Fatalf("marshal resources.values.yaml: %v", err)
	}

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
	if _, err := seedRepo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	historyRewriteWriteAndAdd(t, seedDir, wt, valuesPath, content)
	if _, err := wt.Commit("seed resources.values.yaml", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    historyRewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("clone: %v", err)
	}
	return mgr
}

// TestDoSetDatabaseTier_NoopRefreshesSnapshot is the regression for the tier
// reconcile loop measured on prod: SetDatabaseTier operations 242 -> 252 and
// SendBuildNotification audit rows 66 -> 132 in under 24h.
//
// A SetDatabaseTier operation whose git manifest already carries the wanted
// tier takes the MarkNoop branch and ends Committed, which is correctly NOT
// "in flight" for enqueueDatabaseTier's dedupe -- Committed is a legitimate
// terminal success. But the reconciler's own "does this need to change"
// verdict is read from resource_snapshots.summary_json.tier, not from git, and
// that field was only ever refreshed on the branch that actually produced a
// commit. A snapshot that drifted stale keeps disagreeing with git forever:
// every tick re-decides "must change", enqueues, finds git already correct,
// ends Committed again, and never touches the field the decision was made
// from. That is the loop.
//
// The fix must write the observed tier back into resource_snapshots on the
// no-op branch too, so the next tick's read matches git and the mismatch that
// drives the loop stops existing.
func TestDoSetDatabaseTier_NoopRefreshesSnapshot(t *testing.T) {
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

	dbName := "mlflow-v2"
	appRef := "app1"
	wantTier := "internal"

	projectID, environmentID := seedTierProjectEnv(t, ctx, pool, dbName, "unlimited")

	var projectName string
	if err := pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName); err != nil {
		t.Fatalf("read project name: %v", err)
	}

	valuesPath := renderer.AppResourcesValuesGitPath(projectName, "prod", appRef)
	mgr := newTierManifestRepo(t, valuesPath, dbName, appRef, wantTier)

	op := seedSetDatabaseTierOperation(t, ctx, pool, projectID, environmentID, dbName, appRef, wantTier)

	w := newTestDBWatcher(pool)
	w.cfg = &config.Config{DefaultRepoURL: mgr.RepoURL()}
	w.managers = map[string]*git.Manager{mgr.RepoURL(): mgr}

	if err := w.doSetDatabaseTier(ctx, op); err != nil {
		t.Fatalf("doSetDatabaseTier: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM operations WHERE id = $1`, op.ID).Scan(&status); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if status != "Committed" {
		t.Fatalf("a no-op tier flip must still end terminally, got status %q", status)
	}

	var summaryRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots WHERE environment_id = $1 AND kind = 'ServiceDatabaseV2' AND name = $2`,
		environmentID, dbName,
	).Scan(&summaryRaw); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var summary struct {
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	if summary.Tier != wantTier {
		t.Fatalf("resource_snapshots.summary_json.tier = %q after a no-op, want %q: the reconciler will re-decide the same change forever", summary.Tier, wantTier)
	}
}
