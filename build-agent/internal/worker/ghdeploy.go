package worker

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/github"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	ghDeployRolloutWait  = 30 * time.Minute
	ghDeployHandoffWait  = 5 * time.Minute
	ghDeployRetryWindow  = 2 * time.Hour
	ghDeploySyncBatch    = 50
	ghDeployCallDeadline = 15 * time.Second
)

// buildPageURL is the console page of one build: the commit status details link
// and the GitHub Deployment log link both point here.
func buildPageURL(projectSlug, appName string, buildID uuid.UUID) string {
	return fmt.Sprintf("https://console.dada-tuda.ru/projects/%s/apps/%s/builds/%s",
		projectSlug, appName, buildID.String())
}

// githubEnvironment names the GitHub environment a deploy is filed under, the
// label the repository's Deployments tab groups by. A prod-type environment is
// "Production" and flagged as such, anything else carries its own name. When
// several apps build from the same repository each gets its own environment,
// otherwise every app's success would mark the others' deployments inactive.
func githubEnvironment(g db.GitHubEnvironment, appName string) (string, bool) {
	production := g.Type == "prod" || g.Name == "prod" || g.Name == "production"
	label := "Production"
	if !production {
		label = capitalize(g.Name)
		if label == "" {
			label = "Preview"
		}
	}
	if g.RepoApps > 1 {
		label += " - " + appName
	}
	return label, production
}

func capitalize(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

// openGitHubDeployment opens the GitHub Deployment for a build and marks it in
// progress, so the repository's Deployments tab shows the build the moment it
// starts, the way Vercel's does. Only GitHub App repos qualify (a PAT-linked or
// anonymous repo has no installation to act as) and only once the commit is a
// real sha: a manual build's placeholder is resolved just before this call.
// Everything here is best-effort: a refusal (typically an installation that has
// not accepted the Deployments permission) is recorded as unavailable and the
// build carries on untouched.
func (r *Runner) openGitHubDeployment(ctx context.Context, repo *db.Repo, b *db.Build, ref string) {
	if repo.Provider != "github" || repo.InstallationID == 0 || ref == "" || strings.HasPrefix(ref, "manual-") {
		return
	}
	claimed, err := db.ClaimGitHubDeployment(ctx, r.pool, b.ID)
	if err != nil || !claimed {
		if err != nil {
			log.Debug().Err(err).Str("build", b.ID.String()).Msg("claim github deployment")
		}
		return
	}
	g, err := db.LoadGitHubEnvironment(ctx, r.pool, b.EnvironmentID, repo.RepoFullName)
	if err != nil {
		log.Debug().Err(err).Str("build", b.ID.String()).Msg("load github environment")
		_ = db.SetGitHubDeployment(ctx, r.pool, b.ID, 0, 0, db.GHDeployUnavailable)
		return
	}
	env, production := githubEnvironment(g, repo.AppName)
	id, err := r.github.CreateDeployment(ctx, repo.InstallationID, repo.RepoFullName, github.DeploymentRequest{
		Ref:         ref,
		Environment: env,
		Production:  production,
		Description: "dada cloud: " + repo.AppName,
	})
	if err != nil {
		log.Info().Err(err).Str("build", b.ID.String()).Str("repo", repo.RepoFullName).
			Msg("github deployment not opened")
		_ = db.SetGitHubDeployment(ctx, r.pool, b.ID, 0, 0, db.GHDeployUnavailable)
		return
	}
	if err := db.SetGitHubDeployment(ctx, r.pool, b.ID, id, repo.InstallationID, db.GHDeployInProgress); err != nil {
		log.Warn().Err(err).Str("build", b.ID.String()).Msg("record github deployment")
		_ = r.github.PostDeploymentStatus(ctx, repo.InstallationID, repo.RepoFullName, id, github.DeploymentStatus{
			State:       "error",
			LogURL:      buildPageURL(repo.ProjectSlug, repo.AppName, b.ID),
			Description: "Deployment tracking unavailable",
		})
		_ = db.SetGitHubDeployment(ctx, r.pool, b.ID, 0, 0, db.GHDeployUnavailable)
		return
	}
	if err := r.github.PostDeploymentStatus(ctx, repo.InstallationID, repo.RepoFullName, id, github.DeploymentStatus{
		State:       "in_progress",
		LogURL:      buildPageURL(repo.ProjectSlug, repo.AppName, b.ID),
		Description: "Building",
	}); err != nil {
		log.Debug().Err(err).Str("build", b.ID.String()).Msg("post github deployment in_progress")
	}
}

// ghDeployVerdict decides whether an open GitHub Deployment is finished and
// with what. done=false means keep waiting. The deploy counts as a success only
// once the app is observed running the deployed image and Ready, the same
// running-image evidence the console's own "current deployment" badge uses;
// the build turning green is only half of that.
func ghDeployVerdict(o db.OpenGitHubDeployment, now time.Time) (state, desc, envURL string, done bool) {
	switch o.BuildStatus {
	case db.StatusFailed:
		return "failure", "Build failed", "", true
	case db.StatusCanceled:
		return "error", "Build canceled", "", true
	case db.StatusSuccess:
	default:
		return "", "", "", false
	}
	if o.BuildImage == "" {
		return "success", "Build succeeded", "", true
	}
	age := now.Sub(o.BuildUpdatedAt)
	if !o.HasDeploy {
		if age > ghDeployHandoffWait {
			return "error", "Build succeeded but the deploy was not started", "", true
		}
		return "", "", "", false
	}
	if o.OpStatus == "Failed" {
		msg := "Deploy failed"
		if o.OpError != "" {
			msg += ": " + o.OpError
		}
		return "failure", msg, "", true
	}
	if o.Superseded {
		return "inactive", "Superseded by a newer deployment", "", true
	}
	if o.LiveImage != "" && o.LiveImage == o.DeployImage {
		switch o.LiveStatus {
		case "Ready":
			return "success", "Deployed", o.LiveURL, true
		case "CrashLoop":
			return "failure", "App crashes on start", "", true
		}
	}
	if age > ghDeployRolloutWait {
		return "error", "Rollout not confirmed within 30 minutes", "", true
	}
	return "", "", "", false
}

// SyncGitHubDeployments settles every open GitHub Deployment whose outcome is
// now known. Each transition is claimed in the database first so two agent
// replicas never post the same verdict twice; a post GitHub rejects is handed
// back for the next tick while the deployment is young enough to still matter.
func (r *Runner) SyncGitHubDeployments(ctx context.Context) {
	open, err := db.ListOpenGitHubDeployments(ctx, r.pool, ghDeploySyncBatch)
	if err != nil {
		log.Debug().Err(err).Msg("list open github deployments")
		return
	}
	now := time.Now()
	for _, o := range open {
		if o.RepoFullName == "" {
			_, _ = db.MoveGitHubDeployment(ctx, r.pool, o.BuildID, db.GHDeployInProgress, db.GHDeployUnavailable)
			continue
		}
		state, desc, envURL, done := ghDeployVerdict(o, now)
		if !done {
			continue
		}
		ok, err := db.MoveGitHubDeployment(ctx, r.pool, o.BuildID, db.GHDeployInProgress, state)
		if err != nil || !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, ghDeployCallDeadline)
		perr := r.github.PostDeploymentStatus(cctx, o.InstallationID, o.RepoFullName, o.DeploymentID, github.DeploymentStatus{
			State:          state,
			LogURL:         buildPageURL(o.ProjectSlug, o.AppName, o.BuildID),
			EnvironmentURL: envURL,
			Description:    desc,
		})
		cancel()
		if perr == nil {
			log.Info().Str("build", o.BuildID.String()).Str("state", state).Msg("github deployment settled")
			continue
		}
		log.Warn().Err(perr).Str("build", o.BuildID.String()).Str("state", state).Msg("post github deployment status")
		if now.Sub(o.BuildUpdatedAt) < ghDeployRetryWindow {
			_, _ = db.MoveGitHubDeployment(ctx, r.pool, o.BuildID, state, db.GHDeployInProgress)
		}
	}
}

