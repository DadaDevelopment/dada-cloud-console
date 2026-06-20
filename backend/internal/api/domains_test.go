package api

import "testing"

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
