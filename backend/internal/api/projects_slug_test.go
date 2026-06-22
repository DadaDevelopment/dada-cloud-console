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
		"",            // empty
		"ab",          // too short (<3)
		"1abc",        // must start with a letter
		"-abc",        // must start with a letter
		"abc-",        // must end alphanumeric
		"AbC",         // no uppercase
		"a_b",         // no underscore
		"a b",         // no space
		"a..b",        // no dots
		"thisisaveryveryveryveryveryverylongprojectslugxx", // >40
	}
	for _, s := range invalid {
		if projectSlugRe.MatchString(s) {
			t.Errorf("slug %q should be invalid", s)
		}
	}
}
