package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UserAuth is the result of exchanging an OAuth authorization code: the GitHub
// login of the user who authorized, plus every App installation that user can
// access. The console uses this to attach an already-installed account to a
// project without a reinstall, verifying ownership via the user's own token.
type UserAuth struct {
	Login         string                `json:"login"`
	Installations []InstallationAccount `json:"installations"`
}

// oauthHTTP is a dedicated client so the token exchange (github.com, not the API
// host) does not depend on an App instance.
var oauthHTTP = &http.Client{Timeout: 20 * time.Second}

// ExchangeUserCode swaps an OAuth authorization code for a user access token,
// then lists the App installations that user can access (GET /user/installations)
// and resolves the user's login (GET /user). Ownership proof: the token is the
// user's, so only installations they truly have access to are returned.
func ExchangeUserCode(ctx context.Context, clientID, clientSecret, code string) (*UserAuth, error) {
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("github oauth not configured")
	}

	token, err := exchangeCode(ctx, clientID, clientSecret, code)
	if err != nil {
		return nil, err
	}

	login, err := userLogin(ctx, token)
	if err != nil {
		return nil, err
	}

	installs, err := userInstallations(ctx, token)
	if err != nil {
		return nil, err
	}

	return &UserAuth{Login: login, Installations: installs}, nil
}

// exchangeCode posts to github.com/login/oauth/access_token and returns the user
// access token.
func exchangeCode(ctx context.Context, clientID, clientSecret, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth exchange: %s", readErr(resp))
	}

	var out struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode oauth token: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("oauth exchange: %s: %s", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("oauth exchange: empty access token")
	}
	return out.AccessToken, nil
}

// userLogin resolves the authorizing user's GitHub login (GET /user).
func userLogin(ctx context.Context, userToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get user: %s", readErr(resp))
	}
	var out struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}
	return out.Login, nil
}

// userInstallations lists the App installations the authorizing user can access
// (GET /user/installations, paginated). Unlike /app/installations this response
// wraps the array in an "installations" field.
func userInstallations(ctx context.Context, userToken string) ([]InstallationAccount, error) {
	var out []InstallationAccount
	for page := 1; ; page++ {
		u := fmt.Sprintf("%s/user/installations?per_page=100&page=%d", apiBase, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+userToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := oauthHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list user installations: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			msg := readErr(resp)
			_ = resp.Body.Close()
			return nil, fmt.Errorf("list user installations: %s", msg)
		}
		var batch struct {
			Installations []struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"account"`
			} `json:"installations"`
		}
		err = json.NewDecoder(resp.Body).Decode(&batch)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode user installations: %w", err)
		}
		for _, b := range batch.Installations {
			out = append(out, InstallationAccount{
				InstallationID: b.ID,
				AccountLogin:   b.Account.Login,
				AccountType:    b.Account.Type,
			})
		}
		if len(batch.Installations) < 100 {
			break
		}
	}
	return out, nil
}
