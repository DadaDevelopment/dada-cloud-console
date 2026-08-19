package crypto

import (
	"strings"
	"testing"
)

const testKeyHex = "7b0f9c2a5e1d4a3b8c6f0e2d4a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d9e0f2b46"

// TestKeyWithTrailingNewlineStillWorks pins the 2026-08-19 outage: the
// GITOPS_ENCRYPTION_KEY Secret held 64 hex chars plus a trailing "\n", which
// envFrom passes to the process verbatim. hex.DecodeString rejected it, so every
// encrypt and decrypt on the platform failed for ~21 hours.
func TestKeyWithTrailingNewlineStillWorks(t *testing.T) {
	for _, dirty := range []string{testKeyHex + "\n", "\n" + testKeyHex, " " + testKeyHex + " \n"} {
		enc, err := EncryptToken(dirty, []byte("secret-value"))
		if err != nil {
			t.Fatalf("EncryptToken(%q) failed: %v", dirty, err)
		}
		plain, err := DecryptToken(testKeyHex, enc)
		if err != nil {
			t.Fatalf("DecryptToken with clean key failed for dirty %q: %v", dirty, err)
		}
		if string(plain) != "secret-value" {
			t.Fatalf("round-trip mismatch: got %q", plain)
		}
	}
}

// TestDirtyKeyDecryptsRowsWrittenWithCleanKey covers the reveal path: rows encrypted
// before the Secret was corrupted must stay readable by a process that received the
// whitespace-bearing key.
func TestDirtyKeyDecryptsRowsWrittenWithCleanKey(t *testing.T) {
	enc, err := EncryptToken(testKeyHex, []byte("written-before-corruption"))
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}
	plain, err := DecryptToken(testKeyHex+"\n", enc)
	if err != nil {
		t.Fatalf("DecryptToken with trailing newline failed: %v", err)
	}
	if string(plain) != "written-before-corruption" {
		t.Fatalf("mismatch: got %q", plain)
	}
}

// TestWhitespaceOnlyKeyIsStillRejected guards the trim from swallowing a genuinely
// unconfigured key.
func TestWhitespaceOnlyKeyIsStillRejected(t *testing.T) {
	if _, err := EncryptToken("  \n", []byte("x")); err == nil {
		t.Fatal("expected error for whitespace-only key")
	}
	if _, err := DecryptToken("  \n", []byte("x")); err == nil {
		t.Fatal("expected error for whitespace-only key")
	}
	if _, err := EncryptToken(strings.Repeat("z", 64), []byte("x")); err == nil {
		t.Fatal("expected error for non-hex key")
	}
}
