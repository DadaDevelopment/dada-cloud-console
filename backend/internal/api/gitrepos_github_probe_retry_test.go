package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestProbeGithubCloneAccess_Retry pins the retry that stands between a single
// flaky answer from github.com and a permanently broken app.
//
// The gate only rejects on a decisive "not clonable", so one transport error,
// timeout or 5xx used to be enough to let a private, credential-less repo be
// stored — and every build of it then died with a bare "could not read
// Username for 'https://github.com'" days later. Retrying once turns a lone
// flap back into the verdict the user can act on while still looking at the
// form. The last two cases are the guard rails on that retry: it must not turn
// into an unbounded loop on a github.com that is genuinely down, and it must
// not keep a caller waiting after their request context is gone.
func TestProbeGithubCloneAccess_Retry(t *testing.T) {
	t.Run("flap then decisive no yields a verdict", func(t *testing.T) {
		answers := []struct{ clonable, decisive bool }{
			{false, false},
			{false, true},
		}
		calls := 0
		prev := githubCloneProbe
		githubCloneProbe = func(context.Context, string) (bool, bool) {
			a := answers[calls]
			calls++
			return a.clonable, a.decisive
		}
		t.Cleanup(func() { githubCloneProbe = prev })

		clonable, decisive := probeGithubCloneAccess(context.Background(), "keksmd/a2ahub-landing")
		if clonable || !decisive {
			t.Fatalf("probeGithubCloneAccess after a flap = (%v, %v), want (false, true)", clonable, decisive)
		}
		if calls != 2 {
			t.Fatalf("probe called %d times, want 2", calls)
		}
	})

	t.Run("decisive first answer is not retried", func(t *testing.T) {
		calls := 0
		prev := githubCloneProbe
		githubCloneProbe = func(context.Context, string) (bool, bool) {
			calls++
			return true, true
		}
		t.Cleanup(func() { githubCloneProbe = prev })

		clonable, decisive := probeGithubCloneAccess(context.Background(), "acme/public")
		if !clonable || !decisive {
			t.Fatalf("probeGithubCloneAccess = (%v, %v), want (true, true)", clonable, decisive)
		}
		if calls != 1 {
			t.Fatalf("probe called %d times on a decisive answer, want 1", calls)
		}
	})

	t.Run("github down stays inconclusive and bounded", func(t *testing.T) {
		calls := 0
		prev := githubCloneProbe
		githubCloneProbe = func(context.Context, string) (bool, bool) {
			calls++
			return false, false
		}
		t.Cleanup(func() { githubCloneProbe = prev })

		clonable, decisive := probeGithubCloneAccess(context.Background(), "acme/unknown")
		if clonable || decisive {
			t.Fatalf("probeGithubCloneAccess with github down = (%v, %v), want (false, false)", clonable, decisive)
		}
		if calls != githubCloneProbeAttempts {
			t.Fatalf("probe called %d times, want %d", calls, githubCloneProbeAttempts)
		}
	})

	t.Run("canceled context stops before the retry", func(t *testing.T) {
		calls := 0
		prev := githubCloneProbe
		githubCloneProbe = func(context.Context, string) (bool, bool) {
			calls++
			return false, false
		}
		t.Cleanup(func() { githubCloneProbe = prev })

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		clonable, decisive := probeGithubCloneAccess(ctx, "acme/unknown")
		if clonable || decisive {
			t.Fatalf("probeGithubCloneAccess on a canceled context = (%v, %v), want (false, false)", clonable, decisive)
		}
		if calls != 1 {
			t.Fatalf("probe called %d times on a canceled context, want 1", calls)
		}
	})
}

// TestLinkGitRepo_GithubGateRetriesFlap covers the same retry one level up,
// where it decides whether a row is written. TestProbeGithubCloneAccess_Retry
// would stay green if linkGitRepo went back to calling the raw probe once, and
// that single call is precisely the hole: the connect succeeds, the row lands
// with neither installation nor token, and the repo is dead from then on.
func TestLinkGitRepo_GithubGateRetriesFlap(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")

	const appName = "gate-flap-then-private"
	answers := []struct{ clonable, decisive bool }{
		{false, false},
		{false, true},
	}
	calls := 0
	prev := githubCloneProbe
	githubCloneProbe = func(context.Context, string) (bool, bool) {
		a := answers[calls]
		calls++
		return a.clonable, a.decisive
	}
	t.Cleanup(func() { githubCloneProbe = prev })
	t.Cleanup(func() { dropSeededAudit(pool, "GitRepo", appName) })

	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	repo, fault := h.linkGitRepo(context.Background(), userID, projectID, envID, &connectGitRepoRequest{
		RepoFullName: "acme/" + uuid.NewString()[:8],
		AppName:      appName,
		Provider:     "github",
	})

	if fault == nil {
		t.Fatalf("connect succeeded after a flap hid a private repo, repo=%+v", repo)
	}
	if fault.Status != http.StatusBadRequest || fault.Reason != "github_access_required" {
		t.Fatalf("fault = %d/%s, want 400/github_access_required", fault.Status, fault.Reason)
	}
	if calls != 2 {
		t.Fatalf("probe called %d times from linkGitRepo, want 2", calls)
	}
}
