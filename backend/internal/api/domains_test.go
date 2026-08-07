package api

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"ACME.com":         "acme.com",
		"  shop.Acme.com ": "shop.acme.com",
		"acme.com.":        "acme.com",
		"Sub.Domain.IO.":   "sub.domain.io",
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
		"",                // empty
		"localhost",       // single label
		"*.acme.com",      // wildcard out of scope
		"-bad.com",        // leading hyphen
		"bad-.com",        // trailing hyphen
		"acme..com",       // empty label
		"under_score.com", // illegal char
		"UPPER.com",       // not normalized (caller normalizes first)
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

func TestAppNeedsDefaultDomain(t *testing.T) {
	cases := []struct {
		name    string
		summary map[string]any
		want    bool
	}{
		{"ordinary http app on 8080", map[string]any{"port": float64(8080)}, true},
		{"js app on default vite port", map[string]any{"port": float64(5173), "framework": "vite"}, true},
		{"configured nonstandard port is included", map[string]any{"port": float64(6379)}, true},
		{"configured database-number port is included", map[string]any{"port": float64(5432)}, true},
		{"missing port excluded (hand-maintained infra snapshot)", map[string]any{"framework": "node"}, false},
		{"missing port and framework excluded", map[string]any{}, false},
		{"zero port excluded even with framework", map[string]any{"port": float64(0), "framework": "react"}, false},
		{"worker flag does not override a configured port", map[string]any{"port": float64(8080), "worker": true}, true},
		{"worker false leaves ordinary app included", map[string]any{"port": float64(8080), "worker": false}, true},
	}
	for _, c := range cases {
		if got := appNeedsDefaultDomain(c.summary); got != c.want {
			t.Errorf("%s: appNeedsDefaultDomain(%v) = %v, want %v", c.name, c.summary, got, c.want)
		}
	}
}

func TestBackfillMissingDefaultDomainsDisabledConfig(t *testing.T) {
	if err := BackfillMissingDefaultDomains(nil, nil, nil); err != nil {
		t.Errorf("nil cfg should short-circuit without touching pool, got err: %v", err)
	}
}

func TestBuildDefaultHostnameFullFQDNFitsK8sLabel(t *testing.T) {
	longName := strings.Repeat("w", 80)
	got := buildDefaultHostname("dada-tuda.ru", longName, "ab12")
	if len(got) > 63 {
		t.Errorf("full fqdn %q is %d bytes, want <= 63 (gitops FQDNToName turns the WHOLE fqdn into a k8s resource name, dots->dashes, so this must fit the DNS-1123 label limit, not just the leading label)", got, len(got))
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

func TestBuildDefaultHostnameLongNameIsDeterministic(t *testing.T) {
	longName := strings.Repeat("v", 90)
	got1 := buildDefaultHostname("dada-tuda.ru", longName, "ab12")
	got2 := buildDefaultHostname("dada-tuda.ru", longName, "ab12")
	if got1 != got2 {
		t.Errorf("buildDefaultHostname is not deterministic: %q != %q", got1, got2)
	}
}

func TestBuildDefaultHostnameDistinctLongNamesDontCollide(t *testing.T) {
	base := strings.Repeat("u", 90)
	nameA := base + "-alpha-suffix-one"
	nameB := base + "-beta-suffix-two"
	gotA := buildDefaultHostname("dada-tuda.ru", nameA, "ab12")
	gotB := buildDefaultHostname("dada-tuda.ru", nameB, "ab12")
	if gotA == gotB {
		t.Errorf("distinct long names collided onto the same hostname: %q", gotA)
	}
}

func TestBuildDefaultHostnameMatchesBuildAgentAlgorithm(t *testing.T) {
	cases := []struct {
		base, name, suffix string
	}{
		{"dada-tuda.ru", "myapp", "ab12"},
		{"dada-tuda.ru", strings.Repeat("w", 80), "ab12"},
	}
	for _, c := range cases {
		got := buildDefaultHostname(c.base, c.name, c.suffix)
		if !strings.HasSuffix(got, "."+c.base) {
			t.Errorf("buildDefaultHostname(%q,%q,%q) = %q, missing base suffix", c.base, c.name, c.suffix, got)
		}
		label := strings.TrimSuffix(got, "."+c.base)
		if len(got) > 63 {
			t.Errorf("buildDefaultHostname(%q,%q,%q) = %q is %d bytes, want <= 63", c.base, c.name, c.suffix, got, len(got))
		}
		if !strings.HasSuffix(label, "-"+c.suffix) {
			t.Errorf("buildDefaultHostname(%q,%q,%q) = %q must keep the full random suffix", c.base, c.name, c.suffix, got)
		}
	}
}
