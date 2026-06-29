// Package buildagent is a lean proxy client for the build-agent's GitHub/GitLab
// integration endpoints (listing installation repositories, framework detection).
//
// The build-agent owns all git-provider credentials (GitHub App private key,
// install-token minting). The console backend never talks to GitHub directly —
// it proxies through the agent so secrets stay in one place. Returns nil when
// unconfigured so callers can treat the feature as disabled (503).
package buildagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a read-only proxy client for the build-agent HTTP API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a build-agent proxy client. Returns nil if unconfigured so callers
// can treat git-provider proxying as disabled.
func New(baseURL string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) getJSON(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("build-agent GET %s: status %d: %s", path, resp.StatusCode, string(b))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// RemoteRepo mirrors the frontend GitRemoteRepo shape.
type RemoteRepo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Description   string `json:"description,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

// ListInstallationRepos proxies GET /github/installations/:id/repos on the agent.
func (c *Client) ListInstallationRepos(ctx context.Context, installationID int64) ([]RemoteRepo, error) {
	var out struct {
		Repos []RemoteRepo `json:"repos"`
	}
	path := fmt.Sprintf("/github/installations/%d/repos", installationID)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Repos, nil
}

// InstallationAccount mirrors the agent's account-resolve shape.
type InstallationAccount struct {
	InstallationID int64  `json:"installation_id"`
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type"`
}

// ListAppInstallations proxies GET /github/app/installations on the agent —
// every installation of the App, used to bind an already-installed org.
func (c *Client) ListAppInstallations(ctx context.Context) ([]InstallationAccount, error) {
	var out struct {
		Installations []InstallationAccount `json:"installations"`
	}
	if err := c.getJSON(ctx, "/github/app/installations", &out); err != nil {
		return nil, err
	}
	return out.Installations, nil
}

// GetInstallationAccount proxies GET /github/installations/:id/account on the
// agent (the agent holds the App key; the backend only has the DB).
func (c *Client) GetInstallationAccount(ctx context.Context, installationID int64) (*InstallationAccount, error) {
	path := fmt.Sprintf("/github/installations/%d/account", installationID)
	var out InstallationAccount
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FrameworkDetection mirrors the frontend FrameworkDetection shape.
type FrameworkDetection struct {
	Framework      *string `json:"framework"`
	PackageManager *string `json:"package_manager"`
	BuildCommand   *string `json:"build_command"`
	InstallCommand *string `json:"install_command"`
	StartCommand   *string `json:"start_command"`
	OutputDir      *string `json:"output_dir"`
	Port           *int    `json:"port"`
}

// DetectFramework proxies GET /github/detect on the agent.
func (c *Client) DetectFramework(ctx context.Context, installationID int64, repoFullName, rootDir string) (*FrameworkDetection, error) {
	q := url.Values{}
	q.Set("repo", repoFullName)
	q.Set("root_dir", rootDir)
	path := fmt.Sprintf("/github/installations/%d/detect?%s", installationID, q.Encode())
	var out FrameworkDetection
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
