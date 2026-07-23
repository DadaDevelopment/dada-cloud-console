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
