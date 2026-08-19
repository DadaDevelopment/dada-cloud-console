package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"testing"
)

const testKeyHex = "7b0f9c2a5e1d4a3b8c6f0e2d4a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d9e0f2b46"

func sealForTest(t *testing.T, plaintext string) []byte {
	t.Helper()
	key, err := hex.DecodeString(testKeyHex)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil)
}

// TestDecryptWithWhitespaceKey pins the 2026-08-19 outage: the GITOPS_ENCRYPTION_KEY
// Secret carried a trailing "\n" that envFrom hands to the process verbatim, so every
// render that resolved a runtime env var failed on hex decode.
func TestDecryptWithWhitespaceKey(t *testing.T) {
	ct := sealForTest(t, "runtime-value")
	for _, dirty := range []string{testKeyHex + "\n", "\n" + testKeyHex, " " + testKeyHex + " \n"} {
		got, err := DecryptToken(dirty, ct)
		if err != nil {
			t.Fatalf("DecryptToken(%q) failed: %v", dirty, err)
		}
		if got != "runtime-value" {
			t.Fatalf("mismatch: got %q", got)
		}
	}
}

func TestWhitespaceOnlyKeyRejected(t *testing.T) {
	if _, err := DecryptToken("  \n", []byte("x")); err == nil {
		t.Fatal("expected error for whitespace-only key")
	}
}
