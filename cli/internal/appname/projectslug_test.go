package appname

import "testing"

func TestNormalizeProjectProducesValidSlugs(t *testing.T) {
	cases := []string{
		"My API",
		"8bit",
		"ui",
		"a",
		"Very-Long-Folder-Name-That-Goes-Well-Past-The-Console-Limit-For-Slugs",
		"__weird__name__",
	}
	for _, in := range cases {
		got := NormalizeProject(in)
		if !ValidProject(got) {
			t.Errorf("NormalizeProject(%q) = %q, which the console would reject", in, got)
		}
		if len(got) > 40 {
			t.Errorf("NormalizeProject(%q) = %q, longer than the 40-char limit", in, got)
		}
	}
}

func TestValidProjectRejectsBadSlugs(t *testing.T) {
	for _, in := range []string{"", "a", "ab", "1app", "App", "app-", "-app", "app_name"} {
		if ValidProject(in) {
			t.Errorf("ValidProject(%q) = true, want false", in)
		}
	}
}
