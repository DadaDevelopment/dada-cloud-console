package cliapp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/cli/internal/agentmarker"
	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/appname"
	"github.com/dada-tuda/console/cli/internal/archive"
	"github.com/dada-tuda/console/cli/internal/gitremote"
)

// DeployOptions are the resolved inputs to a single `ddc deploy` run.
type DeployOptions struct {
	Dir     string
	AppName string
	Project string
}

// buildPoll* control how often ddc asks the console for build status while
// streaming progress. 2s balances "feels live" against hammering the API.
const (
	buildPollInterval = 2 * time.Second
	buildPollTimeout  = 20 * time.Minute
	urlPollInterval   = 3 * time.Second
	urlPollTimeout    = 3 * time.Minute
)

// Deploy runs the full v0 flow: resolve project/environment/app name,
// package the directory, upload it, stream build status, then print the
// live URL once the platform has assigned one.
func Deploy(ctx context.Context, cfg Config, opts DeployOptions, in io.Reader, out io.Writer) error {
	if err := EnsureLoggedIn(ctx, cfg, out); err != nil {
		return err
	}
	client := apiclient.New(cfg.APIBase, &http.Client{}, TokenSource(cfg), agentmarker.DetectFromEnv())

	appName, err := resolveAppName(opts)
	if err != nil {
		return err
	}

	target, err := resolveTarget(ctx, client, opts, appName, in, out)
	if err != nil {
		return err
	}
	projectID, envID := target.ProjectID, target.EnvID
	fmt.Fprintf(out, "App: %s -> %s / %s\n", appName, target.ProjectName, target.EnvName)

	buildID, fromGit := deployFromGit(ctx, client, projectID, envID, appName, opts.Dir, out)
	if !fromGit {
		buildID, err = deployFromArchive(ctx, client, projectID, envID, appName, opts.Dir, out)
		if err != nil {
			return err
		}
	}

	finalStatus, err := streamBuildStatus(ctx, client, projectID, envID, appName, buildID, out)
	if err != nil {
		return err
	}
	if finalStatus != apiclient.StatusSuccess {
		return fmt.Errorf("build finished with status %q - see the build log in the console for details", finalStatus)
	}

	url, ok, err := pollAppURL(ctx, client, projectID, envID, appName, out)
	if err != nil {
		return err
	}
	if ok {
		fmt.Fprintf(out, "\nLive: %s\n", url)
	} else {
		fmt.Fprintln(out, "\nBuild succeeded. The app's URL was not assigned yet - check it in the console.")
	}
	return nil
}

// deployFromGit builds straight from the folder's GitHub remote when that is
// possible, which is both faster and gives the console a real commit instead
// of an opaque archive. Every reason it cannot is announced in one line and
// answered by returning ok=false, so the caller falls back to the upload path
// rather than failing the deploy.
func deployFromGit(ctx context.Context, client *apiclient.Client, projectID, envID, appName, dir string, out io.Writer) (string, bool) {
	info := gitremote.Detect(dir)
	if !info.IsRepo {
		return "", false
	}
	fallback := func(reason string) (string, bool) {
		fmt.Fprintf(out, "Uploading the folder instead of building from git: %s.\n", reason)
		return "", false
	}
	if info.Unsupported != "" {
		return fallback(info.Unsupported)
	}
	if info.Dirty {
		return fallback("you have uncommitted changes")
	}
	if !info.HeadPushed {
		return fallback("this commit is not pushed to origin/" + info.Branch)
	}

	repos, err := client.ListGitRepos(ctx, projectID, envID)
	if err != nil {
		return fallback("could not read this environment's repositories")
	}
	linked, found := findGitRepo(repos, appName)
	switch {
	case found && linked.RepoFullName != info.FullName:
		return fallback(fmt.Sprintf("app %q is already linked to %s", appName, linked.RepoFullName))
	case !found:
		fmt.Fprintf(out, "Linking %s (%s)...\n", info.FullName, info.Branch)
		_, err := client.ConnectGitRepo(ctx, projectID, envID, apiclient.ConnectGitRepoRequest{
			RepoFullName:     info.FullName,
			AppName:          appName,
			ProductionBranch: info.Branch,
			RootDir:          rootDirOrDot(info.SubdirOfRoot),
			AutoDeploy:       true,
			Provider:         "github",
		})
		if err != nil && !isAlreadyConnected(err) {
			var apiErr *apiclient.APIError
			if errors.As(err, &apiErr) && apiErr.Code == "github_access_required" {
				return fallback("the platform cannot clone this repository - connect GitHub to the project to build from it")
			}
			return fallback("linking the repository failed (" + apiclient.Explain(err) + ")")
		}
	}

	build, err := client.TriggerBuild(ctx, projectID, envID, appName)
	if err != nil {
		return fallback("starting the build from git failed (" + apiclient.Explain(err) + ")")
	}
	fmt.Fprintf(out, "Source: github.com/%s @ %s\n", info.FullName, info.Branch)
	fmt.Fprintf(out, "Build queued: %s\n", build.ID)
	return build.ID, true
}

