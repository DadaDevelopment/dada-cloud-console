package apiclient

import (
	"context"
)

// GitRepo mirrors the subset of the console's git_repos row the CLI reads
// (backend/internal/api/gitrepos.go gitRepo).
type GitRepo struct {
	ID               string `json:"id"`
	AppName          string `json:"app_name"`
	Provider         string `json:"provider"`
	PlatformAccess   string `json:"platform_access"`
	RepoFullName     string `json:"repo_full_name"`
	ProductionBranch string `json:"production_branch"`
	RootDir          string `json:"root_dir"`
	AutoDeploy       bool   `json:"auto_deploy"`
}

type listGitReposResponse struct {
	Repos []GitRepo `json:"repos"`
}

// ListGitRepos returns the repositories linked in one environment, per
// GET /projects/:projectId/environments/:envId/repos.
func (c *Client) ListGitRepos(ctx context.Context, projectID, envID string) ([]GitRepo, error) {
	var out listGitReposResponse
	if err := c.doJSON(ctx, "GET", "/projects/"+projectID+"/environments/"+envID+"/repos", nil, "", &out); err != nil {
		return nil, err
	}
	return out.Repos, nil
}

// ConnectGitRepoRequest mirrors connectGitRepoRequest
// (backend/internal/api/gitrepos.go). Only RepoFullName, AppName and
// ProductionBranch matter to the CLI; the server fills in root dir, port,
// replicas and profile.
type ConnectGitRepoRequest struct {
	RepoFullName     string `json:"repo_full_name"`
	AppName          string `json:"app_name"`
	ProductionBranch string `json:"production_branch"`
	RootDir          string `json:"root_dir,omitempty"`
	AutoDeploy       bool   `json:"auto_deploy"`
	Provider         string `json:"provider,omitempty"`
}

// ConnectGitRepo links a repository to an app.
//
// A private GitHub repository is rejected with HTTP 400 and code
// "github_access_required": the platform clones anonymously unless a GitHub
// App installation is bound to the project, and the CLI cannot mint one. A
// repeat of the same link comes back 409 "repo_already_connected", which
// callers should treat as success.
func (c *Client) ConnectGitRepo(ctx context.Context, projectID, envID string, req ConnectGitRepoRequest) (GitRepo, error) {
	body, err := jsonBody(req)
	if err != nil {
		return GitRepo{}, err
	}
	var out listGitReposResponse
	path := "/projects/" + projectID + "/environments/" + envID + "/repos"
	if err := c.doJSON(ctx, "POST", path, body, "application/json", &out); err != nil {
		return GitRepo{}, err
	}
	if len(out.Repos) == 0 {
		return GitRepo{}, nil
	}
	return out.Repos[0], nil
}

// DisconnectGitRepo unlinks a repository from its app, per
// DELETE /projects/:projectId/environments/:envId/repos/:repoId. It only
// removes the git_repos row and the build history hanging off it; the deployed
// app, its domains and its volumes are untouched. There is no PATCH route, so
// this plus ConnectGitRepo is the only way to re-point an app at a different
// source.
func (c *Client) DisconnectGitRepo(ctx context.Context, projectID, envID, repoID string) error {
	path := "/projects/" + projectID + "/environments/" + envID + "/repos/" + repoID
	return c.doJSON(ctx, "DELETE", path, nil, "", nil)
}

type installURLResponse struct {
	URL string `json:"url"`
}

// GitInstallURL returns the GitHub App install URL for a project, per
// GET /projects/:projectId/git/install-url. The URL carries an HMAC-signed
// state binding the installation to this project, so it is the only way to
// give the platform read access to a private repository - visiting the App's
// page by hand installs it without binding it to anything.
func (c *Client) GitInstallURL(ctx context.Context, projectID string) (string, error) {
	var out installURLResponse
	path := "/projects/" + projectID + "/git/install-url?provider=github"
	if err := c.doJSON(ctx, "GET", path, nil, "", &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// GitHubAuthorizeURL returns the GitHub user-authorization URL for a project,
// per GET /projects/:projectId/git/github/authorize. It is the counterpart of
// GitInstallURL for the common case: the App is already installed on the
// user's account but recorded under a different org, so a fresh install is
// both wrong and impossible (GitHub sends an already-installed account to a
// configure page and never calls our setup URL back). Authorizing proves the
// caller owns that GitHub account, and the callback imports the installations
// it can already see into this project's org.
func (c *Client) GitHubAuthorizeURL(ctx context.Context, projectID string) (string, error) {
	var out installURLResponse
	if err := c.doJSON(ctx, "GET", "/projects/"+projectID+"/git/github/authorize", nil, "", &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// Installation is one GitHub App installation the project's org may link,
// mirroring availableInstallation (backend/internal/api/gitrepos.go).
type Installation struct {
	InstallationID string `json:"installation_id"`
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type"`
	Bound          bool   `json:"bound"`
}

type availableInstallationsResponse struct {
	Installations []Installation `json:"installations"`
}

// ListAvailableInstallations returns the installations visible to a project's
// org, per GET /projects/:projectId/git/installations/available. The list is
// how the CLI notices, without asking the user anything, that a trip to GitHub
// has finished: both the install callback and the authorization callback write
// the row this reads.
func (c *Client) ListAvailableInstallations(ctx context.Context, projectID string) ([]Installation, error) {
	var out availableInstallationsResponse
	if err := c.doJSON(ctx, "GET", "/projects/"+projectID+"/git/installations/available", nil, "", &out); err != nil {
		return nil, err
	}
	return out.Installations, nil
}

type triggerBuildResponse struct {
	Build Build `json:"build"`
}

// TriggerBuild queues a build of the app's linked repository. It requires the
// builds:write scope (backend/internal/api/router.go:592) and answers 409 when
// the app has no linked repository.
func (c *Client) TriggerBuild(ctx context.Context, projectID, envID, appName string) (Build, error) {
	var out triggerBuildResponse
	path := "/projects/" + projectID + "/environments/" + envID + "/apps/" + appName + "/builds"
	if err := c.doJSON(ctx, "POST", path, nil, "application/json", &out); err != nil {
		return Build{}, err
	}
	return out.Build, nil
}
