package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestLinkGitRepo_ConflictSplitsRepeatFromCollision proves the two things a
// caller of ConnectGitRepo needs to tell apart when the (project, environment,
// app_name) unique constraint on git_repos fires: reconnecting the very same
// repository under the same app name (a retried click, already succeeded) is
// not the same failure as a second repository fighting over an app name
// someone else already took. Before this change both landed on the single
// generic "repo_already_linked" 409 and the console could not show a message
// that matched what actually happened.
func TestLinkGitRepo_ConflictSplitsRepeatFromCollision(t *testing.T) {
	pool := testInstallPool(t)
	suffix := uuid.NewString()[:8]
	orgID := "acme-collision-" + suffix
	repoFirst := "acme/first-repo-" + suffix
	repoSecond := "acme/second-repo-" + suffix
	appName := "shared-app-" + suffix
	projectID, envID, userID := seedInstallProject(t, pool, orgID, "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "GitRepo", appName) })

	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	ctx := context.Background()

	first := &connectGitRepoRequest{
		RepoFullName: repoFirst,
		AppName:      appName,
		Provider:     "github",
	}
	if _, fault := h.linkGitRepo(ctx, userID, projectID, envID, first); fault != nil {
		t.Fatalf("seed link failed: %+v", fault)
	}

	t.Run("same repo again yields repo_already_connected", func(t *testing.T) {
		repeat := &connectGitRepoRequest{
			RepoFullName: repoFirst,
			AppName:      appName,
			Provider:     "github",
		}
		_, fault := h.linkGitRepo(ctx, userID, projectID, envID, repeat)
		if fault == nil {
			t.Fatalf("expected a conflict, got a successful link")
		}
		if fault.Status != http.StatusConflict {
			t.Fatalf("status = %d, want 409", fault.Status)
		}
		if fault.Reason != "repo_already_connected" {
			t.Fatalf("reason = %q, want repo_already_connected", fault.Reason)
		}
	})

	t.Run("different repo under the same app name yields app_name_taken", func(t *testing.T) {
		collider := &connectGitRepoRequest{
			RepoFullName: repoSecond,
			AppName:      appName,
			Provider:     "github",
		}
		_, fault := h.linkGitRepo(ctx, userID, projectID, envID, collider)
		if fault == nil {
			t.Fatalf("expected a conflict, got a successful link")
		}
		if fault.Status != http.StatusConflict {
			t.Fatalf("status = %d, want 409", fault.Status)
		}
		if fault.Reason != "app_name_taken" {
			t.Fatalf("reason = %q, want app_name_taken", fault.Reason)
		}
	})
}

// TestLinkGitRepo_SameRepoInAnotherProjectOfSameOrgIsRefused proves backlog
// 0385's actual failure mode is closed: the git_repos unique constraint only
// covers (project_id, environment_id, app_name), so nothing stopped the same
// owner from connecting the same repository under the SAME app name into a
// second project -- exactly what happened live with alexas85/SevaraTeamBot,
// bound first into project tvkassistantbot and then, a day later, into
// project sevarabot: two argo trees, two image repos, one live app and one
// eternal CrashLoop zombie, with no warning either time.
func TestLinkGitRepo_SameRepoInAnotherProjectOfSameOrgIsRefused(t *testing.T) {
	pool := testInstallPool(t)
	orgID := "same-owner-" + uuid.NewString()[:8]
	firstProjectID, firstEnvID, userID := seedInstallProject(t, pool, orgID, "k8s")
	secondProjectID, secondEnvID, _ := seedInstallProject(t, pool, orgID, "k8s")

	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	ctx := context.Background()

	first := &connectGitRepoRequest{
		RepoFullName: "alexas85/SevaraTeamBot",
		AppName:      "sevarateambot",
		Provider:     "github",
	}
	if _, fault := h.linkGitRepo(ctx, userID, firstProjectID, firstEnvID, first); fault != nil {
		t.Fatalf("seed link into first project failed: %+v", fault)
	}

	second := &connectGitRepoRequest{
		RepoFullName: "alexas85/SevaraTeamBot",
		AppName:      "sevarateambot",
		Provider:     "github",
	}
	_, fault := h.linkGitRepo(ctx, userID, secondProjectID, secondEnvID, second)
	if fault == nil {
		t.Fatalf("expected the second project's link to be refused, got a successful link (duplicate stack)")
	}
	if fault.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", fault.Status)
	}
	if fault.Reason != "repo_linked_to_other_project" {
		t.Fatalf("reason = %q, want repo_linked_to_other_project", fault.Reason)
	}
}

// TestLinkConflictFault_LookupMissBecomesAppNameTaken guards the fallback: if
// the diagnostic SELECT that distinguishes the two conflict causes finds no
// row (or a mismatched one), the caller still gets a 409 with a stable reason
// rather than an internal-error 500 caused by the fault-classification query
// itself.
func TestLinkConflictFault_LookupMissBecomesAppNameTaken(t *testing.T) {
	pool := testInstallPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	req := &connectGitRepoRequest{
		RepoFullName: "acme/never-inserted",
		AppName:      "no-such-app-" + uuid.NewString()[:8],
		Provider:     "github",
	}
	fault := h.linkConflictFault(ctx, uuid.New(), uuid.New(), req)
	if fault == nil {
		t.Fatalf("expected a fault, got nil")
	}
	if fault.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", fault.Status)
	}
	if fault.Reason != "app_name_taken" {
		t.Fatalf("reason = %q, want app_name_taken on a lookup miss", fault.Reason)
	}
}

// TestLinkGitRepo_SameRepoUnderAnotherAppNameIsAllowed keeps the 0385 guard
// from turning into a wall: one repository holding several deployable
// services is a legitimate layout, and only the same repo under the same app
// name produces the duplicate stack the guard exists to prevent.
func TestLinkGitRepo_SameRepoUnderAnotherAppNameIsAllowed(t *testing.T) {
	pool := testInstallPool(t)
	orgID := "monorepo-owner-" + uuid.NewString()[:8]
	firstProjectID, firstEnvID, userID := seedInstallProject(t, pool, orgID, "k8s")
	secondProjectID, secondEnvID, _ := seedInstallProject(t, pool, orgID, "k8s")

	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	ctx := context.Background()

	repo := "alexas85/" + uuid.NewString()[:8]
	first := &connectGitRepoRequest{RepoFullName: repo, AppName: "api", Provider: "github"}
	if _, fault := h.linkGitRepo(ctx, userID, firstProjectID, firstEnvID, first); fault != nil {
		t.Fatalf("seed link into first project failed: %+v", fault)
	}

	second := &connectGitRepoRequest{RepoFullName: repo, AppName: "worker", Provider: "github"}
	if _, fault := h.linkGitRepo(ctx, userID, secondProjectID, secondEnvID, second); fault != nil {
		t.Fatalf("second app name from the same repo must be allowed, got %+v", fault)
	}
}
