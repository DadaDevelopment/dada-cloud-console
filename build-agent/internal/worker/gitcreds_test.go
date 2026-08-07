package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
)

// TestGitCredsGitLabAnonWhenNoToken locks in the fix for a public gitlab repo
// connected without a PAT: gitCreds must clone anonymously (empty token, bare
// clone_url) instead of failing with "gitlab repo missing token". Regressing
// this back to an error reproduces the fail_reason=git_auth_failed dead end
// seen in prod for a repo that never needed auth to clone in the first place.
func TestGitCredsGitLabAnonWhenNoToken(t *testing.T) {
	r := &Runner{}
	repo := &db.Repo{Provider: "gitlab", CloneURL: "https://gitlab.com/acme/app.git"}

	token, cloneURL, err := r.gitCreds(context.Background(), repo, &db.Build{})
	if err != nil {
		t.Fatalf("gitCreds: %v", err)
	}
	if token != "" {
		t.Fatalf("anon gitlab path must not mint a token, got %q", token)
	}
	if cloneURL != repo.CloneURL {
		t.Fatalf("anon gitlab path must return the bare clone url, got %q", cloneURL)
	}
	if strings.Contains(cloneURL, "@") {
		t.Fatalf("anon gitlab clone url must carry no credentials, got %q", cloneURL)
	}
}

// TestGitCredsGitHubAndGitLabAnonAreSymmetric pins the contract that both
// providers treat "no credential on file" the same way -- neither should be
// allowed to drift back to erroring while the other stays anonymous.
func TestGitCredsGitHubAndGitLabAnonAreSymmetric(t *testing.T) {
	r := &Runner{}
	ghRepo := &db.Repo{Provider: "github", InstallationID: 0, CloneURL: "https://github.com/acme/app.git"}
	glRepo := &db.Repo{Provider: "gitlab", CloneURL: "https://gitlab.com/acme/app.git"}

	ghToken, ghURL, ghErr := r.gitCreds(context.Background(), ghRepo, &db.Build{})
	glToken, glURL, glErr := r.gitCreds(context.Background(), glRepo, &db.Build{})

	if ghErr != nil || glErr != nil {
		t.Fatalf("expected no error, got github=%v gitlab=%v", ghErr, glErr)
	}
	if ghToken != "" || glToken != "" {
		t.Fatalf("expected both anonymous, got github token=%q gitlab token=%q", ghToken, glToken)
	}
	if ghURL != ghRepo.CloneURL || glURL != glRepo.CloneURL {
		t.Fatalf("expected both bare clone urls, got github=%q gitlab=%q", ghURL, glURL)
	}
}

// TestGitCredsGitLabWithToken exercises the credentialed branch end to end
// using the same AES-256-GCM helper the backend uses to encrypt the stored
// PAT, so no live database or network is needed.
func TestGitCredsGitLabWithToken(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const plainToken = "glpat-supersecret"

	enc, err := db.EncryptToken(key, plainToken)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}

	r := &Runner{cfg: &config.Config{EncryptionKey: key}}
	repo := &db.Repo{
		Provider:       "gitlab",
		CloneURL:       "https://gitlab.com/acme/app.git",
		TokenEncrypted: enc,
	}

	token, cloneURL, err := r.gitCreds(context.Background(), repo, &db.Build{})
	if err != nil {
		t.Fatalf("gitCreds: %v", err)
	}
	if token != plainToken {
		t.Fatalf("gitCreds token = %q, want %q", token, plainToken)
	}
	wantURL := "https://oauth2:" + plainToken + "@gitlab.com/acme/app.git"
	if cloneURL != wantURL {
		t.Fatalf("gitCreds cloneURL = %q, want %q", cloneURL, wantURL)
	}
}

// TestGitCredsGitLabForkUnsafeSkipsInjection mirrors the github fork-unsafe
// branch: even with a token on file, a fork-unsafe build must not embed
// credentials in the clone url.
func TestGitCredsGitLabForkUnsafeSkipsInjection(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	enc, err := db.EncryptToken(key, "glpat-supersecret")
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}

	r := &Runner{cfg: &config.Config{EncryptionKey: key}}
	repo := &db.Repo{
		Provider:       "gitlab",
		CloneURL:       "https://gitlab.com/acme/app.git",
		TokenEncrypted: enc,
	}

	_, cloneURL, err := r.gitCreds(context.Background(), repo, &db.Build{ForkUnsafe: true})
	if err != nil {
		t.Fatalf("gitCreds: %v", err)
	}
	if cloneURL != repo.CloneURL {
		t.Fatalf("fork-unsafe gitlab clone url should stay bare, got %q", cloneURL)
	}
}
