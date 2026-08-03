package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestRegexAlternationQuotesMetacharacters guards the container-metrics label
// matchers: an image reference is full of regex metacharacters (dots in the
// registry host, a colon before the tag), and an unquoted "." would let one
// app's series match another's.
func TestRegexAlternationQuotesMetacharacters(t *testing.T) {
	got := regexAlternation([]string{"ghcr.io/dadadevelopment/app:a81e594f"})
	if strings.Contains(got, "o/") && !strings.Contains(got, `\.`) {
		t.Fatalf("regexAlternation = %q, want the dots quoted", got)
	}
	if strings.Contains(got, "|") {
		t.Fatalf("regexAlternation of one value = %q, want no alternation", got)
	}
}

func TestRegexAlternationJoinsAndDropsEmpty(t *testing.T) {
	got := regexAlternation([]string{"argocd-prod", "", "platform-prod"})
	if got != "argocd-prod|platform-prod" {
		t.Fatalf("regexAlternation = %q, want argocd-prod|platform-prod", got)
	}
	if regexAlternation(nil) != "" {
		t.Fatal("regexAlternation(nil) is non-empty")
	}
}

// TestK8sContainerMetricSpecsUseRegexMatchers pins the matcher operator: with
// "=" the multi-namespace/multi-image case an adopted ArgoCD app produces would
// silently return no data, which is the failure this pass exists to remove.
func TestK8sContainerMetricSpecsUseRegexMatchers(t *testing.T) {
	for _, s := range k8sContainerMetricSpecs {
		q := fmt.Sprintf(s.expr, "argocd-prod|platform-prod", `ghcr\.io/app:1`)
		if !strings.Contains(q, `namespace=~"argocd-prod|platform-prod"`) {
			t.Fatalf("%s query %q does not use a namespace regex matcher", s.key, q)
		}
		if !strings.Contains(q, `image=~"`) {
			t.Fatalf("%s query %q does not use an image regex matcher", s.key, q)
		}
	}
}

func TestMergeNonEmptyDedupesAndDropsBlanks(t *testing.T) {
	got := mergeNonEmpty([]string{"argocd-prod", "argocd-prod"}, "platform-prod")
	if len(got) != 2 || got[0] != "argocd-prod" || got[1] != "platform-prod" {
		t.Fatalf("mergeNonEmpty = %v, want [argocd-prod platform-prod]", got)
	}
	if got := mergeNonEmpty(nil, ""); len(got) != 0 {
		t.Fatalf("mergeNonEmpty(nil, \"\") = %v, want empty", got)
	}
	if got := mergeNonEmpty([]string{"a"}, "a"); len(got) != 1 {
		t.Fatalf("mergeNonEmpty duplicated an existing value: %v", got)
	}
}
