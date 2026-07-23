package db

import (
	"strings"
	"testing"
)

func TestSanitizeBranch(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		want   string
	}{
		{"simple", "feature-x", "feature-x"},
		{"slash", "feature/foo-bar", "feature-foo-bar"},
		{"uppercase", "Feature/FooBar", "feature-foobar"},
		{"collapse_runs", "a///b", "a-b"},
		{"trim_edges", "-foo-", "foo"},
		{"unicode_only", "ééé", "branch"},
		{"unicode_mixed", "fix-écaf-bug", "fix-caf-bug"},
		{"dots", "release.1.2.3", "release-1-2-3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeBranch(c.branch)
			if got != c.want {
				t.Errorf("sanitizeBranch(%q) = %q, want %q", c.branch, got, c.want)
			}
		})
	}
}

func TestSanitizeBranchNeverEmpty(t *testing.T) {
	inputs := []string{"", "---", "***", "éé"}
	for _, in := range inputs {
		got := sanitizeBranch(in)
		if got == "" {
			t.Errorf("sanitizeBranch(%q) returned empty string", in)
		}
	}
}

func TestBuildPreviewHostname(t *testing.T) {
	got := buildPreviewHostname("dada-tuda.ru", "myapp", "feature/foo-bar", "ab12")
	want := "myapp-git-feature-foo-bar-ab12.dada-tuda.ru"
	if got != want {
		t.Errorf("buildPreviewHostname = %q, want %q", got, want)
	}
}

func TestBuildPreviewHostnameTruncatesBranchNotSuffix(t *testing.T) {
	longBranch := strings.Repeat("x", 100)
	got := buildPreviewHostname("dada-tuda.ru", "myapp", longBranch, "ab12")

	dotIdx := strings.Index(got, ".dada-tuda.ru")
	if dotIdx == -1 {
		t.Fatalf("buildPreviewHostname result missing base suffix: %q", got)
	}
	label := got[:dotIdx]
	if len(label) > 63 {
		t.Errorf("hostname label %q is %d bytes, want <= 63", label, len(label))
	}
	if !strings.HasSuffix(label, "-ab12") {
		t.Errorf("hostname label %q must keep the full suffix", label)
	}
	if !strings.HasPrefix(label, "myapp-git-") {
		t.Errorf("hostname label %q must keep the full app-name prefix", label)
	}
	if strings.HasSuffix(strings.TrimSuffix(label, "-ab12"), "-") {
		t.Errorf("truncated branch left a trailing '-' before the suffix: %q", label)
	}
}

func TestBuildPreviewHostnameUnicodeBranch(t *testing.T) {
	got := buildPreviewHostname("dada-tuda.ru", "myapp", "ééé", "ab12")
	want := "myapp-git-branch-ab12.dada-tuda.ru"
	if got != want {
		t.Errorf("buildPreviewHostname(unicode) = %q, want %q", got, want)
	}
}
