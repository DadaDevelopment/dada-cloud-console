package promptsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIBase is the GitHub REST root; tests point it at an httptest server.
var APIBase = "https://api.github.com"

// Fetcher reads one repository with an installation token.
type Fetcher struct {
	Token  string
	Client *http.Client
}

// HeadSHA resolves a branch, tag or sha to the commit sha it points at.
func (f Fetcher) HeadSHA(ctx context.Context, repoFullName, ref string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	if err := f.getJSON(ctx, fmt.Sprintf("/repos/%s/commits/%s", repoFullName, url.PathEscape(ref)), &out); err != nil {
		return "", err
	}
	if out.SHA == "" {
		return "", fmt.Errorf("ref %s has no commit sha", ref)
	}
	return out.SHA, nil
}

// Fetch downloads core.md and domains/*.md under dir at sha and returns them
// keyed by the path relative to dir.
func (f Fetcher) Fetch(ctx context.Context, repoFullName, sha, dir string) (map[string][]byte, error) {
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int    `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := f.getJSON(ctx, fmt.Sprintf("/repos/%s/git/trees/%s?recursive=1", repoFullName, sha), &tree); err != nil {
		return nil, err
	}
	if tree.Truncated {
		return nil, fmt.Errorf("repository tree is too large for one listing; move the agent directory to a smaller repository")
	}
	prefix := strings.Trim(dir, "/") + "/"
	files := map[string][]byte{}
	found := false
	for _, entry := range tree.Tree {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		found = true
		rel := strings.TrimPrefix(entry.Path, prefix)
		if entry.Type != "blob" || !WantedPath(rel) {
			continue
		}
		content, err := f.blob(ctx, repoFullName, entry.SHA)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Path, err)
		}
		files[rel] = content
	}
	if !found {
		return nil, fmt.Errorf("directory %s does not exist at %s", strings.Trim(dir, "/"), sha[:min(7, len(sha))])
	}
	return files, nil
}

func (f Fetcher) blob(ctx context.Context, repoFullName, sha string) ([]byte, error) {
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := f.getJSON(ctx, fmt.Sprintf("/repos/%s/git/blobs/%s", repoFullName, sha), &out); err != nil {
		return nil, err
	}
	if out.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected blob encoding %q", out.Encoding)
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
}

func (f Fetcher) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+f.Token)
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf("github: %s not found (check the repository, branch and that the app installation can read it)", path)
	default:
		return fmt.Errorf("github: %s returned %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}
