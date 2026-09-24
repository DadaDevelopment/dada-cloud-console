package worker

import (
	"testing"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/db"
)

func TestGHDeployVerdict(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	img := "nexus/p/app@sha256:new"
	landed := db.OpenGitHubDeployment{
		BuildStatus: db.StatusSuccess, BuildImage: img, BuildUpdatedAt: now.Add(-2 * time.Minute),
		HasDeploy: true, DeployImage: img, OpStatus: "Committed",
	}
	with := func(f func(*db.OpenGitHubDeployment)) db.OpenGitHubDeployment {
		o := landed
		f(&o)
		return o
	}
	cases := []struct {
		name    string
		in      db.OpenGitHubDeployment
		state   string
		envURL  string
		settled bool
	}{
		{"still building", with(func(o *db.OpenGitHubDeployment) { o.BuildStatus = "building" }), "", "", false},
		{"build failed", with(func(o *db.OpenGitHubDeployment) { o.BuildStatus = db.StatusFailed }), "failure", "", true},
		{"build canceled", with(func(o *db.OpenGitHubDeployment) { o.BuildStatus = db.StatusCanceled }), "error", "", true},
		{"no image means nothing to roll out", with(func(o *db.OpenGitHubDeployment) { o.BuildImage = "" }), "success", "", true},
		{"handoff pending", with(func(o *db.OpenGitHubDeployment) { o.HasDeploy = false }), "", "", false},
		{"handoff never came", with(func(o *db.OpenGitHubDeployment) {
			o.HasDeploy = false
			o.BuildUpdatedAt = now.Add(-10 * time.Minute)
		}), "error", "", true},
		{"operation failed", with(func(o *db.OpenGitHubDeployment) { o.OpStatus = "Failed" }), "failure", "", true},
		{"old image still running", with(func(o *db.OpenGitHubDeployment) {
			o.LiveImage = "nexus/p/app@sha256:old"
			o.LiveStatus = "Ready"
		}), "", "", false},
		{"new image not ready yet", with(func(o *db.OpenGitHubDeployment) {
			o.LiveImage = img
			o.LiveStatus = "Pending"
		}), "", "", false},
		{"landed and ready", with(func(o *db.OpenGitHubDeployment) {
			o.LiveImage = img
			o.LiveStatus = "Ready"
			o.LiveURL = "https://app.dada-tuda.ru"
		}), "success", "https://app.dada-tuda.ru", true},
		{"landed and crashing", with(func(o *db.OpenGitHubDeployment) {
			o.LiveImage = img
			o.LiveStatus = "CrashLoop"
		}), "failure", "", true},
		{"superseded", with(func(o *db.OpenGitHubDeployment) { o.Superseded = true }), "inactive", "", true},
		{"rollout never confirmed", with(func(o *db.OpenGitHubDeployment) { o.BuildUpdatedAt = now.Add(-31 * time.Minute) }), "error", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, desc, envURL, done := ghDeployVerdict(tc.in, now)
			if done != tc.settled || state != tc.state || envURL != tc.envURL {
				t.Fatalf("verdict = (%q, %q, %q, %v), want (%q, _, %q, %v)", state, desc, envURL, done, tc.state, tc.envURL, tc.settled)
			}
			if done && desc == "" {
				t.Fatal("settled verdict without a description")
			}
		})
	}
}

func TestGitHubEnvironmentLabel(t *testing.T) {
	cases := []struct {
		env        db.GitHubEnvironment
		want       string
		production bool
	}{
		{db.GitHubEnvironment{Name: "prod", Type: "prod", RepoApps: 1}, "Production", true},
		{db.GitHubEnvironment{Name: "main", Type: "prod", RepoApps: 1}, "Production", true},
		{db.GitHubEnvironment{Name: "staging", Type: "dev", RepoApps: 1}, "Staging", false},
		{db.GitHubEnvironment{Name: "prod", Type: "prod", RepoApps: 2}, "Production - web", true},
		{db.GitHubEnvironment{Name: "", Type: "", RepoApps: 1}, "Preview", false},
	}
	for _, tc := range cases {
		got, prod := githubEnvironment(tc.env, "web")
		if got != tc.want || prod != tc.production {
			t.Errorf("githubEnvironment(%+v) = %q, %v; want %q, %v", tc.env, got, prod, tc.want, tc.production)
		}
	}
}
