package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StoredToken is the on-disk shape written to ~/.config/ddc/token.json.
// ExpiresAt is computed once at save time so later reads don't need to
// re-derive it from ExpiresIn plus a stored issue time.
type StoredToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	ClientID     string    `json:"client_id"`
	Issuer       string    `json:"issuer"`
}

// Expired reports whether the access token is past its expiry, with a
// 30-second safety margin so a token that is about to expire mid-request
// isn't treated as fresh.
func (t StoredToken) Expired() bool {
	return time.Now().Add(30 * time.Second).After(t.ExpiresAt)
}

// ConfigDir returns ~/.config/ddc, creating it with 0700 permissions if it
// does not exist yet.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "ddc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

func tokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token.json"), nil
}

// SaveToken writes tok to ~/.config/ddc/token.json with 0600 permissions.
// The file is written to a temp path in the same directory first and renamed
// into place, so a crash mid-write cannot leave a half-written token file.
func SaveToken(tok StoredToken) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving token file: %w", err)
	}
	return nil
}

// LoadToken reads the cached token, returning (StoredToken{}, false, nil)
// when no token has been saved yet - not logged in is not an error.
func LoadToken() (StoredToken, bool, error) {
	path, err := tokenPath()
	if err != nil {
		return StoredToken{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StoredToken{}, false, nil
		}
		return StoredToken{}, false, fmt.Errorf("reading token file: %w", err)
	}
	var tok StoredToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return StoredToken{}, false, fmt.Errorf("token file is corrupt, run login again: %w", err)
	}
	return tok, true, nil
}

// ClearToken removes the cached token file, if any.
func ClearToken() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// FromTokenResponse converts a fresh token endpoint response into a
// StoredToken, stamping ExpiresAt from ExpiresIn.
func FromTokenResponse(tr TokenResponse, clientID, issuer string) StoredToken {
	return StoredToken{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		ClientID:     clientID,
		Issuer:       issuer,
	}
}

// RefreshAccessToken exchanges a refresh token for a new access token using
// the standard OAuth2 refresh_token grant.
func RefreshAccessToken(ep Endpoints, hc httpDoer, clientID, refreshToken string) (TokenResponse, error) {
	return doRefresh(ep, hc, clientID, refreshToken)
}
