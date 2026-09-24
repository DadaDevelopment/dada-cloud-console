package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestGitHubDeployment_ClaimListAndReopen(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")

	appName := "gh-deploy-app"
	fullName := "org/gh-deploy-" + uuid.NewString()[:8]
	gitRepoID := uuid.New()
	exec(t, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url, profile)
		 VALUES ($1, $2, $3, $4, 'github', $5, 'https://example.com/x.git', 'small')`,
		gitRepoID, projectID, envID, appName, fullName)
	b := seedBuild(t, pool, gitRepoID, envID, appName, "deadbeef")

	g, err := LoadGitHubEnvironment(ctx, pool, envID, fullName)
	if err != nil {
		t.Fatalf("LoadGitHubEnvironment: %v", err)
	}
	if g.Type != "preview" || g.RepoApps != 1 {
		t.Fatalf("environment = %+v, want type preview and one app on the repo", g)
	}

	ok, err := ClaimGitHubDeployment(ctx, pool, b.ID)
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v; want true", ok, err)
	}
	if ok, _ := ClaimGitHubDeployment(ctx, pool, b.ID); ok {
		t.Fatal("second claim while opening succeeded; a restarted build would open a duplicate deployment")
	}
	if err := SetGitHubDeployment(ctx, pool, b.ID, 4242, 77, GHDeployInProgress); err != nil {
		t.Fatalf("SetGitHubDeployment: %v", err)
	}

	image := "nexus.example.com/p/gh-deploy-app@sha256:abc"
	exec(t, pool, `UPDATE builds SET image_uri = $2 WHERE id = $1`, b.ID, image)
	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "small"}
	if _, err := HandoffDeploy(ctx, pool, b, repo, image, DeployDetection{}, DefaultDomainOpts{}); err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}
	summary, _ := json.Marshal(map[string]any{"image": image, "status": "Ready", "url": "https://gh.example.com"})
	exec(t, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4, NOW())
		 ON CONFLICT (project_id, environment_id, kind, name) DO UPDATE SET summary_json = EXCLUDED.summary_json`,
		projectID, envID, appName, summary)

	open, err := ListOpenGitHubDeployments(ctx, pool, 1000)
	if err != nil {
		t.Fatalf("ListOpenGitHubDeployments: %v", err)
	}
	var got *OpenGitHubDeployment
	for i := range open {
		if open[i].BuildID == b.ID {
			got = &open[i]
		}
	}
	if got == nil {
		t.Fatal("in-progress deployment missing from the open list")
	}
	if got.RepoFullName != fullName || got.AppName != appName || got.ProjectSlug == "" {
		t.Errorf("identity = %q %q %q", got.RepoFullName, got.ProjectSlug, got.AppName)
	}
	if got.DeploymentID != 4242 || got.InstallationID != 77 {
		t.Errorf("ids = %d/%d, want 4242/77", got.DeploymentID, got.InstallationID)
	}
	if !got.HasDeploy || got.DeployImage != image || got.OpStatus == "" {
		t.Errorf("deploy join = has %v image %q op %q", got.HasDeploy, got.DeployImage, got.OpStatus)
	}
	if got.LiveImage != image || got.LiveStatus != "Ready" || got.LiveURL != "https://gh.example.com" {
		t.Errorf("live = %q %q %q", got.LiveImage, got.LiveStatus, got.LiveURL)
	}
	if got.Superseded {
		t.Error("only deployment of the app reads as superseded")
	}

	if ok, _ := MoveGitHubDeployment(ctx, pool, b.ID, GHDeployInProgress, "failure"); !ok {
		t.Fatal("move in_progress -> failure refused")
	}
	if ok, _ := MoveGitHubDeployment(ctx, pool, b.ID, GHDeployInProgress, "success"); ok {
		t.Fatal("second replica moved an already settled deployment")
	}
	if ok, _ := ClaimGitHubDeployment(ctx, pool, b.ID); !ok {
		t.Fatal("a retried build after a failed deployment could not open a new one")
	}
}
