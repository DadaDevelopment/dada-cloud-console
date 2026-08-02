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
func (a *liveTokenApp) BranchHead(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

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
		name        string
		repo        string
		root        string
		want        string
		wantPM      string
		wantPort    int
		wantBuild   string
		wantInstall string
		wantStart   string
	}{
		{name: "gitbucket-mcp-plugin", repo: "DadaDevelopment/gitbucket-mcp-plugin", root: ".", want: "scala", wantPM: "gradle", wantPort: 8080, wantBuild: "./gradlew shadowJar", wantInstall: "./gradlew dependencies"},
		{name: "reels-tracker", repo: "DadaDevelopment/reels-tracker", root: ".", want: "fastapi", wantPM: "pip", wantPort: 8000, wantInstall: "pip install -r requirements.txt"},
		{name: "telemost-bot", repo: "DadaDevelopment/telemost-bot", root: ".", want: "fastapi", wantPM: "pip", wantPort: 8000, wantInstall: "pip install -r requirements.txt"},
		{name: "dada-development-site", repo: "DadaDevelopment/dada-development-site", root: ".", want: "react", wantPM: "npm", wantPort: 5173, wantBuild: "npm run build", wantInstall: "npm ci", wantStart: "npm run preview"},
		{name: "dada-cloud-console", repo: "DadaDevelopment/dada-cloud-console", root: ".", want: "nextjs", wantPM: "npm", wantPort: 3000, wantBuild: "npm run build", wantInstall: "npm ci", wantStart: "npm run start"},
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
			t.Logf("%s => framework=%s pm=%s build=%s install=%s start=%s output=%s port=%d", tc.repo, got, strOrEmpty(det.PackageManager), strOrEmpty(det.BuildCommand), strOrEmpty(det.InstallCommand), strOrEmpty(det.StartCommand), strOrEmpty(det.OutputDir), intOrZero(det.Port))
			if got != tc.want {
				t.Fatalf("framework = %q, want %q", got, tc.want)
			}
			if det.PackageManager == nil || *det.PackageManager != tc.wantPM {
				t.Fatalf("package_manager = %v, want %s", det.PackageManager, tc.wantPM)
			}
			if det.Port == nil || *det.Port != tc.wantPort {
				t.Fatalf("port = %v, want %d", det.Port, tc.wantPort)
			}
			if tc.wantBuild != "" && (det.BuildCommand == nil || *det.BuildCommand != tc.wantBuild) {
				t.Fatalf("build_command = %v, want %s", det.BuildCommand, tc.wantBuild)
			}
			if tc.wantInstall != "" && (det.InstallCommand == nil || *det.InstallCommand != tc.wantInstall) {
				t.Fatalf("install_command = %v, want %s", det.InstallCommand, tc.wantInstall)
			}
			if tc.wantStart != "" && (det.StartCommand == nil || *det.StartCommand != tc.wantStart) {
				t.Fatalf("start_command = %v, want %s", det.StartCommand, tc.wantStart)
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

func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
