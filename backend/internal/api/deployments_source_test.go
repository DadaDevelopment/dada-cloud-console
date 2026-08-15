package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedGitRepo inserts a git_repos row so a build can reference it, cleaning up
// via the project cascade seedOptimisticFixture already registers.
func seedGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, provider string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, $4, 'octocat/hello', 'https://example.invalid/octocat/hello.git')
		 RETURNING id`,
		projectID, envID, appName, provider,
	).Scan(&id); err != nil {
		t.Fatalf("seed git repo: %v", err)
	}
	return id
}

// seedBuild inserts a builds row and returns its id.
func seedBuild(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA, commitMessage, branch string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, commit_message, branch, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'success')
		 RETURNING id`,
		gitRepoID, envID, appName, commitSHA, commitMessage, branch,
	).Scan(&id); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return id
}

// seedDeployment inserts a deployments row, optionally linked to a build.
// It deliberately leaves is_current at its DEFAULT FALSE: that column has no
// write path in production (see the doc comment on
// deploymentSelectColsWithSource in deployments.go) and ListDeployments no
// longer reads it, so seeding it true here would only test a fiction.
func seedDeployment(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName string, buildID *uuid.UUID, imageURI string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO deployments (environment_id, app_name, build_id, image_uri, trigger)
		 VALUES ($1, $2, $3, $4, 'push')
		 RETURNING id`,
		envID, appName, buildID, imageURI,
	).Scan(&id); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	return id
}

// seedDeploymentAt is seedDeployment with an explicit created_at, for tests
// that need to control deployment ordering (e.g. which of two deployments of
// the same image is the freshest).
func seedDeploymentAt(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName string, imageURI string, createdAt string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO deployments (environment_id, app_name, image_uri, trigger, created_at)
		 VALUES ($1, $2, $3, 'push', $4::timestamptz)
		 RETURNING id`,
		envID, appName, imageURI, createdAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed deployment at: %v", err)
	}
	return id
}

// TestListDeployments_ReturnsCommitProvenance is the regression gate for the
// console lying about deployment source: the deployments endpoint used to
// select only id/environment_id/app_name/build_id/operation_id/image_uri/
// trigger/is_current/created_at, so the frontend's resolveCommit() always
// fell back to kind:"none" and every row rendered as an "uploaded archive" --
// including plain git deploys. The endpoint must now join through
// builds -> git_repos and return commit_sha/commit_message/branch/source, and
// a deployment with no build_id at all must carry no source rather than a
// fabricated one.
func TestListDeployments_ReturnsCommitProvenance(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	gitApp := "git-app-" + uuid.NewString()[:8]
	repoID := seedGitRepo(t, pool, projectID, envID, gitApp, "github")
	buildID := seedBuild(t, pool, repoID, envID, gitApp, "abc123abc123abc123abc123abc123abc123ab", "fix the thing", "main")
	gitDeployID := seedDeployment(t, pool, envID, gitApp, &buildID, "harbor.example/proj/"+gitApp+"@sha256:aaa")

	noBuildApp := "manual-app-" + uuid.NewString()[:8]
	noBuildDeployID := seedDeployment(t, pool, envID, noBuildApp, nil, "harbor.example/proj/"+noBuildApp+"@sha256:bbb")

	c, rec := newGetCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: gitApp},
	}, godClaims(seedUser(t, pool)))
	h.ListDeployments(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("git app: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if len(body.Deployments) != 1 {
		t.Fatalf("git app: got %d deployments, want 1", len(body.Deployments))
	}
	d := body.Deployments[0]
	if d.ID != gitDeployID {
		t.Fatalf("git app: unexpected deployment id %s", d.ID)
	}
	if d.CommitSHA == nil || *d.CommitSHA != "abc123abc123abc123abc123abc123abc123ab" {
		t.Fatalf("git app: commit_sha = %v, want the seeded sha", d.CommitSHA)
	}
	if d.Branch == nil || *d.Branch != "main" {
		t.Fatalf("git app: branch = %v, want main", d.Branch)
	}
	if d.Source != "git" {
		t.Fatalf("git app: source = %q, want git", d.Source)
	}

	c2, rec2 := newGetCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: noBuildApp},
	}, godClaims(seedUser(t, pool)))
	h.ListDeployments(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("no-build app: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var body2 struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec2.Body.String())
	}
	if len(body2.Deployments) != 1 {
		t.Fatalf("no-build app: got %d deployments, want 1", len(body2.Deployments))
	}
	d2 := body2.Deployments[0]
	if d2.ID != noBuildDeployID {
		t.Fatalf("no-build app: unexpected deployment id %s", d2.ID)
	}
	if d2.CommitSHA != nil {
		t.Fatalf("no-build app: commit_sha = %v, want nil (no build_id, no fabricated commit)", d2.CommitSHA)
	}
	if d2.Source != "" {
		t.Fatalf("no-build app: source = %q, want empty (no build_id means no known provenance -- must not claim archive or git)", d2.Source)
	}
}

// listDeploymentsFor is a small helper shared by the is_current tests below:
// it calls ListDeployments for one app and decodes the deployments array.
func listDeploymentsFor(t *testing.T, h *Handler, projectID, envID uuid.UUID, appName string) []deployment {
	t.Helper()
	c, rec := newGetCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}, godClaims(seedUser(t, h.pool)))
	h.ListDeployments(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListDeployments status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	return body.Deployments
}

