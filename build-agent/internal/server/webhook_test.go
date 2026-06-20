package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/config"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookPerRepoSecret(t *testing.T) {
	s := &Server{cfg: &config.Config{GitHubWebhookSecret: "global"}}
	body := []byte(`{"ref":"refs/heads/main"}`)

	// Per-repo secret takes precedence.
	if !s.verifyWebhook("repo-secret", body, sign("repo-secret", body)) {
		t.Error("valid per-repo signature rejected")
	}
	if s.verifyWebhook("repo-secret", body, sign("wrong", body)) {
		t.Error("invalid per-repo signature accepted")
	}
	// Falls back to global secret when repo secret empty.
	if !s.verifyWebhook("", body, sign("global", body)) {
		t.Error("valid global signature rejected")
	}
}

func TestVerifyWebhookNoSecretIsPermissive(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	if !s.verifyWebhook("", []byte("x"), "") {
		t.Error("with no configured secret verification should pass")
	}
}
