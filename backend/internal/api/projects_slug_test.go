package api

import "testing"

func TestProjectSlugRe(t *testing.T) {
	valid := []string{"fin-core", "internal", "abc", "a1b2c3", "my-cool-app-1"}
	for _, s := range valid {
		if !projectSlugRe.MatchString(s) {
			t.Errorf("slug %q should be valid", s)
		}
	}
	invalid := []string{
		"",     // empty
		"ab",   // too short (<3)
		"1abc", // must start with a letter
		"-abc", // must start with a letter
		"abc-", // must end alphanumeric
		"AbC",  // no uppercase
		"a_b",  // no underscore
		"a b",  // no space
		"a..b", // no dots
		"thisisaveryveryveryveryveryverylongprojectslugxx", // >40
	}
	for _, s := range invalid {
		if projectSlugRe.MatchString(s) {
			t.Errorf("slug %q should be invalid", s)
		}
	}
}

func TestDefaultProjectSlug(t *testing.T) {
	// A clean username sanitizes to a valid slug verbatim-ish.
	cases := map[string]string{
		"alexkekiy": "alexkekiy",
		"John.Doe":  "john-doe",
		"a_b c":     "a-b-c",
		"-leading-": "leading",
		"ab":        "default-", // too short after sanitize → hashed fallback (prefix)
		"":          "default-", // empty → hashed fallback (prefix)
	}
	for username, want := range cases {
		got := defaultProjectSlug(username)
		if !projectSlugRe.MatchString(got) {
			t.Errorf("defaultProjectSlug(%q) = %q is not a valid slug", username, got)
		}
		if len(want) > 0 && want[len(want)-1] == '-' {
			// fallback case: only assert the prefix and validity.
			if got[:len(want)] != want {
				t.Errorf("defaultProjectSlug(%q) = %q, want prefix %q", username, got, want)
			}
			continue
		}
		if got != want {
			t.Errorf("defaultProjectSlug(%q) = %q, want %q", username, got, want)
		}
	}
}
