package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifySignature validates a GitHub webhook HMAC (X-Hub-Signature-256).
// Copied verbatim from gitops-agent/internal/server/server.go:137 — real,
// constant-time compare. Each repo verifies against its own webhook_secret.
func VerifySignature(secret string, body []byte, sig string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expected)
}
