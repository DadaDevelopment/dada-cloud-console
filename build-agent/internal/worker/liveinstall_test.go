package worker

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/github"
)

type fakeApp struct {
	installs []github.InstallationAccount
}

func (f *fakeApp) InstallToken(context.Context, int64) (string, error) { return "", nil }
func (f *fakeApp) ListRepos(context.Context, int64) ([]github.RemoteRepo, error) {
	return nil, nil
}
func (f *fakeApp) GetInstallation(context.Context, int64) (*github.InstallationAccount, error) {
	return nil, nil
}
func (f *fakeApp) ListInstallations(context.Context) ([]github.InstallationAccount, error) {
	return f.installs, nil
}
func (f *fakeApp) ListBranches(context.Context, int64, string) ([]github.RemoteBranch, error) {
	return nil, nil
}
func (f *fakeApp) PostStatus(context.Context, int64, string, string, string, string, string) error {
	return nil
}
func (f *fakeApp) BranchHead(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}
func (f *fakeApp) SearchRepos(context.Context, string, int) ([]github.SearchHit, error) {
	return nil, nil
}

func TestLiveInstallationForOwner(t *testing.T) {
	app := &fakeApp{installs: []github.InstallationAccount{
		{InstallationID: 143604728, AccountLogin: "ggrk52", AccountType: "User"},
		{InstallationID: 143550113, AccountLogin: "keksmd", AccountType: "User"},
	}}
	r := &Runner{github: app}

	cases := []struct {
		name    string
		repo    string
		want    int64
		wantErr bool
	}{
		{"exact match", "keksmd/a2ahub-landing", 143550113, false},
		{"case-insensitive owner", "Keksmd/a2ahub-landing", 143550113, false},
		{"different owner", "ggrk52/site", 143604728, false},
		{"no live installation", "someoneelse/repo", 0, true},
		{"malformed repo", "no-slash", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.liveInstallationForOwner(context.Background(), tc.repo)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got id %d", tc.repo, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("liveInstallationForOwner(%q) = %d, want %d", tc.repo, got, tc.want)
			}
		})
	}
}
