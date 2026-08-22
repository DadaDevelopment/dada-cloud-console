package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// DecryptToken decrypts an AES-GCM ciphertext produced by the backend.
// keyHex is the hex-encoded 32-byte key from GITOPS_ENCRYPTION_KEY.
// ciphertext is the raw bytes from the token_encrypted column: nonce || ciphertext.
func DecryptToken(keyHex string, ciphertext []byte) (string, error) {
	keyHex = strings.TrimSpace(keyHex)
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

// EncryptToken produces the ciphertext shape DecryptToken and the backend both
// read: nonce || aes-256-gcm(plaintext). The gitops-agent needs it because
// adoption writes env_vars rows itself -- the values it stores come out of git,
// where the console can see them, but env_vars.value_encrypted is encrypted for
// every row regardless of sensitivity, so an adopted row has to look exactly
// like one the backend wrote.
func EncryptToken(keyHex string, plaintext []byte) ([]byte, error) {
	keyHex = strings.TrimSpace(keyHex)
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
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