func rootDirOrDot(sub string) string {
	if sub == "" {
		return "."
	}
	return sub
}

func findGitRepo(repos []apiclient.GitRepo, appName string) (apiclient.GitRepo, bool) {
	for _, r := range repos {
		if r.AppName == appName {
			return r, true
		}
	}
	return apiclient.GitRepo{}, false
}

// isAlreadyConnected reports the harmless half of the link conflict: this app
// is already linked to this very repository, which is exactly the state the
// caller wanted.
func isAlreadyConnected(err error) bool {
	var apiErr *apiclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "repo_already_connected"
}

// deployFromArchive is the no-git path: package the directory, upload it, and
// return the id of the build the upload queued.
func deployFromArchive(ctx context.Context, client *apiclient.Client, projectID, envID, appName, dir string, out io.Writer) (string, error) {
	entries, total, err := archive.Plan(dir)
	if err != nil {
		return "", fmt.Errorf("scanning %s: %w", dir, err)
	}
	if total > archive.MaxBytes {
		return "", fmt.Errorf("this project is %.1fMB, over the console's %dMB upload limit - "+
			"trim large files or add them to .gitignore and try again",
			float64(total)/1024/1024, archive.MaxBytes/1024/1024)
	}
	fmt.Fprintf(out, "Packaging %d files (%.1fMB)...\n", len(entries), float64(total)/1024/1024)

	data, err := archive.Build(dir, entries)
	if err != nil {
		return "", fmt.Errorf("building archive: %w", err)
	}

	fmt.Fprintln(out, "Uploading...")
	uploadResp, err := client.UploadSourceArchive(ctx, projectID, envID, appName, appName+".tar.gz", data)
	if err != nil {
		return "", fmt.Errorf("upload failed: %s", apiclient.Explain(err))
	}
	fmt.Fprintf(out, "Detected: %s (port %d)\n", nonEmpty(uploadResp.Detected.Framework, "unknown"), uploadResp.Detected.Port)
	fmt.Fprintf(out, "Build queued: %s\n", uploadResp.Build.ID)
	return uploadResp.Build.ID, nil
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// resolveTarget decides where this directory deploys to. The first run in a
// folder never shows a menu: it reuses the project named after the folder, or
// creates it. Later runs reuse what was remembered for that folder. The menu
// survives only as the fallback for --project naming something ambiguous.
func resolveTarget(ctx context.Context, client *apiclient.Client, opts DeployOptions, appName string, in io.Reader, out io.Writer) (Target, error) {
	if opts.Project == "" {
		if remembered, ok, err := LookupTarget(opts.Dir); err == nil && ok {
			return remembered, nil
		}
	}

	projects, err := client.ListProjects(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("listing projects: %s", apiclient.Explain(err))
	}

	wanted := opts.Project
	if wanted == "" {
		wanted = appName
	}

	project, found := findProject(projects, wanted)
	if !found {
		if opts.Project != "" {
			return Target{}, fmt.Errorf("no project named %q - run it without --project to create one", opts.Project)
		}
		project, err = createProjectForFolder(ctx, client, wanted, out)
		if err != nil {
			return Target{}, err
		}
	}

	env, err := resolveEnvironment(ctx, client, project, in, out)
	if err != nil {
		return Target{}, err
	}

	target := Target{
		ProjectID:   project.ID,
		ProjectName: nonEmpty(project.DisplayName, project.Name),
		EnvID:       env.ID,
		EnvName:     env.Name,
		AppName:     appName,
	}
	if err := RememberTarget(opts.Dir, target); err != nil {
		fmt.Fprintf(out, "note: could not remember this folder's target (%v)\n", err)
	}
	return target, nil
}

// createProjectForFolder mints the project this folder deploys into. Slugs
// are unique platform-wide (backend/internal/api/projects.go:218), so a name
// another tenant already took comes back 409 and is retried with a short
// suffix rather than dumping the user back into a menu.
func createProjectForFolder(ctx context.Context, client *apiclient.Client, wanted string, out io.Writer) (apiclient.Project, error) {
	slug := appname.NormalizeProject(wanted)
	if !appname.ValidProject(slug) {
		return apiclient.Project{}, fmt.Errorf("could not derive a project name from %q - pass an existing one with --project", wanted)
	}

	fmt.Fprintf(out, "First deploy from this folder - creating project %q...\n", slug)
	project, err := client.CreateProject(ctx, slug)
	if err == nil {
		return project, nil
	}

	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		return apiclient.Project{}, fmt.Errorf("creating project %q: %s", slug, apiclient.Explain(err))
	}

	taken := slug
	suffixed, sufErr := suffixSlug(slug)
	if sufErr != nil {
		return apiclient.Project{}, sufErr
	}
	fmt.Fprintf(out, "Name %q is taken platform-wide - using %q instead.\n", taken, suffixed)
	project, err = client.CreateProject(ctx, suffixed)
	if err != nil {
		return apiclient.Project{}, fmt.Errorf("creating project %q: %s", suffixed, apiclient.Explain(err))
	}
	return project, nil
}

