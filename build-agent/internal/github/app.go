// Package github mints GitHub App installation tokens and posts commit status.
package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const apiBase = "https://api.github.com"

// App is the GitHub App surface build-agent needs.
type App interface {
	// InstallToken mints a short-lived (~1h) installation token for cloning and
	// API calls scoped to one installation.
	InstallToken(ctx context.Context, installationID int64) (string, error)
	// ListRepos returns the repositories accessible to an installation (used by
	// the import wizard).
	ListRepos(ctx context.Context, installationID int64) ([]RemoteRepo, error)
	// GetInstallation resolves the org/user an installation belongs to (used by
	// the install-callback to persist git_app_installations).
	GetInstallation(ctx context.Context, installationID int64) (*InstallationAccount, error)
	// ListInstallations returns every installation of this App (used by the
	// connect wizard to bind an already-installed org without a reinstall).
	ListInstallations(ctx context.Context) ([]InstallationAccount, error)
	// PostStatus reports a commit status back to GitHub on each build-state
	// transition, with a details URL → console build page.
	PostStatus(ctx context.Context, installationID int64, repoFullName, sha, state, detailsURL, description string) error
}

// InstallationAccount identifies the org/user a GitHub App installation belongs
// to. Returned by the install-callback resolve step (the backend has no App key).
type InstallationAccount struct {
	InstallationID int64  `json:"installation_id"`
	AccountLogin   string `json:"account_login"` // GitHub org/user slug
	AccountType    string `json:"account_type"`  // "Organization" | "User"
}

// RemoteRepo is a repository accessible to an installation.
type RemoteRepo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Client is the production App. It signs an App JWT (RS256 over APP_ID + private
// key) and exchanges it for per-installation access tokens, which it caches
// until shortly before expiry.
type Client struct {
	appID  string
	appKey []byte // PEM private key bytes
	http   *http.Client

	mu     sync.Mutex
	key    *rsa.PrivateKey // parsed lazily
	tokens map[int64]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

// New returns a GitHub App client. appKey is the PEM-encoded RSA private key
// (the value of BUILD_GITHUB_APP_KEY, which may contain literal \n escapes).
func New(appID, appKey string) *Client {
	return &Client{
		appID:  appID,
		appKey: []byte(strings.ReplaceAll(appKey, `\n`, "\n")),
		http:   &http.Client{Timeout: 30 * time.Second},
		tokens: make(map[int64]cachedToken),
	}
}

// signedAppJWT mints a short-lived (10m) RS256 App JWT.
func (c *Client) signedAppJWT() (string, error) {
	c.mu.Lock()
	if c.key == nil {
		key, err := jwt.ParseRSAPrivateKeyFromPEM(c.appKey)
		if err != nil {
			c.mu.Unlock()
			return "", fmt.Errorf("parse app key: %w", err)
		}
		c.key = key
	}
	key := c.key
	c.mu.Unlock()

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock-skew slack
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

// InstallToken mints (or returns a cached) installation access token.
func (c *Client) InstallToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[installationID]; ok && time.Until(t.expires) > 2*time.Minute {
		c.mu.Unlock()
		return t.token, nil
	}
	c.mu.Unlock()

	appJWT, err := c.signedAppJWT()
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("install token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("install token: %s", readErr(resp))
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode install token: %w", err)
	}

	c.mu.Lock()
	c.tokens[installationID] = cachedToken{token: out.Token, expires: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// ListRepos returns the repositories accessible to an installation (paginated).
func (c *Client) ListRepos(ctx context.Context, installationID int64) ([]RemoteRepo, error) {
	token, err := c.InstallToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	var repos []RemoteRepo
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", apiBase, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list repos: %w", err)
		}
		var out struct {
			TotalCount   int          `json:"total_count"`
			Repositories []RemoteRepo `json:"repositories"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode repos: %w", err)
		}
		repos = append(repos, out.Repositories...)
		if len(out.Repositories) < 100 || len(repos) >= out.TotalCount {
			break
		}
	}
	return repos, nil
}

// GetInstallation resolves the account (org/user) behind an installation via the
// App JWT (GET /app/installations/{id}). No install token needed.
func (c *Client) GetInstallation(ctx context.Context, installationID int64) (*InstallationAccount, error) {
	appJWT, err := c.signedAppJWT()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/app/installations/%d", apiBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get installation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get installation: %s", readErr(resp))
	}

	var out struct {
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode installation: %w", err)
	}
	return &InstallationAccount{
		InstallationID: installationID,
		AccountLogin:   out.Account.Login,
		AccountType:    out.Account.Type,
	}, nil
}

// ListInstallations returns every installation of this App via the App JWT
// (GET /app/installations, paginated).
func (c *Client) ListInstallations(ctx context.Context) ([]InstallationAccount, error) {
	appJWT, err := c.signedAppJWT()
	if err != nil {
		return nil, err
	}

	var out []InstallationAccount
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/app/installations?per_page=100&page=%d", apiBase, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+appJWT)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list installations: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			msg := readErr(resp)
			return nil, fmt.Errorf("list installations: %s", msg)
		}
		var batch []struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		}
		err = json.NewDecoder(resp.Body).Decode(&batch)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode installations: %w", err)
		}
		for _, b := range batch {
			out = append(out, InstallationAccount{
				InstallationID: b.ID,
				AccountLogin:   b.Account.Login,
				AccountType:    b.Account.Type,
			})
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

// PostStatus posts a commit status. state ∈ {pending, success, failure, error}.
func (c *Client) PostStatus(ctx context.Context, installationID int64, repoFullName, sha, state, detailsURL, description string) error {
	token, err := c.InstallToken(ctx, installationID)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{
		"state":       state,
		"target_url":  detailsURL,
		"description": truncate(description, 140),
		"context":     "dada-cloud/build",
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/statuses/%s", apiBase, repoFullName, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("post status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("post status: %s", readErr(resp))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func readErr(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return fmt.Sprintf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

var _ App = (*Client)(nil)
