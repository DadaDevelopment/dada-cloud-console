package cliapp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/cli/internal/agentmarker"
	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/appname"
	"github.com/dada-tuda/console/cli/internal/archive"
)

// DeployOptions are the resolved inputs to a single `ddc deploy` run.
type DeployOptions struct {
	Dir     string
	AppName string
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
	client := apiclient.New(cfg.APIBase, &http.Client{}, TokenSource(cfg), agentmarker.DetectFromEnv())

	projectID, envID, err := resolveTarget(ctx, client, in, out)
	if err != nil {
		return err
	}

	appName, err := resolveAppName(opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "App: %s\n", appName)

	entries, total, err := archive.Plan(opts.Dir)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", opts.Dir, err)
	}
	if total > archive.MaxBytes {
		return fmt.Errorf("this project is %.1fMB, over the console's %dMB upload limit - "+
			"trim large files or add them to .gitignore and try again",
			float64(total)/1024/1024, archive.MaxBytes/1024/1024)
	}
	fmt.Fprintf(out, "Packaging %d files (%.1fMB)...\n", len(entries), float64(total)/1024/1024)

	data, err := archive.Build(opts.Dir, entries)
	if err != nil {
		return fmt.Errorf("building archive: %w", err)
	}

	fmt.Fprintln(out, "Uploading...")
	uploadResp, err := client.UploadSourceArchive(ctx, projectID, envID, appName, appName+".tar.gz", data)
	if err != nil {
		return fmt.Errorf("upload failed: %s", apiclient.Explain(err))
	}
	fmt.Fprintf(out, "Detected: %s (port %d)\n", nonEmpty(uploadResp.Detected.Framework, "unknown"), uploadResp.Detected.Port)
	fmt.Fprintf(out, "Build queued: %s\n", uploadResp.Build.ID)

	finalStatus, err := streamBuildStatus(ctx, client, projectID, envID, appName, uploadResp.Build.ID, out)
	if err != nil {
		return err
	}
	if finalStatus != "succeeded" {
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

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// resolveTarget picks a project and environment. With exactly one of each
// visible to the caller, it picks silently; otherwise it prompts.
func resolveTarget(ctx context.Context, client *apiclient.Client, in io.Reader, out io.Writer) (projectID, envID string, err error) {
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return "", "", fmt.Errorf("listing projects: %s", apiclient.Explain(err))
	}
	if len(projects) == 0 {
		return "", "", fmt.Errorf("no projects visible to your account - create one in the console first")
	}

	var project apiclient.Project
	if len(projects) == 1 {
		project = projects[0]
	} else {
		project, err = choose(in, out, "Select a project", projects, func(p apiclient.Project) string {
			return fmt.Sprintf("%s (%s)", nonEmpty(p.DisplayName, p.Name), p.Name)
		})
		if err != nil {
			return "", "", err
		}
	}

	envs, err := client.GetProjectEnvironments(ctx, project.ID)
	if err != nil {
		return "", "", fmt.Errorf("listing environments: %s", apiclient.Explain(err))
	}
	if len(envs) == 0 {
		return "", "", fmt.Errorf("project %q has no environments", project.Name)
	}

	var env apiclient.Environment
	if len(envs) == 1 {
		env = envs[0]
	} else {
		env, err = choose(in, out, "Select an environment", envs, func(e apiclient.Environment) string {
			return fmt.Sprintf("%s (%s)", e.Name, e.Type)
		})
		if err != nil {
			return "", "", err
		}
	}

	return project.ID, env.ID, nil
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

var _ = os.Stdin
