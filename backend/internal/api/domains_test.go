package api

import (
	"testing"
	"time"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"ACME.com":       "acme.com",
		"  shop.Acme.com ": "shop.acme.com",
		"acme.com.":      "acme.com",
		"Sub.Domain.IO.": "sub.domain.io",
	}
	for in, want := range cases {
		if got := normalizeDomain(in); got != want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidDomain(t *testing.T) {
	valid := []string{"acme.com", "shop.acme.com", "a.b.c.example.io", "x-y.example.com"}
	for _, d := range valid {
		if !isValidDomain(d) {
			t.Errorf("isValidDomain(%q) = false, want true", d)
		}
	}
	invalid := []string{
		"",                      // empty
		"localhost",             // single label
		"*.acme.com",            // wildcard out of scope
		"-bad.com",              // leading hyphen
		"bad-.com",              // trailing hyphen
		"acme..com",            // empty label
		"under_score.com",       // illegal char
		"UPPER.com",             // not normalized (caller normalizes first)
	}
	for _, d := range invalid {
		if isValidDomain(d) {
			t.Errorf("isValidDomain(%q) = true, want false", d)
		}
	}
}

func TestTxtChallenge(t *testing.T) {
	if got := txtChallengeValue("abc123"); got != "dada-domain-verify=abc123" {
		t.Errorf("txtChallengeValue = %q", got)
	}
}

func TestHostnamePendingExpired(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		created time.Time
		want    bool
	}{
		{"fresh attach stays pending", now.Add(-5 * time.Minute), false},
		{"slow but real DNS cutover stays pending", now.Add(-12 * time.Hour), false},
		{"just under window stays pending", now.Add(-hostnamePendingFailAfter + time.Minute), false},
		{"just over window fails", now.Add(-hostnamePendingFailAfter - time.Minute), true},
		{"long-orphaned row fails", now.Add(-24 * 24 * time.Hour), true},
	}
	for _, c := range cases {
		if got := hostnamePendingExpired(c.created, now); got != c.want {
			t.Errorf("%s: hostnamePendingExpired = %v, want %v", c.name, got, c.want)
		}
	}
}
