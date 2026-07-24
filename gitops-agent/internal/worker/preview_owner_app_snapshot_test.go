package worker

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPreviewOwnerAppSnapshot_CopiesParentProfile is the P0 regression test for
// the fonbet-value/pr-7 OOM incident: a DB-owning preview's owner-app snapshot
// must carry the real profile from the parent app's own App snapshot (here
// "medium"), not the old bare {"name","kind":"App"} stub that left "profile"
// absent and made doDeployImageVersion default to "small" (256Mi) ->
// OOMKilled. It also proves argo_name is NEVER copied verbatim: reusing the
// parent's argo_name would make the preview's App CR request the same ArgoCD
// Application the parent already owns.
func TestPreviewOwnerAppSnapshot_CopiesParentProfile(t *testing.T) {
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

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	parentID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		parentID, projectID, "ns-parent-"+parentID.String()[:8])

	parentArgoName := "fonbet-value-prod-aabbccdd"
	parentSummary := map[string]any{
		"image":     "registry.example.com/fonbet-value:sha-abc123",
		"framework": "nodejs",
		"port":      float64(8080),
		"replicas":  float64(1),
		"profile":   "medium",
		"status":    "Ready",
		"argo_name": parentArgoName,
	}
	parentSummaryJSON, err := json.Marshal(parentSummary)
	if err != nil {
		t.Fatalf("marshal parent summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', 'fonbet-value', 'Ready', $3, NOW())`,
		projectID, parentID, parentSummaryJSON)

	w := &DBWatcher{pool: pool, cfg: &config.Config{EncryptionKey: testEncryptionKey}}
	got, err := w.previewOwnerAppSnapshot(ctx, projectID, &parentID, nil, "fonbet-value", "pr-7-fonbet-value")
	if err != nil {
		t.Fatalf("previewOwnerAppSnapshot: %v", err)
	}

	var cur map[string]any
	if err := json.Unmarshal(got, &cur); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if profile, _ := cur["profile"].(string); profile != "medium" {
		t.Errorf("profile = %q, want %q (must inherit from parent App snapshot, not default to \"small\")", profile, "medium")
	}
	if image, _ := cur["image"].(string); image != "registry.example.com/fonbet-value:sha-abc123" {
		t.Errorf("image = %q, want the parent's image (verbatim copy)", image)
	}
	if status, _ := cur["status"].(string); status != "Pending" {
		t.Errorf("status = %q, want %q (preview app has not deployed yet)", status, "Pending")
	}
	argoName, _ := cur["argo_name"].(string)
	if argoName == "" {
		t.Errorf("argo_name is empty, want a preview-scoped name")
	}
	if argoName == parentArgoName {
		t.Errorf("argo_name = %q, reused the PARENT's argo_name verbatim -- this makes the preview App CR request the parent's own ArgoCD Application (collision)", argoName)
	}
}

// TestPreviewOwnerAppSnapshot_WatcherShapedParentFallsBackToRepoProfile covers
// the case where the parent app was only ever synced by the git-watcher (never
// deployed through CreateApp/DeployImageVersion): its App snapshot has no
// "profile" and no "image" key at all (see gitwatcher.go's syncAppFile, which
// writes only git_sha/git_message/git_author/app_name/status/message). The
// preview snapshot must still get a real profile, coalesced from
// git_repos.profile rather than silently defaulting to "small" downstream.
func TestPreviewOwnerAppSnapshot_WatcherShapedParentFallsBackToRepoProfile(t *testing.T) {
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

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	parentID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		parentID, projectID, "ns-parent-"+parentID.String()[:8])

	watcherSummary := map[string]any{
		"git_sha":     "abc123",
		"git_message": "init",
		"git_author":  "someone",
		"app_name":    "fonbet-value",
		"status":      "Unknown",
		"message":     "Synced from git",
	}
	watcherSummaryJSON, err := json.Marshal(watcherSummary)
	if err != nil {
		t.Fatalf("marshal watcher summary: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', 'fonbet-value', 'Unknown', $3, NOW())`,
		projectID, parentID, watcherSummaryJSON)

	gitRepoID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url, profile)
		 VALUES ($1, $2, $3, 'fonbet-value', 'github', 'org/fonbet-value', 'https://example.com/org/fonbet-value.git', 'medium')`,
		gitRepoID, projectID, parentID)

	w := &DBWatcher{pool: pool, cfg: &config.Config{EncryptionKey: testEncryptionKey}}
	got, err := w.previewOwnerAppSnapshot(ctx, projectID, &parentID, &gitRepoID, "fonbet-value", "pr-7-fonbet-value")
	if err != nil {
		t.Fatalf("previewOwnerAppSnapshot: %v", err)
	}

	var cur map[string]any
	if err := json.Unmarshal(got, &cur); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if profile, _ := cur["profile"].(string); profile != "medium" {
		t.Errorf("profile = %q, want %q (coalesced from git_repos.profile)", profile, "medium")
	}
	if _, hasImage := cur["image"]; hasImage {
		t.Errorf("watcher-shaped copy unexpectedly gained an \"image\" key: %+v", cur)
	}
}

// TestPreviewOwnerAppSnapshot_NoParentSnapshotFallsBackToStub proves the
// fallback path: when the parent has no App snapshot at all (first-ever app,
// or no parent env), the old bare stub is used unchanged.
func TestPreviewOwnerAppSnapshot_NoParentSnapshotFallsBackToStub(t *testing.T) {
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

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])

	parentID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		parentID, projectID, "ns-parent-"+parentID.String()[:8])

	w := &DBWatcher{pool: pool, cfg: &config.Config{EncryptionKey: testEncryptionKey}}
	got, err := w.previewOwnerAppSnapshot(ctx, projectID, &parentID, nil, "brand-new-app", "pr-1-brand-new-app")
	if err != nil {
		t.Fatalf("previewOwnerAppSnapshot: %v", err)
	}
	var cur map[string]any
	if err := json.Unmarshal(got, &cur); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(cur) != 2 {
		t.Errorf("stub fallback = %+v, want exactly {name, kind}", cur)
	}
	if name, _ := cur["name"].(string); name != "brand-new-app" {
		t.Errorf("name = %q, want %q", name, "brand-new-app")
	}
	if kind, _ := cur["kind"].(string); kind != "App" {
		t.Errorf("kind = %q, want %q", kind, "App")
	}
}
