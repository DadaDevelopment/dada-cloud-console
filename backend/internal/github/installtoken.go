// Package github mints short-lived GitHub App credentials used by the
// cloud-task integration: an App JWT and an installation access token.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// apiBase is the GitHub REST API root. It is a package var so tests can point
// it at an httptest server instead of api.github.com.
var apiBase = "https://api.github.com"

// buildAppJWT signs the short-lived GitHub App JWT (iss=appID, 9-min expiry, RS256).
func buildAppJWT(appID, privateKeyPEM string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse app private key: %w", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
}

// MintInstallToken exchanges the App JWT for a short-lived installation access
// token (~1h) scoped to one installation. The token is a secret: never log it.
func MintInstallToken(ctx context.Context, appID, privateKeyPEM string, installationID int64) (string, time.Time, error) {
	appJWT, err := buildAppJWT(appID, privateKeyPEM)
	if err != nil {
		return "", time.Time{}, err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github install token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", time.Time{}, fmt.Errorf("github install token: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", time.Time{}, err
	}
	return out.Token, out.ExpiresAt, nil
}
