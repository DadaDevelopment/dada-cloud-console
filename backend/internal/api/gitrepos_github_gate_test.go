package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestLinkGitRepo_GithubGate pins the wiring between the clone probe and
// linkGitRepo, which TestGithubRepoPubliclyClonable does not reach: it covers
// the probe's classification only, so a linkGitRepo that stopped calling it
// would still leave that test green.
//
// Three cases, all with neither an installation nor a token, which is the only
// state the gate applies to: a decisive "not clonable" must fail the connect
// with github_access_required at the moment the user can still act on it; an
// inconclusive probe (github unreachable, 5xx, timeout) must never block; and a
// decisive yes must link normally.
func TestLinkGitRepo_GithubGate(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")

	cases := []struct {
		name      string
		appName   string
		clonable  bool
		decisive  bool
		wantFault bool
	}{
		{"decisive no blocks the connect", "gate-private", false, true, true},
		{"inconclusive probe never blocks", "gate-unknown", false, false, false},
		{"decisive yes links normally", "gate-public", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := githubCloneProbe
			githubCloneProbe = func(context.Context, string) (bool, bool) { return tc.clonable, tc.decisive }
			t.Cleanup(func() { githubCloneProbe = prev })
			t.Cleanup(func() { dropSeededAudit(pool, "GitRepo", tc.appName) })

			h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
			repo, fault := h.linkGitRepo(context.Background(), userID, projectID, envID, &connectGitRepoRequest{
				RepoFullName: "acme/" + uuid.NewString()[:8],
				AppName:      tc.appName,
				Provider:     "github",
			})

			if !tc.wantFault {
				if fault != nil {
					t.Fatalf("link failed: %+v", fault)
				}
				if repo == nil {
					t.Fatal("repo is nil with no fault")
				}
				return
			}
			if fault == nil {
				t.Fatal("link succeeded, want github_access_required")
			}
			if fault.Status != http.StatusBadRequest || fault.Reason != "github_access_required" {
				t.Fatalf("fault = %d/%s, want 400/github_access_required", fault.Status, fault.Reason)
			}
		})
	}
}
