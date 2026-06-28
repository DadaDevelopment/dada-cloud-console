package worker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
)

const testEncryptionKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestManagerForIntegration_UsesDefaultManagerWhenIntegrationMissing(t *testing.T) {
	w, defaultMgr := newTestWatcher(t)

	got, err := w.managerForIntegration(uuid.New(), nil)
	if err != nil {
		t.Fatalf("managerForIntegration(nil): %v", err)
	}
	if got != defaultMgr {
		t.Fatalf("managerForIntegration(nil) returned non-default manager")
	}
}

func TestManagerForIntegration_DecryptFailureDoesNotFallbackToDefaultRepo(t *testing.T) {
	w, defaultMgr := newTestWatcher(t)
	projectID := uuid.New()

	got, err := w.managerForIntegration(projectID, &db.GitIntegration{
		ProjectID:      projectID,
		Provider:       "github",
		RepoURL:        "https://example.com/project-state.git",
		Branch:         "main",
		TokenEncrypted: []byte("short"),
	})
	if err == nil {
		t.Fatal("expected decrypt error, got nil")
	}
	if !strings.Contains(err.Error(), "decrypt git integration token") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil manager on decrypt failure, got %#v", got)
	}
	if len(w.managers) != 1 || w.managers[w.cfg.DefaultRepoURL] != defaultMgr {
		t.Fatalf("decrypt failure mutated manager cache: %#v", w.managers)
	}
}

func TestManagerForIntegration_CreatesAndCachesProjectManager(t *testing.T) {
	w, _ := newTestWatcher(t)
	projectID := uuid.New()
	ciphertext, err := encryptToken(testEncryptionKey, "ghp_projecttoken")
	if err != nil {
		t.Fatalf("encryptToken: %v", err)
	}

	integration := &db.GitIntegration{
		ProjectID:      projectID,
		Provider:       "github",
		RepoURL:        "https://example.com/project-state.git",
		Branch:         "console",
		TokenEncrypted: ciphertext,
	}

	first, err := w.managerForIntegration(projectID, integration)
	if err != nil {
		t.Fatalf("first managerForIntegration: %v", err)
	}
	second, err := w.managerForIntegration(projectID, integration)
	if err != nil {
		t.Fatalf("second managerForIntegration: %v", err)
	}
	if first != second {
		t.Fatal("expected cached manager to be reused")
	}
	if first.RepoURL() != integration.RepoURL {
		t.Fatalf("RepoURL() = %q, want %q", first.RepoURL(), integration.RepoURL)
	}
	if first.Branch() != integration.Branch {
		t.Fatalf("Branch() = %q, want %q", first.Branch(), integration.Branch)
	}
}

func newTestWatcher(t *testing.T) (*DBWatcher, *git.Manager) {
	t.Helper()

	cfg := &config.Config{
		DefaultRepoURL:  "https://example.com/platform-argo-infra.git",
		DefaultBranch:   "main",
		DefaultUsername: "git",
		DefaultToken:    "token",
		RepoLocalPath:   t.TempDir(),
		EncryptionKey:   testEncryptionKey,
	}
	defaultMgr := git.New(git.RepoConfig{
		RepoURL:   cfg.DefaultRepoURL,
		Branch:    cfg.DefaultBranch,
		Username:  cfg.DefaultUsername,
		Token:     cfg.DefaultToken,
		LocalBase: cfg.RepoLocalPath,
	})
	w := &DBWatcher{
		cfg: cfg,
		managers: map[string]*git.Manager{
			cfg.DefaultRepoURL: defaultMgr,
		},
	}
	return w, defaultMgr
}

func encryptToken(keyHex string, plaintext string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}
