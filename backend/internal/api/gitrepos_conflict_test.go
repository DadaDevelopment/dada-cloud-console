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
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "GitRepo", "shared-app") })

	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	ctx := context.Background()

	first := &connectGitRepoRequest{
		RepoFullName: "acme/first-repo",
		AppName:      "shared-app",
		Provider:     "github",
	}
	if _, fault := h.linkGitRepo(ctx, userID, projectID, envID, first); fault != nil {
		t.Fatalf("seed link failed: %+v", fault)
	}

	t.Run("same repo again yields repo_already_connected", func(t *testing.T) {
		repeat := &connectGitRepoRequest{
			RepoFullName: "acme/first-repo",
			AppName:      "shared-app",
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
			RepoFullName: "acme/second-repo",
			AppName:      "shared-app",
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
