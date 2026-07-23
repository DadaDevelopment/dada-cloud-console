package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// EncryptToken encrypts plaintext with AES-256-GCM, producing the same wire
// format DecryptToken consumes: nonce(12) || ciphertext||tag. Copied verbatim
// from gitops-agent/internal/crypto (see that file's comment for why this is a
// copy, not a cross-module import). Used to re-encrypt an env_vars value after
// rewriting its embedded database name for a preview environment (see
// copyPreviewEnvVars in this package).
func EncryptToken(keyHex, plaintext string) ([]byte, error) {
	if keyHex == "" {
		return nil, fmt.Errorf("encryption key not configured")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decoding encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// DecryptToken decrypts an AES-GCM ciphertext produced by the backend.
// Copied verbatim from gitops-agent/internal/crypto so build-agent shares the
// exact same format: nonce(12) || aes-256-gcm(plaintext), key = hex 32 bytes
// from GITOPS_ENCRYPTION_KEY. Used for GitLab PATs + encrypted env-vars.
//
// Kept as a copy (not a cross-module import) so build-agent builds standalone;
// the plan (§4) explicitly allows "re-export/import OR copy".
func DecryptToken(keyHex string, ciphertext []byte) (string, error) {
	if keyHex == "" {
		return "", fmt.Errorf("encryption key not configured")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", fmt.Errorf("decoding encryption key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, data := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting token: %w", err)
	}
	return string(plain), nil
}
