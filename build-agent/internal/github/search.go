package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SearchHit is one public repository returned by a repository search.
//
// Everything here is what a person needs to decide whether this is the project
// they meant: the name, one line about it, how many people starred it, and
// whether it is archived. Stars are not a quality claim, they are the only
// ordering signal a stranger can read at a glance; Archived is here because
// deploying a dead project is a specific kind of bad afternoon.
type SearchHit struct {
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Stars         int    `json:"stars"`
	DefaultBranch string `json:"default_branch"`
	AvatarURL     string `json:"avatar_url"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	License       string `json:"license"`
	HTMLURL       string `json:"html_url"`
}

// searchMaxLimit bounds one page. The console shows a handful of suggestions
// under an input; asking GitHub for more is bandwidth spent on rows nobody
// scrolls to.
const searchMaxLimit = 10

// SearchRepos searches public GitHub repositories by free text.
//
// Authentication is best-effort by design. GitHub's search endpoint allows 30
// requests a minute with a token and 10 without, and both numbers are per
// source IP for the whole cluster — so the client mints an installation token
// when the App has any installation, and falls back to anonymous rather than
// failing. An interactive search that goes dark because a GitHub App
// installation was removed would be a strange way to lose the feature.
//
// The query is fenced to public, non-fork repositories: a fork is almost never
// what someone typing a product name meant, and a private repository we cannot
// clone is a result that only exists to disappoint.
func (c *Client) SearchRepos(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []SearchHit{}, nil
	}
	if limit <= 0 || limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	endpoint := fmt.Sprintf("%s/search/repositories?q=%s&sort=stars&order=desc&per_page=%d",
		apiBase, url.QueryEscape(q+" fork:false is:public"), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := c.searchToken(ctx); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search repos: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search repos: %s", readErr(resp))
	}

	var out struct {
		Items []struct {
			FullName      string `json:"full_name"`
			Description   string `json:"description"`
			Stars         int    `json:"stargazers_count"`
			DefaultBranch string `json:"default_branch"`
			Archived      bool   `json:"archived"`
			Fork          bool   `json:"fork"`
			HTMLURL       string `json:"html_url"`
			Owner         struct {
				AvatarURL string `json:"avatar_url"`
			} `json:"owner"`
			License struct {
				SpdxID string `json:"spdx_id"`
			} `json:"license"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}

	hits := make([]SearchHit, 0, len(out.Items))
	for _, it := range out.Items {
		hits = append(hits, SearchHit{
			FullName:      it.FullName,
			Description:   truncate(it.Description, 200),
			Stars:         it.Stars,
			DefaultBranch: it.DefaultBranch,
			AvatarURL:     it.Owner.AvatarURL,
			Archived:      it.Archived,
			Fork:          it.Fork,
			License:       it.License.SpdxID,
			HTMLURL:       it.HTMLURL,
		})
	}
	return hits, nil
}

// searchToken returns an installation token to search with, or "" to search
// anonymously.
//
// Any installation will do: repository search over public repositories returns
// the same public index whichever installation asks, and the token is here for
// the rate limit rather than for access. Every failure path returns "" — a
// slower anonymous search is a better outcome than no search.
func (c *Client) searchToken(ctx context.Context) string {
	if c.appID == "" || len(c.appKey) == 0 {
		return ""
	}
	insts, err := c.ListInstallations(ctx)
	if err != nil || len(insts) == 0 {
		return ""
	}
	token, err := c.InstallToken(ctx, insts[0].InstallationID)
	if err != nil {
		return ""
	}
	return token
}
