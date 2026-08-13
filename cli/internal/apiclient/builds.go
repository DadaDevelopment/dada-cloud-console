package apiclient

import (
	"context"
	"time"
)

// Build mirrors the subset of the console's build record the CLI streams
// status from (backend/internal/api/builds.go's build struct).
type Build struct {
	ID           string     `json:"id"`
	AppName      string     `json:"app_name"`
	Status       string     `json:"status"`
	CommitSHA    string     `json:"commit_sha"`
	LogsRef      *string    `json:"logs_ref,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	FailReason   *string    `json:"fail_reason,omitempty"`
}

// StatusSuccess is the one build status that means the image was built and
// pushed. The full vocabulary is queued | detecting | building | pushing |
// success | failed | canceled (build-agent/internal/db/builds.go:14) - note
// "success", not "succeeded".
const StatusSuccess = "success"

// terminalBuildStatuses are the end states of that vocabulary. Anything else,
// including a status a later platform version introduces, is treated as still
// in flight: an unknown status that is actually terminal costs the caller its
// polling timeout, while an unknown status wrongly called terminal reports a
// live build as a failed one - which is exactly what an earlier version of
// this list did to "pushing".
var terminalBuildStatuses = map[string]bool{
	StatusSuccess: true,
	"failed":      true,
	"canceled":    true,
	"cancelled":   true,
}

// IsTerminalBuildStatus reports whether status means the build is done
// (successfully or not) and the poller should stop.
func IsTerminalBuildStatus(status string) bool {
	return terminalBuildStatuses[status]
}

type listBuildsResponse struct {
	Builds []Build `json:"builds"`
}

// ListBuilds returns build history for an app, most recent first, per
// GET .../apps/:appName/builds.
func (c *Client) ListBuilds(ctx context.Context, projectID, envID, appName string) ([]Build, error) {
	var out listBuildsResponse
	path := "/projects/" + projectID + "/environments/" + envID + "/apps/" + appName + "/builds"
	if err := c.doJSON(ctx, "GET", path, nil, "", &out); err != nil {
		return nil, err
	}
	return out.Builds, nil
}

// LatestBuild returns the most recent build for appName, or ok=false if none
// exists yet.
func (c *Client) LatestBuild(ctx context.Context, projectID, envID, appName string) (b Build, ok bool, err error) {
	builds, err := c.ListBuilds(ctx, projectID, envID, appName)
	if err != nil {
		return Build{}, false, err
	}
	if len(builds) == 0 {
		return Build{}, false, nil
	}
	return builds[0], true, nil
}
