package api

import "testing"

func TestModelDiscoveryURLKeepsAnyVersionedBase(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com":               "https://api.openai.com/v1/models",
		"https://api.openai.com/v1":            "https://api.openai.com/v1/models",
		"https://api.z.ai/api/coding/paas/v4":  "https://api.z.ai/api/coding/paas/v4/models",
		"https://api.z.ai/api/coding/paas/v4/": "https://api.z.ai/api/coding/paas/v4/models",
		"https://x.test/v1/models":             "https://x.test/v1/models",
		"https://openrouter.ai/api":            "https://openrouter.ai/api/v1/models",
	}
	for base, want := range cases {
		got, err := modelDiscoveryURL(base)
		if err != nil || got != want {
			t.Errorf("modelDiscoveryURL(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
}

func TestZAIIsARoutableProviderWithBothGLMAliases(t *testing.T) {
	if !isKnownAIProvider("zai") {
		t.Fatal("zai must be storable as a provider credential")
	}
	got := aiAliasesForProvider("zai")
	if len(got) != 2 || got[0] != "glm-5.3" || got[1] != "glm-5.3-flash" {
		t.Fatalf("zai aliases = %v", got)
	}
	if aiProviderDefaultBases["zai"] != "https://api.z.ai/api/coding/paas/v4" {
		t.Fatalf("zai discovery base = %q", aiProviderDefaultBases["zai"])
	}
}
