// Package grafanaembed implements backend-mediated authentication for the
// Grafana dashboards the console embeds in an iframe (ADR-012 follow-up).
//
// The console never lets the browser authenticate to Grafana directly. Instead
// the API mints a short-lived, HMAC-signed embed token scoped to one console
// user and one dashboard; the iframe carries it on its src URL. A reverse-proxy
// gateway sitting in front of grafana.dada-tuda.ru verifies the token, promotes
// it to a first-party session cookie, and injects Grafana auth.proxy identity
// headers (X-WEBAUTH-USER / -EMAIL) on the upstream request. Grafana (auth.proxy
// enabled, whitelist = gateway pod CIDR) trusts those headers and authenticates
// the request as the console user — no manual Grafana login. Cross-tenant
// isolation is per-USER Grafana folder ACL (the console grants each member View on
// their own project folders): Grafana OSS has no Enterprise Team Sync, so we do
// NOT rely on group→team mapping.
//
// Token format is a compact, self-contained `<base64url(payload)>.<base64url(mac)>`
// (a minimal JWS-like envelope; we keep our own so the gateway needs only the
// shared secret, no JWKS round-trip). The payload is signed, never encrypted —
// it carries no secrets, only the identity the gateway will assert and the
// dashboard scope.
package grafanaembed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims is the signed embed-token payload. Field names are short to keep the
// URL compact. Groups is reserved/forward-compatible (X-WEBAUTH-GROUPS team-sync
// is Enterprise-only and unused on OSS, where isolation is per-user folder ACL);
// it is kept so the gateway can assert groups if a future Enterprise Grafana
// enables Team Sync, without a token-format change.
type Claims struct {
	User      string   `json:"u"`           // X-WEBAUTH-USER (console username; stable Grafana login)
	Email     string   `json:"e,omitempty"` // X-WEBAUTH-EMAIL
	Groups    []string `json:"g,omitempty"` // X-WEBAUTH-GROUPS (reserved; unused on OSS)
	Dashboard string   `json:"d,omitempty"` // dashboard UID this token is scoped to
	ExpiresAt int64    `json:"x"`           // unix seconds
}

// Errors returned by Verify. Callers map all of them to 401/403 — they must not
// distinguish "expired" from "bad signature" to the browser.
var (
	ErrMalformed = errors.New("grafanaembed: malformed token")
	ErrSignature = errors.New("grafanaembed: bad signature")
	ErrExpired   = errors.New("grafanaembed: token expired")
)

var b64 = base64.RawURLEncoding

// Sign mints a token for the given claims, signed with secret. exp is stamped
// from now+ttl; the caller's Claims.ExpiresAt is ignored and overwritten so a
// caller can never mint a long-lived token by mistake. now is injected for
// deterministic tests.
func Sign(secret []byte, c Claims, now time.Time, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("grafanaembed: empty secret")
	}
	if c.User == "" {
		return "", errors.New("grafanaembed: empty user")
	}
	c.ExpiresAt = now.Add(ttl).Unix()
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(payload)
	mac := sign(secret, body)
	return body + "." + mac, nil
}

// Verify parses and authenticates a token against secret at time now. It returns
// the claims only when the signature is valid AND the token is unexpired.
func Verify(secret []byte, token string, now time.Time) (*Claims, error) {
	if len(secret) == 0 {
		return nil, errors.New("grafanaembed: empty secret")
	}
	body, mac, ok := strings.Cut(token, ".")
	if !ok || body == "" || mac == "" {
		return nil, ErrMalformed
	}
	// Constant-time compare against the expected MAC; reject before decoding the
	// payload so an attacker can't probe payload parsing with forged tokens.
	expected := sign(secret, body)
	if !hmac.Equal([]byte(mac), []byte(expected)) {
		return nil, ErrSignature
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return nil, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrMalformed
	}
	if c.User == "" {
		return nil, ErrMalformed
	}
	if now.Unix() >= c.ExpiresAt {
		return nil, ErrExpired
	}
	return &c, nil
}

// sign returns the base64url HMAC-SHA256 of body under secret.
func sign(secret []byte, body string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(body))
	return b64.EncodeToString(m.Sum(nil))
}

// GroupsHeader renders the Groups claim as the comma-separated value Grafana
// auth.proxy expects for a multi-value header mapped to team sync.
func (c *Claims) GroupsHeader() string {
	return strings.Join(c.Groups, ",")
}

// String is a redacted form for logs (never log the MAC or full token).
func (c *Claims) String() string {
	return fmt.Sprintf("embed{user=%s dash=%s groups=%d exp=%d}", c.User, c.Dashboard, len(c.Groups), c.ExpiresAt)
}