// TestListDeployments_CurrentBadgeFollowsRunningImage is the regression gate
// for the megafactory drift (found live 2026-08-15): the pod kept running an
// older deploy (image A, 22:58) after a newer one (image B, 23:20) had
// already landed a row in the deployments ledger, because is_current is
// never written by any insert path in production and therefore cannot
// reflect reality. The "current" badge must instead be derived from the
// app's actual running image (resource_snapshots.summary_json.image): the
// OLDER deployment, whose image matches what is really running, must be
// marked current, and the newer, undeployed one must NOT be.
func TestListDeployments_CurrentBadgeFollowsRunningImage(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	appName := "megafactory-" + uuid.NewString()[:8]
	oldImage := "harbor.example/proj/" + appName + "@sha256:" + "09af1723af1723af1723af1723af1723af1723af1723af1723af1723af1723"
	newImage := "harbor.example/proj/" + appName + "@sha256:" + "5575411e5575411e5575411e5575411e5575411e5575411e5575411e5575411e"

	oldDeployID := seedDeploymentAt(t, pool, envID, appName, oldImage, "2026-08-14 22:58:00+00")
	newDeployID := seedDeploymentAt(t, pool, envID, appName, newImage, "2026-08-14 23:20:00+00")
	seedAppWithImage(t, pool, projectID, envID, appName, oldImage)

	deployments := listDeploymentsFor(t, h, projectID, envID, appName)
	if len(deployments) != 2 {
		t.Fatalf("got %d deployments, want 2", len(deployments))
	}

	byID := map[uuid.UUID]deployment{}
	for _, d := range deployments {
		byID[d.ID] = d
	}
	if !byID[oldDeployID].IsCurrent {
		t.Fatalf("old (actually running) deployment %s: is_current = false, want true", oldDeployID)
	}
	if byID[newDeployID].IsCurrent {
		t.Fatalf("new (never deployed to the pod) deployment %s: is_current = true, want false -- this is the megafactory drift", newDeployID)
	}
}

// TestListDeployments_NoSnapshot_NoCurrent covers an app with deployments
// rows but no resource_snapshots row at all (e.g. the snapshot was never
// written or the app was deleted from the cluster): absence of data about
// what is running must not be rendered as a verdict, so nothing is marked
// current.
func TestListDeployments_NoSnapshot_NoCurrent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	appName := "no-snapshot-" + uuid.NewString()[:8]
	seedDeployment(t, pool, envID, appName, nil, "harbor.example/proj/"+appName+"@sha256:aaa")

	deployments := listDeploymentsFor(t, h, projectID, envID, appName)
	if len(deployments) != 1 {
		t.Fatalf("got %d deployments, want 1", len(deployments))
	}
	if deployments[0].IsCurrent {
		t.Fatalf("is_current = true with no resource_snapshots row, want false")
	}
}

// TestListDeployments_EmptyRunningImage_NoCurrent covers an app with a
// resource_snapshots row whose summary_json carries no image yet (e.g. a
// fresh app the reconciler hasn't synced): an empty running image must never
// be treated as a value to match an empty/absent image_uri against.
func TestListDeployments_EmptyRunningImage_NoCurrent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	appName := "empty-running-" + uuid.NewString()[:8]
	seedDeployment(t, pool, envID, appName, nil, "harbor.example/proj/"+appName+"@sha256:bbb")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Pending', '{}'::jsonb)`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed empty snapshot: %v", err)
	}

	deployments := listDeploymentsFor(t, h, projectID, envID, appName)
	if len(deployments) != 1 {
		t.Fatalf("got %d deployments, want 1", len(deployments))
	}
	if deployments[0].IsCurrent {
		t.Fatalf("is_current = true with empty running image, want false")
	}
}

// TestListDeployments_SameImageTwice_OnlyNewestMarkedCurrent covers an image
// that was deployed more than once (e.g. a rollback back to an earlier
// image): exactly one row -- the freshest by created_at -- must carry the
// current badge, never two.
func TestListDeployments_SameImageTwice_OnlyNewestMarkedCurrent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	appName := "redeployed-" + uuid.NewString()[:8]
	image := "harbor.example/proj/" + appName + "@sha256:ccc"

	firstID := seedDeploymentAt(t, pool, envID, appName, image, "2026-08-10 10:00:00+00")
	secondID := seedDeploymentAt(t, pool, envID, appName, image, "2026-08-12 10:00:00+00")
	seedAppWithImage(t, pool, projectID, envID, appName, image)

	deployments := listDeploymentsFor(t, h, projectID, envID, appName)
	if len(deployments) != 2 {
		t.Fatalf("got %d deployments, want 2", len(deployments))
	}

	byID := map[uuid.UUID]deployment{}
	current := 0
	for _, d := range deployments {
		byID[d.ID] = d
		if d.IsCurrent {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("got %d deployments marked current, want exactly 1", current)
	}
	if !byID[secondID].IsCurrent {
		t.Fatalf("freshest deployment %s (created 2026-08-12): is_current = false, want true", secondID)
	}
	if byID[firstID].IsCurrent {
		t.Fatalf("older deployment %s (created 2026-08-10): is_current = true, want false -- only the freshest may be marked", firstID)
	}
}
