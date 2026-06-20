package crypto_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"testing"

	"github.com/dada-tuda/console/backend/internal/crypto"
)

const testKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// gitopsEncrypt replicates the gitops-agent test-helper encrypt so we can verify
// cross-compatibility: backend DecryptToken must handle gitops-agent output and vice-versa.
func gitopsEncrypt(keyHex string, plaintext string) ([]byte, error) {
	key, _ := hex.DecodeString(keyHex)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// TestRoundTrip verifies EncryptToken → DecryptToken is identity.
func TestRoundTrip(t *testing.T) {
	plain := []byte("ghp_supersecrettoken123")

	ct, err := crypto.EncryptToken(testKeyHex, plain)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}

	got, err := crypto.DecryptToken(testKeyHex, ct)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("round-trip: got %q, want %q", got, plain)
	}
}

// TestCrossCompat verifies that ciphertext produced with the gitops-agent wire format
// (nonce || gcm-sealed) is decryptable by backend DecryptToken — same algorithm, same key.
func TestCrossCompat(t *testing.T) {
	plain := "cross-compat-token-value"

	ct, err := gitopsEncrypt(testKeyHex, plain)
	if err != nil {
		t.Fatalf("gitopsEncrypt: %v", err)
	}

	got, err := crypto.DecryptToken(testKeyHex, ct)
	if err != nil {
		t.Fatalf("DecryptToken on gitops-produced ciphertext: %v", err)
	}
	if string(got) != plain {
		t.Errorf("cross-compat: got %q, want %q", got, plain)
	}
}

// TestErrorPaths covers bad-key and short-ciphertext error conditions.
func TestErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		fn      func() error
		wantErr bool
	}{
		{
			name:    "encrypt empty key",
			fn:      func() error { _, err := crypto.EncryptToken("", []byte("x")); return err },
			wantErr: true,
		},
		{
			name:    "encrypt bad hex key",
			fn:      func() error { _, err := crypto.EncryptToken("not-hex!!", []byte("x")); return err },
			wantErr: true,
		},
		{
			name: "encrypt wrong key length",
			fn: func() error {
				_, err := crypto.EncryptToken("deadbeef", []byte("x")) // 4 bytes, not 32
				return err
			},
			wantErr: true,
		},
		{
			name:    "decrypt empty key",
			fn:      func() error { _, err := crypto.DecryptToken("", []byte("anything")); return err },
			wantErr: true,
		},
		{
			name:    "decrypt bad hex key",
			fn:      func() error { _, err := crypto.DecryptToken("not-hex!!", []byte("anything")); return err },
			wantErr: true,
		},
		{
			name: "decrypt wrong key length",
			fn: func() error {
				_, err := crypto.DecryptToken("deadbeef", []byte("anything")) // 4 bytes, not 32
				return err
			},
			wantErr: true,
		},
		{
			name: "decrypt ciphertext too short",
			fn: func() error {
				_, err := crypto.DecryptToken(testKeyHex, []byte("short"))
				return err
			},
			wantErr: true,
		},
		{
			name: "decrypt wrong key",
			fn: func() error {
				wrongKey := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				ct, _ := crypto.EncryptToken(testKeyHex, []byte("token"))
				_, err := crypto.DecryptToken(wrongKey, ct)
				return err
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
