package grafanaembed

import (
	"testing"
	"time"
)

var secret = []byte("test-secret-0123456789")

func TestSignVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	in := Claims{User: "alice", Email: "alice@x.io", Groups: []string{"proj:a", "proj:b"}, Dashboard: "dma123"}
	tok, err := Sign(secret, in, now, 2*time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	out, err := Verify(secret, tok, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.User != "alice" || out.Email != "alice@x.io" || out.Dashboard != "dma123" {
		t.Fatalf("claims mismatch: %+v", out)
	}
	if out.GroupsHeader() != "proj:a,proj:b" {
		t.Fatalf("groups header = %q", out.GroupsHeader())
	}
}

func TestSignStampsExpiryIgnoringCallerValue(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "u", ExpiresAt: 1}, now, time.Minute)
	c, err := Verify(secret, tok, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.ExpiresAt != now.Add(time.Minute).Unix() {
		t.Fatalf("exp not stamped: %d", c.ExpiresAt)
	}
}

func TestVerifyExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "u"}, now, time.Minute)
	if _, err := Verify(secret, tok, now.Add(2*time.Minute)); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerifyTamperedPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "alice", Groups: []string{"proj:a"}}, now, time.Minute)
	// Flip the body but keep the original MAC → must fail signature.
	body, mac, _ := cut(tok)
	forged := body[:len(body)-1] + flip(body[len(body)-1:]) + "." + mac
	if _, err := Verify(secret, forged, now); err != ErrSignature {
		t.Fatalf("want ErrSignature, got %v", err)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "u"}, now, time.Minute)
	if _, err := Verify([]byte("other-secret-xxxxxxxxxx"), tok, now); err != ErrSignature {
		t.Fatalf("want ErrSignature, got %v", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, tok := range []string{"", "nodot", ".", "a.", ".b"} {
		if _, err := Verify(secret, tok, now); err == nil {
			t.Fatalf("token %q: want error, got nil", tok)
		}
	}
}

func TestSignRejectsEmptyUserOrSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if _, err := Sign(secret, Claims{}, now, time.Minute); err == nil {
		t.Fatal("want error for empty user")
	}
	if _, err := Sign(nil, Claims{User: "u"}, now, time.Minute); err == nil {
		t.Fatal("want error for empty secret")
	}
}

func cut(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func flip(s string) string {
	if s == "A" {
		return "B"
	}
	return "A"
}
