package mcp

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The number of tools we ADVERTISE must be the number we actually expose.
//
// This test exists because the plugin README claimed 132 tools for months while the
// real curated surface was a third of that. 132 was the reflected count from before
// the keep-list existed, so the number was true once and then quietly became a
// promise the product did not keep — in its own installation instructions, which is
// the first thing a new user reads.
//
// A number in prose has no compiler, so this is the compiler. It reads the same
// allowlist the server loads and fails when the advertised figure drifts, which is
// cheaper than remembering to update three files.
//
// If this fails after a deliberate change to the keep-list: update the README and
// both manifests to the new count. That is the whole fix, and the failure message
// prints the number to use.
func TestAdvertisedToolCountMatchesTheKeepList(t *testing.T) {
	ov, err := LoadOverrides("does-not-exist.yaml") // falls back to the embedded default
	if err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}
	want := len(ov.Keep)
	if want == 0 {
		t.Fatal("the embedded keep-list is empty; the curation gate is not in force")
	}

	// Every place the count is published to a user. Paths are relative to this
	// package (backend/internal/mcp).
	for _, path := range []string{
		"../../../mcp-plugin/README.md",
		"../../../mcp-plugin/.claude-plugin/plugin.json",
		"../../../.claude-plugin/marketplace.json",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// "40 curated platform tools", "40 curated MCP tools", "40 tools" — the
		// wording differs per file, the number must not.
		matches := regexp.MustCompile(`(\d+)\s+(?:curated\s+)?(?:platform\s+|MCP\s+)?tools`).FindAllStringSubmatch(string(body), -1)
		if len(matches) == 0 {
			t.Errorf("%s advertises no tool count; it should say how many tools the plugin exposes", path)
			continue
		}
		for _, m := range matches {
			got, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if got != want {
				t.Errorf("%s advertises %d tools, but the keep-list exposes %d — update the text to %d",
					path, got, want, want)
			}
		}
	}
}
