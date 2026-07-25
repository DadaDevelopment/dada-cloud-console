package worker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSyncAppFile_DoesNotClobberRichSeedWithBareStub is the regression test for
// the live e2e failure: doCreatePreviewEnv (preview.go) seeds the preview's App
// resource_snapshot verbatim from the parent app (image + volume carried
// through, see previewOwnerAppSnapshot), then ensureAppExists (dbwatcher.go)
// writes a brand-new app.yaml stub to git in the SAME operation. When the git
// watcher later syncs that commit, syncAppFile used to unconditionally
// UpsertSnapshot a bare {status:"Unknown", git_sha, git_message, app_name}
// payload with the commit's own timestamp -- which lands a few milliseconds
// after the seed's time.Now(), so UpsertSnapshot's last_synced_at LWW guard let
// it through and wiped image/volume. build-agent's HandoffDeploy then read a
// summary_json with no "image" key, routed to CreateApp instead of
// DeployImageVersion, and the app's worker/volume config was dropped.
//
// syncAppFile's payload is always bare (it never parses app.yaml for
// image/volume, see the handler body), so a bare git-stub sync must never be
// allowed to overwrite a snapshot that already carries an "image" key,
// regardless of how the two timestamps compare. This test uses a commit time
// strictly after the seed time to reproduce the actual prod ordering:
// ensureAppExists commits the stub a beat after the seed's time.Now().
func TestSyncAppFile_DoesNotClobberRichSeedWithBareStub(t *testing.T) {
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

	projectSlug := "proj-" + uuid.New().String()[:8]
	envSlug := "pr-42"
	appName := "web"

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, projectSlug)

	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, $3, $4, 'preview')`,
		envID, projectID, envSlug, "ns-"+envSlug)

	seedTime := time.Now()
	richSummary := map[string]any{
		"image":    "registry.example.com/web:sha-abc123",
		"port":     float64(8080),
		"replicas": float64(1),
		"profile":  "medium",
		"volume":   map[string]any{"size": "5Gi", "mountPath": "/data"},
		"status":   "Pending",
	}
	richSummaryJSON, err := json.Marshal(richSummary)
	if err != nil {
		t.Fatalf("marshal rich summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Pending', $4, $5)`,
		projectID, envID, appName, richSummaryJSON, seedTime)

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/org/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})

	filePath := "clusters/beget-prod/projects/" + projectSlug + "/environments/" + envSlug + "/apps/" + appName + "/app.yaml"
	c := git.Commit{
		SHA:     "1111111111111111111111111111111111111111",
		Message: "Create preview env " + envSlug,
		Author:  "gitops-agent",
		Email:   "bot@dada",
		When:    seedTime.Add(1 * time.Second),
		Files:   []string{filePath},
	}

	w := &GitWatcher{pool: pool}
	w.syncAppFile(ctx, mgr, filePath, projectSlug, envSlug, appName, c)

	var gotSummary []byte
	var gotPhase string
	if err := pool.QueryRow(ctx,
		`SELECT phase, summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&gotPhase, &gotSummary); err != nil {
		t.Fatalf("query snapshot after sync: %v", err)
	}

	var cur map[string]any
	if err := json.Unmarshal(gotSummary, &cur); err != nil {
		t.Fatalf("unmarshal snapshot summary: %v", err)
	}
	if image, _ := cur["image"].(string); image != "registry.example.com/web:sha-abc123" {
		t.Errorf("image = %q, want the rich seed's image preserved -- bare git-stub sync clobbered it", image)
	}
	if _, hasVolume := cur["volume"]; !hasVolume {
		t.Errorf("volume key missing after sync, want it preserved: %+v", cur)
	}
}

// TestSyncAppFile_WritesSnapshotWhenNoneExists preserves the ordinary case: a
// brand new app.yaml with no prior resource_snapshots row must still create one
// (an app that has never been touched by the API/preview seeding at all).
func TestSyncAppFile_WritesSnapshotWhenNoneExists(t *testing.T) {
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

	projectSlug := "proj-" + uuid.New().String()[:8]
	envSlug := "prod"
	appName := "brand-new"

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, projectSlug)

	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, $3, $4, 'prod')`,
		envID, projectID, envSlug, "ns-"+envSlug)

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/org/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})

	filePath := "clusters/beget-prod/projects/" + projectSlug + "/environments/" + envSlug + "/apps/" + appName + "/app.yaml"
	c := git.Commit{
		SHA:     "2222222222222222222222222222222222222222",
		Message: "add app",
		Author:  "someone",
		Email:   "someone@dada",
		When:    time.Now(),
		Files:   []string{filePath},
	}

	w := &GitWatcher{pool: pool}
	w.syncAppFile(ctx, mgr, filePath, projectSlug, envSlug, appName, c)

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		t.Fatalf("count snapshot: %v", err)
	}
	if count != 1 {
		t.Fatalf("resource_snapshots rows for new app = %d, want 1", count)
	}
}

// TestSyncAppFile_BareUpdatesStillFollowLWW proves the ordinary LWW path still
// applies between two bare (imageless) snapshots: a newer bare sync must still
// win over an older one, so ordinary git_sha/git_message tracking keeps working.
func TestSyncAppFile_BareUpdatesStillFollowLWW(t *testing.T) {
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

	projectSlug := "proj-" + uuid.New().String()[:8]
	envSlug := "prod"
	appName := "watcher-only"

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, projectSlug)

	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, $3, $4, 'prod')`,
		envID, projectID, envSlug, "ns-"+envSlug)

	baseTime := time.Now()
	bareSummary := map[string]any{
		"git_sha":     "old-sha",
		"git_message": "old",
		"app_name":    appName,
		"status":      "Unknown",
		"message":     "Synced from git",
	}
	bareSummaryJSON, err := json.Marshal(bareSummary)
	if err != nil {
		t.Fatalf("marshal bare summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Unknown', $4, $5)`,
		projectID, envID, appName, bareSummaryJSON, baseTime)

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/org/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})

	filePath := "clusters/beget-prod/projects/" + projectSlug + "/environments/" + envSlug + "/apps/" + appName + "/app.yaml"
	c := git.Commit{
		SHA:     "3333333333333333333333333333333333333333",
		Message: "new commit",
		Author:  "someone",
		Email:   "someone@dada",
		When:    baseTime.Add(1 * time.Second),
		Files:   []string{filePath},
	}

	w := &GitWatcher{pool: pool}
	w.syncAppFile(ctx, mgr, filePath, projectSlug, envSlug, appName, c)

	var gotSummary []byte
	if err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&gotSummary); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	var cur map[string]any
	if err := json.Unmarshal(gotSummary, &cur); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if sha, _ := cur["git_sha"].(string); sha != c.SHA {
		t.Errorf("git_sha = %q, want the newer commit's sha %q (bare-to-bare LWW must still advance)", sha, c.SHA)
	}
}