// BackfillGitHubDeployments gives every GitHub App repo that has never had a
// GitHub Deployment one for the commit its app is running now, marked success
// with the app's URL, so the repository's Deployments tab is not empty until
// the next push. It only touches repos with no deployment history at all and
// claims each build first, so it is safe to run on every agent start. A repo
// whose app is not Ready is skipped rather than reported as deployed.
func (r *Runner) BackfillGitHubDeployments(ctx context.Context) {
	candidates, err := db.ListGitHubBackfillCandidates(ctx, r.pool)
	if err != nil {
		log.Warn().Err(err).Msg("github deployment backfill: listing candidates")
		return
	}
	var opened, refused, skipped int
	for _, c := range candidates {
		if c.LiveStatus != "Ready" || c.Ref == "" || strings.HasPrefix(c.Ref, "manual-") {
			skipped++
			continue
		}
		claimed, err := db.ClaimGitHubDeployment(ctx, r.pool, c.BuildID)
		if err != nil || !claimed {
			skipped++
			continue
		}
		g, err := db.LoadGitHubEnvironment(ctx, r.pool, c.EnvironmentID, c.RepoFullName)
		if err != nil {
			_ = db.SetGitHubDeployment(ctx, r.pool, c.BuildID, 0, 0, db.GHDeployUnavailable)
			skipped++
			continue
		}
		env, production := githubEnvironment(g, c.AppName)
		cctx, cancel := context.WithTimeout(ctx, ghDeployCallDeadline)
		id, err := r.github.CreateDeployment(cctx, c.InstallationID, c.RepoFullName, github.DeploymentRequest{
			Ref:         c.Ref,
			Environment: env,
			Production:  production,
			Description: "dada cloud: " + c.AppName,
		})
		cancel()
		if err != nil {
			log.Info().Err(err).Str("repo", c.RepoFullName).Int64("installation", c.InstallationID).
				Msg("github deployment backfill: refused")
			_ = db.SetGitHubDeployment(ctx, r.pool, c.BuildID, 0, 0, db.GHDeployUnavailable)
			refused++
			continue
		}
		cctx, cancel = context.WithTimeout(ctx, ghDeployCallDeadline)
		perr := r.github.PostDeploymentStatus(cctx, c.InstallationID, c.RepoFullName, id, github.DeploymentStatus{
			State:          "success",
			LogURL:         buildPageURL(c.ProjectSlug, c.AppName, c.BuildID),
			EnvironmentURL: c.LiveURL,
			Description:    "Deployed",
		})
		cancel()
		state := "success"
		if perr != nil {
			log.Warn().Err(perr).Str("repo", c.RepoFullName).Msg("github deployment backfill: status not posted")
			state = db.GHDeployInProgress
		}
		if err := db.SetGitHubDeployment(ctx, r.pool, c.BuildID, id, c.InstallationID, state); err != nil {
			log.Warn().Err(err).Str("repo", c.RepoFullName).Msg("github deployment backfill: recording")
		}
		opened++
		log.Info().Str("repo", c.RepoFullName).Str("app", c.AppName).Str("ref", c.Ref).Int64("deployment", id).
			Msg("github deployment backfill: opened")
	}
	log.Info().Int("candidates", len(candidates)).Int("opened", opened).Int("refused", refused).Int("skipped", skipped).
		Msg("github deployment backfill done")
}
