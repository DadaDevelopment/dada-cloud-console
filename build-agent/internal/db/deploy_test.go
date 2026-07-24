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

func TestBuildPreviewHostnameFullFQDNFitsK8sLabel(t *testing.T) {
	branch := "codex-v0-9-matching-dataset-elo-586435-some-more-descriptive-branch-name-text"
	got := buildPreviewHostname("dada-tuda.ru", "fonbet-value", branch, "ab12")
	if len(got) > 63 {
		t.Errorf("full fqdn %q is %d bytes, want <= 63 (gitops FQDNToName turns the WHOLE fqdn into a k8s resource name, dots->dashes, so this must fit the DNS-1123 label limit, not just the leading label)", got, len(got))
	}
	if !strings.HasSuffix(got, ".dada-tuda.ru") {
		t.Errorf("hostname %q lost the base domain", got)
	}
}

func TestBuildPreviewHostnameLongBranchIsDeterministic(t *testing.T) {
	branch := strings.Repeat("y", 100)
	got1 := buildPreviewHostname("dada-tuda.ru", "fonbet-value", branch, "ab12")
	got2 := buildPreviewHostname("dada-tuda.ru", "fonbet-value", branch, "ab12")
	if got1 != got2 {
		t.Errorf("buildPreviewHostname is not deterministic: %q != %q", got1, got2)
	}
	if len(got1) > 63 {
		t.Errorf("full fqdn %q is %d bytes, want <= 63", got1, len(got1))
	}
}

func TestBuildPreviewHostnameDistinctLongBranchesDontCollide(t *testing.T) {
	base := strings.Repeat("z", 90)
	branchA := base + "-alpha-suffix-one"
	branchB := base + "-beta-suffix-two"
	gotA := buildPreviewHostname("dada-tuda.ru", "fonbet-value", branchA, "ab12")
	gotB := buildPreviewHostname("dada-tuda.ru", "fonbet-value", branchB, "ab12")
	if gotA == gotB {
		t.Errorf("distinct long branches collided onto the same hostname: %q", gotA)
	}
	if len(gotA) > 63 || len(gotB) > 63 {
		t.Errorf("full fqdn exceeds 63 bytes: %q (%d) / %q (%d)", gotA, len(gotA), gotB, len(gotB))
	}
}

func TestBuildPreviewHostnameShortBranchUnchanged(t *testing.T) {
	got := buildPreviewHostname("dada-tuda.ru", "myapp", "feature/foo-bar", "ab12")
	want := "myapp-git-feature-foo-bar-ab12.dada-tuda.ru"
	if got != want {
		t.Errorf("short-branch hostname changed behavior: buildPreviewHostname = %q, want %q", got, want)
	}
}

func TestBuildDefaultHostnameFullFQDNFitsK8sLabel(t *testing.T) {
	longName := strings.Repeat("w", 80)
	got := buildDefaultHostname("dada-tuda.ru", longName, "ab12")
	if len(got) > 63 {
		t.Errorf("full fqdn %q is %d bytes, want <= 63", got, len(got))
	}
	if !strings.HasSuffix(got, ".dada-tuda.ru") {
		t.Errorf("hostname %q lost the base domain", got)
	}
}

func TestBuildDefaultHostnameShortNameUnchanged(t *testing.T) {
	got := buildDefaultHostname("dada-tuda.ru", "myapp", "ab12")
	want := "myapp-ab12.dada-tuda.ru"
	if got != want {
		t.Errorf("short-name hostname changed behavior: buildDefaultHostname = %q, want %q", got, want)
	}
}
