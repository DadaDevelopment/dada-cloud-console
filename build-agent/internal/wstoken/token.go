// Package wstoken issues and verifies short-lived HMAC-signed tokens used to
// authenticate frontend WebSocket connections directly to the build-agent.
// Copied from gitops-agent/internal/wstoken with a BuildID claim added.
// The console backend signs a token after checking canWrite(); the frontend
// passes the token in the WS handshake query-string; the agent verifies it.
package wstoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Claims are the payload embedded in a delegate token. BuildID scopes the token
// to a single build's log stream (/ws/build).
type Claims struct {
	Project string `json:"project"`
	Env     string `json:"env"`
	App     string `json:"app"`
	BuildID string `json:"build,omitempty"`
	Exp     int64  `json:"exp"` // Unix timestamp
}

// Sign encodes claims as base64(json) + "." + base64(hmac-sha256).
func Sign(secret string, c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("wstoken sign: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := computeHMAC(secret, enc)
	return enc + "." + mac, nil
}

// Verify checks the signature and expiry, returning the decoded Claims.
func Verify(secret, token string) (Claims, error) {
	dot := strings.LastIndex(token, ".")
	if dot < 0 {
		return Claims{}, fmt.Errorf("wstoken: invalid format")
	}
	enc, mac := token[:dot], token[dot+1:]

	if expected := computeHMAC(secret, enc); !hmac.Equal([]byte(mac), []byte(expected)) {
		return Claims{}, fmt.Errorf("wstoken: invalid signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Claims{}, fmt.Errorf("wstoken: decode: %w", err)
	}

	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, fmt.Errorf("wstoken: unmarshal: %w", err)
	}

	if time.Now().Unix() > c.Exp {
		return Claims{}, fmt.Errorf("wstoken: expired")
	}
	return c, nil
}

func computeHMAC(secret, data string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
