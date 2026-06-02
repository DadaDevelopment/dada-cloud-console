package wstoken

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTripWithFile(t *testing.T) {
	const secret = "test-secret"
	in := Claims{
		Project: "client-a",
		Env:     "prod",
		App:     "api",
		File:    "compose.yaml",
		Exp:     time.Now().Add(time.Minute).Unix(),
	}
	tok, err := Sign(secret, in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := Verify(secret, tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.File != "compose.yaml" || out.Project != "client-a" || out.Env != "prod" || out.App != "api" {
		t.Fatalf("claims round-trip mismatch: %+v", out)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	const secret = "test-secret"
	tok, err := Sign(secret, Claims{App: "api", File: ".env", Exp: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify("wrong-secret", tok); err == nil {
		t.Fatal("expected verification failure with wrong secret")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	const secret = "test-secret"
	tok, _ := Sign(secret, Claims{App: "api", Exp: time.Now().Add(-time.Second).Unix()})
	if _, err := Verify(secret, tok); err == nil {
		t.Fatal("expected expired token to fail verification")
	}
}
