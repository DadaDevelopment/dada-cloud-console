package appname

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"MyApp":            "myapp",
		"my_cool_app":      "my-cool-app",
		"  spaced out  ":   "spaced-out",
		"---leading-dash":  "leading-dash",
		"trailing-dash---": "trailing-dash",
		"a.b.c":            "a-b-c",
		"already-valid":    "already-valid",
		"a__b":             "a-b",
	}
	for in, want := range cases {
		got := Normalize(in)
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTruncatesTo63(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := Normalize(long)
	if len(got) > 63 {
		t.Fatalf("expected length <= 63, got %d", len(got))
	}
	if err := Validate(got); err != nil {
		t.Fatalf("normalized long name should be valid: %v", err)
	}
}

func TestValidateAcceptsPattern(t *testing.T) {
	valid := []string{"a", "a1", "my-app", "a0-b9"}
	for _, name := range valid {
		if err := Validate(name); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", name, err)
		}
	}
}

func TestValidateRejectsBadNames(t *testing.T) {
	invalid := []string{"", "-leading", "trailing-", "Upper", "has_underscore", "a..b"}
	for _, name := range invalid {
		if err := Validate(name); err == nil {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}
