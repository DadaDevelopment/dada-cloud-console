package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFillTwinsPreservesExistingSummaryKeys(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "web", SummaryJSON: json.RawMessage(`{"repo_full_name":"octocat/hello","profile":"small"}`)},
		{Name: "worker", SummaryJSON: json.RawMessage(`{"profile":"small"}`)},
	}
	twins := map[string]TwinRef{
		"web": {ProjectID: "p2", ProjectName: "Other Project", AppName: "web", RepoFullName: "octocat/hello"},
	}

	FillTwins(apps, twins)

	var webSummary map[string]any
	if err := json.Unmarshal(apps[0].SummaryJSON, &webSummary); err != nil {
		t.Fatalf("unmarshal web summary: %v", err)
	}
	if got, want := webSummary["profile"], "small"; got != want {
		t.Fatalf("profile clobbered: got %v want %v", got, want)
	}
	twinRaw, ok := webSummary["twin_of"].(map[string]any)
	if !ok {
		t.Fatalf("twin_of missing or wrong shape: %#v", webSummary["twin_of"])
	}
	if twinRaw["project_id"] != "p2" || twinRaw["project_name"] != "Other Project" ||
		twinRaw["app_name"] != "web" || twinRaw["repo_full_name"] != "octocat/hello" {
		t.Fatalf("twin_of shape wrong: %#v", twinRaw)
	}

	var workerSummary map[string]any
	if err := json.Unmarshal(apps[1].SummaryJSON, &workerSummary); err != nil {
		t.Fatalf("unmarshal worker summary: %v", err)
	}
	if _, ok := workerSummary["twin_of"]; ok {
		t.Fatalf("worker has no twin, twin_of must not be set")
	}
}

func TestRepoByAppFromSummariesSkipsUploads(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "web", SummaryJSON: json.RawMessage(`{"repo_full_name":"octocat/hello"}`)},
		{Name: "archived-app", SummaryJSON: json.RawMessage(`{"repo_full_name":"upload/archived-app"}`)},
		{Name: "no-summary"},
	}
	got := RepoByAppFromSummaries(apps)
	if got["web"] != "octocat/hello" {
		t.Fatalf("web repo = %q, want octocat/hello", got["web"])
	}
	if _, ok := got["archived-app"]; ok {
		t.Fatalf("archive upload must never be reported as a repo/twin source: %#v", got)
	}
	if _, ok := got["no-summary"]; ok {
		t.Fatalf("app with no summary must not appear")
	}
}

func seedProjectWithOwner(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, owner_id) VALUES ($1, $2, $3) RETURNING id`,
		"twin-test-"+suffix, "Twin Test "+suffix, ownerID,
	).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, id) })
	return id
}

func seedEnvironment(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO environments (project_id, name, namespace, type, runtime) VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, "twin-test-ns-"+uuid.NewString()[:8],
	).Scan(&id); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return id
}

func seedTwinGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, repoFullName string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, 'github', $4, $5)`,
		projectID, envID, appName, repoFullName, "https://example.invalid/"+repoFullName+".git",
	); err != nil {
		t.Fatalf("seed git repo: %v", err)
	}
}

func TestLoadAppTwinsFindsTwinInSameOwnerOtherProject(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ownerID := seedUser(t, pool)

	projectA := seedProjectWithOwner(t, pool, ownerID)
	envA := seedEnvironment(t, pool, projectA)
	seedTwinGitRepo(t, pool, projectA, envA, "web", "octocat/hello")

	projectB := seedProjectWithOwner(t, pool, ownerID)
	envB := seedEnvironment(t, pool, projectB)
	seedTwinGitRepo(t, pool, projectB, envB, "web", "octocat/hello")

	h := &Handler{pool: pool}
	twins := h.loadAppTwins(ctx, projectA, map[string]string{"web": "octocat/hello"})

	twin, ok := twins["web"]
	if !ok {
		t.Fatalf("expected a twin for 'web', got none: %#v", twins)
	}
	if twin.ProjectID != projectB.String() {
		t.Fatalf("twin.ProjectID = %q, want %q", twin.ProjectID, projectB.String())
	}
	if twin.AppName != "web" {
		t.Fatalf("twin.AppName = %q, want web", twin.AppName)
	}
	if twin.RepoFullName != "octocat/hello" {
		t.Fatalf("twin.RepoFullName = %q, want octocat/hello", twin.RepoFullName)
	}
	if twin.ProjectName == "" {
		t.Fatalf("twin.ProjectName must not be empty")
	}
}

func TestLoadAppTwinsIgnoresDifferentOwner(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	ownerA := seedUser(t, pool)
	ownerB := seedUser(t, pool)

	projectA := seedProjectWithOwner(t, pool, ownerA)
	envA := seedEnvironment(t, pool, projectA)
	seedTwinGitRepo(t, pool, projectA, envA, "web", "octocat/hello")

	projectB := seedProjectWithOwner(t, pool, ownerB)
	envB := seedEnvironment(t, pool, projectB)
	seedTwinGitRepo(t, pool, projectB, envB, "web", "octocat/hello")

	h := &Handler{pool: pool}
	twins := h.loadAppTwins(ctx, projectA, map[string]string{"web": "octocat/hello"})

	if _, ok := twins["web"]; ok {
		t.Fatalf("must not report a twin owned by a different owner: %#v", twins)
	}
}

func TestLoadAppTwinsIgnoresUploadRepos(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ownerID := seedUser(t, pool)

	projectA := seedProjectWithOwner(t, pool, ownerID)

	projectB := seedProjectWithOwner(t, pool, ownerID)
	envB := seedEnvironment(t, pool, projectB)
	seedTwinGitRepo(t, pool, projectB, envB, "archived-app", "upload/archived-app")

	repoByApp := RepoByAppFromSummaries([]models.ResourceSnapshot{
		{Name: "archived-app", SummaryJSON: json.RawMessage(`{"repo_full_name":"upload/archived-app"}`)},
	})
	if len(repoByApp) != 0 {
		t.Fatalf("upload repo must be filtered before it ever reaches loadAppTwins: %#v", repoByApp)
	}

	h := &Handler{pool: pool}
	twins := h.loadAppTwins(ctx, projectA, repoByApp)
	if len(twins) != 0 {
		t.Fatalf("expected no twins, got %#v", twins)
	}
}
