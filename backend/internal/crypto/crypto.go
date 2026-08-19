package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrKeyMisconfigured marks every failure that comes from GITOPS_ENCRYPTION_KEY
// itself rather than from the payload: absent, non-hex, or the wrong length.
// Such a failure is permanent for the lifetime of the process, so retry loops
// must stop on it instead of replaying the same broken config. Named after the
// 2026-08-19 outage, where a trailing newline in the Secret made
// deliverDatabaseDSNAsync burn 172 identical attempts in 28 minutes.
var ErrKeyMisconfigured = errors.New("encryption key misconfigured")

// decodeKey turns the configured hex key into raw AES-256 key bytes, tagging
// every key-shaped rejection with ErrKeyMisconfigured.
func decodeKey(keyHex string) ([]byte, error) {
	keyHex = strings.TrimSpace(keyHex)
	if keyHex == "" {
		return nil, fmt.Errorf("%w: encryption key not configured", ErrKeyMisconfigured)
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding encryption key: %w", ErrKeyMisconfigured, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: encryption key must be 32 bytes, got %d", ErrKeyMisconfigured, len(key))
	}
	return key, nil
}

// EncryptToken encrypts plaintext using AES-256-GCM.
// keyHex is the hex-encoded 32-byte key from GITOPS_ENCRYPTION_KEY.
// Output format: nonce(12) || aes-256-gcm(plaintext) — matches gitops-agent DecryptToken wire format.
func EncryptToken(keyHex string, plaintext []byte) ([]byte, error) {
	key, err := decodeKey(keyHex)
	if err != nil {
		return nil, err
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

	// Seal appends ciphertext+tag to nonce, producing nonce||ciphertext.
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptToken decrypts an AES-GCM ciphertext produced by EncryptToken (or the gitops-agent backend).
// keyHex is the hex-encoded 32-byte key from GITOPS_ENCRYPTION_KEY.
// ciphertext is the raw bytes: nonce(12) || aes-256-gcm(plaintext).
func DecryptToken(keyHex string, ciphertext []byte) ([]byte, error) {
	key, err := decodeKey(keyHex)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, data := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting token: %w", err)
	}
	return plain, nil
}
