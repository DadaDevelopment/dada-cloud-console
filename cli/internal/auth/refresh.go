package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// httpDoer is the subset of *http.Client used here, so tests can supply a
// fake transport without spinning up a real listener.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// doRefresh performs the OAuth2 refresh_token grant against the token
// endpoint. A refresh failure (expired or revoked refresh token) is reported
// with a message telling the user to log in again rather than raw HTTP
// status text.
func doRefresh(ep Endpoints, hc httpDoer, clientID, refreshToken string) (TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	req, err := http.NewRequest(http.MethodPost, ep.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := hc.Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("contacting login server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, fmt.Errorf("session expired, run 'ddc login' again")
	}

	var tok TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return TokenResponse{}, fmt.Errorf("login server sent an unreadable token response: %w", err)
	}
	return tok, nil
}