// suffixSlug appends a random 6-hex-digit suffix, trimming the base so the
// result still fits the console's 40-character project slug limit.
func suffixSlug(slug string) (string, error) {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	suffix := "-" + hex.EncodeToString(raw[:])
	base := slug
	if len(base)+len(suffix) > 40 {
		base = strings.Trim(base[:40-len(suffix)], "-")
	}
	return base + suffix, nil
}

// findProject matches by exact name first, then by display name, so both
// `--project my-api` and `--project "My API"` land on the same row.
func findProject(projects []apiclient.Project, wanted string) (apiclient.Project, bool) {
	for _, p := range projects {
		if p.Name == wanted {
			return p, true
		}
	}
	for _, p := range projects {
		if p.DisplayName == wanted {
			return p, true
		}
	}
	return apiclient.Project{}, false
}

// resolveEnvironment picks the environment to deploy into: the production one
// when the project has it, the only one when there is only one, and a prompt
// only when neither rule decides.
func resolveEnvironment(ctx context.Context, client *apiclient.Client, project apiclient.Project, in io.Reader, out io.Writer) (apiclient.Environment, error) {
	envs, err := client.GetProjectEnvironments(ctx, project.ID)
	if err != nil {
		return apiclient.Environment{}, fmt.Errorf("listing environments: %s", apiclient.Explain(err))
	}
	if len(envs) == 0 {
		return apiclient.Environment{}, fmt.Errorf("project %q has no environments", project.Name)
	}
	if len(envs) == 1 {
		return envs[0], nil
	}
	for _, e := range envs {
		if e.Name == "prod" || e.Name == "production" {
			return e, nil
		}
	}
	for _, e := range envs {
		if e.Type == "prod" {
			return e, nil
		}
	}
	return choose(in, out, "Select an environment", envs, func(e apiclient.Environment) string {
		return fmt.Sprintf("%s (%s)", e.Name, e.Type)
	})
}

// choose prints a numbered list of items and reads a 1-based selection from
// in. It is generic over T so it serves both the project and environment
// prompts with the same code.
func choose[T any](in io.Reader, out io.Writer, prompt string, items []T, label func(T) string) (T, error) {
	var zero T
	fmt.Fprintln(out, prompt+":")
	for i, item := range items {
		fmt.Fprintf(out, "  %d) %s\n", i+1, label(item))
	}
	fmt.Fprint(out, "> ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return zero, fmt.Errorf("no input received")
	}
	choice := strings.TrimSpace(scanner.Text())
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(items) {
		return zero, fmt.Errorf("invalid selection %q", choice)
	}
	return items[n-1], nil
}

// resolveAppName uses opts.AppName if the caller passed --name, otherwise
// derives it from the directory name.
func resolveAppName(opts DeployOptions) (string, error) {
	if opts.AppName != "" {
		if err := appname.Validate(opts.AppName); err != nil {
			return "", err
		}
		return opts.AppName, nil
	}

	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	normalized := appname.Normalize(base)
	if err := appname.Validate(normalized); err != nil {
		return "", fmt.Errorf("could not derive a valid app name from directory %q - pass one explicitly with --name", base)
	}
	return normalized, nil
}

// streamBuildStatus polls the build's status until it reaches a terminal
// state, printing each change, and returns the terminal status.
func streamBuildStatus(ctx context.Context, client *apiclient.Client, projectID, envID, appName, buildID string, out io.Writer) (string, error) {
	deadline := time.Now().Add(buildPollTimeout)
	last := ""

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for the build after %s", buildPollTimeout)
		}

		build, ok, err := client.LatestBuild(ctx, projectID, envID, appName)
		if err != nil {
			return "", fmt.Errorf("checking build status: %s", apiclient.Explain(err))
		}
		if ok && build.ID == buildID {
			if build.Status != last {
				fmt.Fprintf(out, "Build: %s\n", build.Status)
				last = build.Status
			}
			if apiclient.IsTerminalBuildStatus(build.Status) {
				return build.Status, nil
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(buildPollInterval):
		}
	}
}

// pollAppURL waits briefly for the platform to assign the app a live URL
// after a successful build - it is created by the same handoff, but not
// necessarily synced into the snapshot the instant the build finishes.
func pollAppURL(ctx context.Context, client *apiclient.Client, projectID, envID, appName string, out io.Writer) (string, bool, error) {
	deadline := time.Now().Add(urlPollTimeout)
	for {
		url, ok, err := client.FindAppURL(ctx, projectID, envID, appName)
		if err != nil {
			return "", false, fmt.Errorf("checking app URL: %s", apiclient.Explain(err))
		}
		if ok {
			return url, true, nil
		}
		if time.Now().After(deadline) {
			return "", false, nil
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(urlPollInterval):
		}
	}
}
