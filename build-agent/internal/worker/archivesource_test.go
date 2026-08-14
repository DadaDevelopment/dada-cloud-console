package worker

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/db"
)

type fakeArchivePresigner struct {
	enabled bool
	url     string
	err     error
}

func (f fakeArchivePresigner) Enabled() bool { return f.enabled }

func (f fakeArchivePresigner) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

func TestParseS3URL(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantBucket string
		wantKey    string
		wantErr    bool
	}{
		{"ok", "s3://uploads/source-uploads/dada/proj/app/abc123.tar.gz", "uploads", "source-uploads/dada/proj/app/abc123.tar.gz", false},
		{"no scheme", "uploads/key", "", "", true},
		{"no key", "s3://uploads", "", "", true},
		{"empty bucket", "s3:///key", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bucket, key, err := parseS3URL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tc.wantBucket || key != tc.wantKey {
				t.Fatalf("parseS3URL(%q) = (%q, %q), want (%q, %q)", tc.in, bucket, key, tc.wantBucket, tc.wantKey)
			}
		})
	}
}

func TestArchiveUploadID(t *testing.T) {
	cases := []struct{ key, want string }{
		{"source-uploads/dada/proj/app/abc123def456.tar.gz", "abc123def456"},
		{"source-uploads/dada/proj/app/abc123def456.zip", "abc123def456"},
		{"source-uploads/dada/proj/app/abc123def456.tgz", "abc123def456"},
		{"abc123def456", "abc123def456"},
	}
	for _, tc := range cases {
		if got := archiveUploadID(tc.key); got != tc.want {
			t.Errorf("archiveUploadID(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestArchiveUploadBranch(t *testing.T) {
	got := archiveUploadBranch("source-uploads/dada/proj/app/abc123def456789.tar.gz")
	want := "upload-abc123de"
	if got != want {
		t.Fatalf("archiveUploadBranch = %q, want %q", got, want)
	}

	got = archiveUploadBranch("source-uploads/dada/proj/app/short.zip")
	want = "upload-short"
	if got != want {
		t.Fatalf("archiveUploadBranch (short id) = %q, want %q", got, want)
	}
}

func TestGitCredsArchiveProvider(t *testing.T) {
	r := &Runner{archivePresign: fakeArchivePresigner{
		enabled: true,
		url:     "https://s3.example.com/uploads/source-uploads/dada/proj/app/abc12345.tar.gz?X-Amz-Signature=xyz",
	}}
	repo := &db.Repo{Provider: "archive", CloneURL: "s3://uploads/source-uploads/dada/proj/app/abc12345.tar.gz"}

	token, cloneURL, err := r.gitCreds(context.Background(), repo, &db.Build{})
	if err != nil {
		t.Fatalf("gitCreds: %v", err)
	}
	if token != "" {
		t.Fatalf("archive provider must not mint a git token, got %q", token)
	}
	if cloneURL != "https://s3.example.com/uploads/source-uploads/dada/proj/app/abc12345.tar.gz?X-Amz-Signature=xyz" {
		t.Fatalf("gitCreds returned unexpected presigned url: %q", cloneURL)
	}
}

func TestGitCredsArchiveProviderDisabled(t *testing.T) {
	r := &Runner{archivePresign: fakeArchivePresigner{enabled: false}}
	repo := &db.Repo{Provider: "archive", CloneURL: "s3://uploads/key.tar.gz"}

	if _, _, err := r.gitCreds(context.Background(), repo, &db.Build{}); err == nil {
		t.Fatal("expected error when archive presign is not configured")
	}
}

func TestGitCredsArchiveProviderMalformedCloneURL(t *testing.T) {
	r := &Runner{archivePresign: fakeArchivePresigner{enabled: true, url: "https://ignored"}}
	repo := &db.Repo{Provider: "archive", CloneURL: "not-an-s3-url"}

	if _, _, err := r.gitCreds(context.Background(), repo, &db.Build{}); err == nil {
		t.Fatal("expected error for malformed clone_url")
	}
}

func TestGitCredsGitHubAnonUnaffectedByArchivePresigner(t *testing.T) {
	r := &Runner{archivePresign: fakeArchivePresigner{enabled: false}}
	repo := &db.Repo{Provider: "github", InstallationID: 0, CloneURL: "https://github.com/acme/app.git"}

	token, cloneURL, err := r.gitCreds(context.Background(), repo, &db.Build{})
	if err != nil {
		t.Fatalf("gitCreds: %v", err)
	}
	if token != "" {
		t.Fatalf("anon github path must not mint a token, got %q", token)
	}
	if cloneURL != repo.CloneURL {
		t.Fatalf("anon github path must return the bare clone url, got %q", cloneURL)
	}
}

// TestSourceForBuild_ArchiveOverridesGitRepo covers the per-build archive
// source. A build queued by an archive upload carries the S3 URI on the build
// row, while git_repos keeps describing the app's GitHub binding, so this build
// must be run against the archive without the repo row ever being rewritten -
// the rewrite is what silently killed auto deploy on keksmd/family-tree.
func TestSourceForBuild_ArchiveOverridesGitRepo(t *testing.T) {
	token := []byte("secret")
	repo := &db.Repo{
		Provider:          "github",
		CloneURL:          "https://github.com/keksmd/family-tree.git",
		InstallationID:    42,
		TokenEncrypted:    token,
		FrameworkOverride: "nextjs",
		Port:              3000,
	}
	archive := "s3://uploads/source-uploads/proj/app/abc123.tar.gz"
	framework := "static"
	port := 8080
	b := &db.Build{ArchiveURL: &archive, ArchiveFramework: &framework, ArchivePort: &port}

	got := sourceForBuild(repo, b)

	if got.Provider != "archive" {
		t.Fatalf("provider = %q, want archive", got.Provider)
	}
	if got.CloneURL != archive {
		t.Fatalf("clone_url = %q, want %q", got.CloneURL, archive)
	}
	if got.InstallationID != 0 || got.TokenEncrypted != nil {
		t.Fatalf("git credentials leaked into the archive build: installation=%d token=%v", got.InstallationID, got.TokenEncrypted)
	}
	if got.FrameworkOverride != "static" {
		t.Fatalf("framework = %q, want static", got.FrameworkOverride)
	}
	if got.Port != 8080 {
		t.Fatalf("port = %d, want 8080", got.Port)
	}
	if repo.Provider != "github" || repo.CloneURL != "https://github.com/keksmd/family-tree.git" || repo.InstallationID != 42 {
		t.Fatalf("git_repos row was mutated: %+v", repo)
	}
}

// TestSourceForBuild_GitBuildUntouched keeps a normal push-triggered build
// running against git: no archive on the build row means nothing to override.
func TestSourceForBuild_GitBuildUntouched(t *testing.T) {
	repo := &db.Repo{Provider: "github", CloneURL: "https://github.com/keksmd/family-tree.git", InstallationID: 42}
	got := sourceForBuild(repo, &db.Build{})
	if got != repo {
		t.Fatalf("git build got a rewritten repo: %+v", got)
	}
}
