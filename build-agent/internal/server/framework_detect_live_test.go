package server

import (
	"context"
	"os"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/github"
)

type liveTokenApp struct {
	token string
}

func (a *liveTokenApp) InstallToken(_ context.Context, _ int64) (string, error) { return a.token, nil }
func (a *liveTokenApp) ListRepos(_ context.Context, _ int64) ([]github.RemoteRepo, error) {
	return nil, nil
}
func (a *liveTokenApp) GetInstallation(_ context.Context, _ int64) (*github.InstallationAccount, error) {
	return nil, nil
}
func (a *liveTokenApp) ListInstallations(_ context.Context) ([]github.InstallationAccount, error) {
	return nil, nil
}
func (a *liveTokenApp) PostStatus(_ context.Context, _ int64, _, _, _, _, _ string) error { return nil }

func TestLiveDetectFrameworks(t *testing.T) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	if tok == "" {
		t.Skip("GITHUB_TOKEN/GH_TOKEN is required for live detection checks")
	}

	s := &Server{gh: &liveTokenApp{token: tok}}
	cases := []struct {
		name string
		repo string
		root string
		want string
	}{
		{name: "gitbucket-mcp-plugin", repo: "DadaDevelopment/gitbucket-mcp-plugin", root: ".", want: "scala"},
		{name: "reels-tracker", repo: "DadaDevelopment/reels-tracker", root: ".", want: "fastapi"},
		{name: "telemost-bot", repo: "DadaDevelopment/telemost-bot", root: ".", want: "fastapi"},
		{name: "dada-development-site", repo: "DadaDevelopment/dada-development-site", root: ".", want: "react"},
		{name: "dada-cloud-console", repo: "DadaDevelopment/dada-cloud-console", root: ".", want: "nextjs"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			det, err := s.detectFramework(context.Background(), 1, tc.repo, tc.root)
			if err != nil {
				t.Fatalf("detectFramework: %v", err)
			}
			if det.Framework == nil {
				t.Fatalf("framework = nil, want %s", tc.want)
			}
			got := *det.Framework
			t.Logf("%s => framework=%s build=%s install=%s output=%s", tc.repo, got, strOrEmpty(det.BuildCommand), strOrEmpty(det.InstallCommand), strOrEmpty(det.OutputDir))
			if got != tc.want {
				t.Fatalf("framework = %q, want %q", got, tc.want)
			}
		})
	}
}

func strOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
