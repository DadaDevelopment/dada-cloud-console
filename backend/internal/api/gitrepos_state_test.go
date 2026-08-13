package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestInstallStateRoundTrip(t *testing.T) {
	secret := "s3cr3t"
	pid := uuid.New()

	state, nonce := signInstallState(secret, pid)
	got, gotNonce, ok := verifyInstallState(secret, state)
	if !ok {
		t.Fatalf("verify failed for freshly signed state")
	}
	if got != pid {
		t.Errorf("project id = %s, want %s", got, pid)
	}
	if gotNonce != nonce {
		t.Errorf("nonce = %q, want %q — the callback cannot find its intent row without it", gotNonce, nonce)
	}
	if nonce == "" {
		t.Error("nonce is empty: the two halves of the install flight have no correlation key")
	}
}

func TestInstallStateNonceIsUniquePerCall(t *testing.T) {
	secret := "s3cr3t"
	pid := uuid.New()

	_, first := signInstallState(secret, pid)
	_, second := signInstallState(secret, pid)
	if first == second {
		t.Errorf("two install URLs for the same project share nonce %q — two flights would collapse into one row", first)
	}
}

// TestInstallStateRejectsTamper covers three ways a state can be wrong: a
// forged project id carrying a genuine signature, a state signed with another
// secret, and malformed input, which must be rejected rather than panic.
func TestInstallStateRejectsTamper(t *testing.T) {
	secret := "s3cr3t"
	pid := uuid.New()
	state, _ := signInstallState(secret, pid)

	other := uuid.New().String()
	parts := strings.SplitN(state, ".", 2)
	forged := other + "." + parts[1]
	if _, _, ok := verifyInstallState(secret, forged); ok {
		t.Errorf("verify accepted a forged project id")
	}

	if _, _, ok := verifyInstallState("other-secret", state); ok {
		t.Errorf("verify accepted state signed with a different secret")
	}

	for _, bad := range []string{"", "a", "a.b", "a.b.c.d"} {
		if _, _, ok := verifyInstallState(secret, bad); ok {
			t.Errorf("verify accepted malformed state %q", bad)
		}
	}
}
