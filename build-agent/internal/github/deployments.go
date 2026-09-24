package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DeploymentRequest is the GitHub Deployment opened for one build. RequiredContexts
// is always sent empty: GitHub otherwise refuses the deployment (409) while any
// commit status on the sha is pending, and our own dada-cloud/build status is
// pending for exactly as long as the build runs.
type DeploymentRequest struct {
	Ref         string
	Environment string
	Production  bool
	Description string
}

// DeploymentStatus is one state transition of a GitHub Deployment. State is one
// of queued, in_progress, success, failure, error, inactive. LogURL points at the
// console build page, EnvironmentURL at the live app and may be empty.
type DeploymentStatus struct {
	State          string
	LogURL         string
	EnvironmentURL string
	Description    string
}

// CreateDeployment opens a GitHub Deployment via POST /repos/{repo}/deployments
// and returns its id. This is what fills the repository's Deployments tab and
// the environment badge on pull requests, the same surface Vercel writes to.
func (c *Client) CreateDeployment(ctx context.Context, installationID int64, repoFullName string, d DeploymentRequest) (int64, error) {
	body := map[string]any{
		"ref":                    d.Ref,
		"environment":            d.Environment,
		"production_environment": d.Production,
		"transient_environment":  false,
		"auto_merge":             false,
		"required_contexts":      []string{},
		"description":            truncate(d.Description, 140),
	}
	var out struct {
		ID int64 `json:"id"`
	}
	url := fmt.Sprintf("%s/repos/%s/deployments", apiBase, repoFullName)
	if err := c.postJSON(ctx, installationID, url, body, &out); err != nil {
		return 0, fmt.Errorf("create deployment: %w", err)
	}
	if out.ID == 0 {
		return 0, fmt.Errorf("create deployment: github returned no id")
	}
	return out.ID, nil
}

// PostDeploymentStatus moves a GitHub Deployment to a new state via
// POST /repos/{repo}/deployments/{id}/statuses. auto_inactive is left at its
// default (true), so a success here greys out the previous deployment of the
// same environment, which is how the tab keeps exactly one Active row.
func (c *Client) PostDeploymentStatus(ctx context.Context, installationID int64, repoFullName string, deploymentID int64, s DeploymentStatus) error {
	body := map[string]any{
		"state":       s.State,
		"description": truncate(s.Description, 140),
	}
	if s.LogURL != "" {
		body["log_url"] = s.LogURL
	}
	if s.EnvironmentURL != "" {
		body["environment_url"] = s.EnvironmentURL
	}
	url := fmt.Sprintf("%s/repos/%s/deployments/%d/statuses", apiBase, repoFullName, deploymentID)
	if err := c.postJSON(ctx, installationID, url, body, nil); err != nil {
		return fmt.Errorf("post deployment status: %w", err)
	}
	return nil
}

// postJSON sends one authenticated App-installation POST and decodes the reply
// into out when out is non-nil.
func (c *Client) postJSON(ctx context.Context, installationID int64, url string, body any, out any) error {
	token, err := c.InstallToken(ctx, installationID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s", readErr(resp))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
