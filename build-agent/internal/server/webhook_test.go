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

func TestVerifyWebhookAppSecretPriority(t *testing.T) {
	s := &Server{cfg: &config.Config{GitHubWebhookSecret: "app"}}
	body := []byte(`{"ref":"refs/heads/main"}`)

	if !s.verifyWebhook("repo-secret", body, sign("app", body)) {
		t.Error("valid App-level signature rejected")
	}
	if s.verifyWebhook("repo-secret", body, sign("repo-secret", body)) {
		t.Error("per-repo signature accepted while App-level secret configured")
	}
	if s.verifyWebhook("repo-secret", body, sign("wrong", body)) {
		t.Error("invalid signature accepted")
	}
}

func TestVerifyWebhookPerRepoFallback(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	body := []byte(`{"ref":"refs/heads/main"}`)

	if !s.verifyWebhook("repo-secret", body, sign("repo-secret", body)) {
		t.Error("valid per-repo signature rejected when App-level secret empty")
	}
	if s.verifyWebhook("repo-secret", body, sign("wrong", body)) {
		t.Error("invalid per-repo signature accepted")
	}
}

func TestVerifyWebhookNoSecretIsPermissive(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	if !s.verifyWebhook("", []byte("x"), "") {
		t.Error("with no configured secret verification should pass")
	}
}
