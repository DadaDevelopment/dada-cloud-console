package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestInstallStateRoundTrip(t *testing.T) {
	secret := "s3cr3t"
	pid := uuid.New()

	state := signInstallState(secret, pid)
	got, ok := verifyInstallState(secret, state)
	if !ok {
		t.Fatalf("verify failed for freshly signed state")
	}
	if got != pid {
		t.Errorf("project id = %s, want %s", got, pid)
	}
}

func TestInstallStateRejectsTamper(t *testing.T) {
	secret := "s3cr3t"
	pid := uuid.New()
	state := signInstallState(secret, pid)

	// Forge a different project id but keep the original signature.
	other := uuid.New().String()
	parts := strings.SplitN(state, ".", 2)
	forged := other + "." + parts[1]
	if _, ok := verifyInstallState(secret, forged); ok {
		t.Errorf("verify accepted a forged project id")
	}

	// Wrong secret must not validate.
	if _, ok := verifyInstallState("other-secret", state); ok {
		t.Errorf("verify accepted state signed with a different secret")
	}

	// Malformed states are rejected, not panicking.
	for _, bad := range []string{"", "a", "a.b", "a.b.c.d"} {
		if _, ok := verifyInstallState(secret, bad); ok {
			t.Errorf("verify accepted malformed state %q", bad)
		}
	}
}
