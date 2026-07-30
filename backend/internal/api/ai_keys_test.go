package api

import (
	"strings"
	"testing"
)

func TestGenerateAIKey_FormatAndHash(t *testing.T) {
	plaintext, hash, prefix, err := generateAIKey()
	if err != nil {
		t.Fatalf("generateAIKey: %v", err)
	}
	if !strings.HasPrefix(plaintext, aiKeyPrefix) {
		t.Fatalf("plaintext=%q want %q prefix", plaintext, aiKeyPrefix)
	}
	if len(plaintext) != len(aiKeyPrefix)+48 {
		t.Fatalf("plaintext len=%d want %d", len(plaintext), len(aiKeyPrefix)+48)
	}
	if prefix != plaintext[:aiKeyPrefixLen] {
		t.Fatalf("prefix=%q want first %d chars of plaintext (%q)", prefix, aiKeyPrefixLen, plaintext[:aiKeyPrefixLen])
	}
	if hash != hashAIKey(plaintext) {
		t.Fatalf("hash=%q does not match hashAIKey(plaintext)=%q", hash, hashAIKey(plaintext))
	}

	plaintext2, hash2, _, err := generateAIKey()
	if err != nil {
		t.Fatalf("generateAIKey (2nd): %v", err)
	}
	if plaintext2 == plaintext || hash2 == hash {
		t.Fatal("two generateAIKey calls produced the same key")
	}
}

// TestAIKeyPrefix_DisjointFromUserServiceKeys pins the routing invariant the
// gateway plugin depends on: a console-minted key must be recognisable by
// prefix alone, so it is never sent to user-service introspection (which would
// answer "invalid" and reject a perfectly good key).
func TestAIKeyPrefix_DisjointFromUserServiceKeys(t *testing.T) {
	if !strings.HasPrefix(aiKeyPrefix, "sk-dada-") {
		t.Fatalf("aiKeyPrefix=%q must stay inside the sk-dada- family", aiKeyPrefix)
	}
	if aiKeyPrefix == "sk-dada-" {
		t.Fatal("aiKeyPrefix must be strictly longer than sk-dada- or it cannot be told apart")
	}
}

func TestMaskAIKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sk-proj-abcdefghijklmnop", "sk-p...mnop"},
		{"short", "*****"},
		{"", ""},
		{"exactly11ch", "***********"},
	}
	for _, tc := range cases {
		if got := maskAIKey(tc.in); got != tc.want {
			t.Fatalf("maskAIKey(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// TestAICatalogProvidersResolve guards the catalog against a model pointing at
// a provider the console cannot accept a credential for -- such a model would
// render in the UI and then fail closed at call time with no way to fix it.
func TestAICatalogProvidersResolve(t *testing.T) {
	for _, m := range aiCatalogModels {
		if !isKnownAIProvider(m.Provider) {
			t.Fatalf("model %q references unknown provider %q", m.Alias, m.Provider)
		}
		if m.Kind != "chat" && m.Kind != "embeddings" {
			t.Fatalf("model %q has kind %q, want chat or embeddings", m.Alias, m.Kind)
		}
	}
	seen := map[string]bool{}
	for _, m := range aiCatalogModels {
		if seen[m.Alias] {
			t.Fatalf("duplicate model alias %q", m.Alias)
		}
		seen[m.Alias] = true
	}
}
