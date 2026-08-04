package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
)

// DecryptToken decrypts an AES-GCM ciphertext produced by the backend.
// keyHex is the hex-encoded 32-byte key from GITOPS_ENCRYPTION_KEY.
// ciphertext is the raw bytes from the token_encrypted column: nonce || ciphertext.
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
