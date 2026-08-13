// Package auth implements the OAuth 2.0 device authorization grant
// (RFC 8628) against Keycloak, plus on-disk storage of the resulting tokens.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DeviceCodeResponse is Keycloak's response to the device authorization
// request, per RFC 8628 section 3.2.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// TokenResponse is a successful token endpoint response.
type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// tokenErrorResponse is the RFC 6749 section 5.2 error shape the token
// endpoint returns for both device-flow polling errors (authorization_pending,
// slow_down, expired_token, access_denied) and ordinary grant failures.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Endpoints holds the two Keycloak URLs the device flow needs, derived from
// the realm issuer.
type Endpoints struct {
	DeviceAuthURL string
	TokenURL      string
}

// EndpointsFromIssuer builds Endpoints from a realm issuer URL such as
// "https://id.dada-tuda.ru/realms/master".
func EndpointsFromIssuer(issuer string) Endpoints {
	issuer = strings.TrimSuffix(issuer, "/")
	return Endpoints{
		DeviceAuthURL: issuer + "/protocol/openid-connect/auth/device",
		TokenURL:      issuer + "/protocol/openid-connect/token",
	}
}

// RequiredScopes are the OAuth2 scopes ddc must request explicitly on the
// device authorization request. The ddc-cli Keycloak client's default scope
// is "read builds:read" only; "deploy:write" is optional and must be asked
// for by name, or the source-archive upload later fails with 403.
var RequiredScopes = []string{"read", "builds:read", "deploy:write"}

// JoinScopes renders scopes as the space-separated string the OAuth2 "scope"
// parameter expects.
func JoinScopes(scopes []string) string {
	return strings.Join(scopes, " ")
}

// ParseScopes splits a space-separated OAuth2 scope string back into its
// individual scope names, ignoring any extra whitespace.
func ParseScopes(s string) []string {
	return strings.Fields(s)
}

// StartDeviceAuth requests a device code from Keycloak for clientID, asking
// for scope (space-separated, see RequiredScopes/JoinScopes).
func StartDeviceAuth(ctx context.Context, hc *http.Client, ep Endpoints, clientID, scope string) (*DeviceCodeResponse, error) {
	form := url.Values{"client_id": {clientID}}
	if scope != "" {
		form.Set("scope", scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.DeviceAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting login server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login server rejected device request (status %d) - check that client %q allows device flow", resp.StatusCode, clientID)
	}

	var dc DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("login server sent an unreadable response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, errors.New("login server response was missing the device or user code")
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// pollOutcome is the result of classifying one token-endpoint response
// during device-flow polling, factored out from the HTTP call so the retry
// and backoff rules can be unit tested without a real server or real sleeps.
type pollOutcome struct {
	retry        bool
	nextInterval int
	terminal     error
}

// classifyPollResponse decides what a poller should do next given the HTTP
// status, the parsed error code (empty on success), and the current poll
// interval. It implements RFC 8628 section 3.5: authorization_pending means
// "keep polling at the same interval", slow_down means "add 5 seconds and
// keep polling", and every other error is terminal.
func classifyPollResponse(status int, errCode string, currentInterval int) pollOutcome {
	if status == http.StatusOK {
		return pollOutcome{}
	}
	switch errCode {
	case "authorization_pending":
		return pollOutcome{retry: true, nextInterval: currentInterval}
	case "slow_down":
		return pollOutcome{retry: true, nextInterval: currentInterval + 5}
	case "expired_token":
		return pollOutcome{terminal: errors.New("the login code expired before you finished signing in - run the command again")}
	case "access_denied":
		return pollOutcome{terminal: errors.New("sign-in was denied")}
	case "":
		return pollOutcome{terminal: fmt.Errorf("login server returned status %d", status)}
	default:
		return pollOutcome{terminal: fmt.Errorf("login failed: %s", errCode)}
	}
}

// PollToken polls the token endpoint until the user completes sign-in, the
// device code expires, or a terminal error occurs. It blocks the caller
// thread using time.Sleep between attempts, honoring interval and any
// slow_down backoff the server requests.
func PollToken(ctx context.Context, hc *http.Client, ep Endpoints, clientID string, dc *DeviceCodeResponse) (*TokenResponse, error) {
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	interval := dc.Interval

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("the login code expired before you finished signing in - run the command again")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		tok, status, errCode, err := doTokenPoll(ctx, hc, ep, clientID, dc.DeviceCode)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return tok, nil
		}

		outcome := classifyPollResponse(status, errCode, interval)
		if outcome.terminal != nil {
			return nil, outcome.terminal
		}
		interval = outcome.nextInterval
	}
}

func doTokenPoll(ctx context.Context, hc *http.Client, ep Endpoints, clientID, deviceCode string) (*TokenResponse, int, string, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("contacting login server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var tok TokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return nil, 0, "", fmt.Errorf("login server sent an unreadable token response: %w", err)
		}
		return &tok, resp.StatusCode, "", nil
	}

	var te tokenErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&te)
	return nil, resp.StatusCode, te.Error, nil
}

// FormatExpiresIn renders a device-code expiry as whole minutes, for the
// message shown to the user while they complete sign-in.
func FormatExpiresIn(seconds int) string {
	minutes := seconds / 60
	if minutes <= 0 {
		return strconv.Itoa(seconds) + "s"
	}
	return strconv.Itoa(minutes) + " min"
}
