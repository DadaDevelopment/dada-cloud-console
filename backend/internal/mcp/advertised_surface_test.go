package mcp

import (
	"os"
	"regexp"
	"strconv"
	"strings"
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
	for _, path := range advertisedCountFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// "40 curated platform tools", "40 curated MCP tools", "40 tools",
		// "40 отобранных инструментов" — the wording differs per file and per
		// language, the number must not.
		matches := advertisedCount.FindAllStringSubmatch(string(body), -1)
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

// Every user-facing surface that prints the size of the tool list. The plugin
// manifests were guarded first; the public documentation was not, and went on
// saying 132 for months after the manifests were corrected — the same rot in a
// second place, which is why the list is now shared rather than inlined.
var advertisedCountFiles = []string{
	"../../../mcp-plugin/README.md",
	"../../../mcp-plugin/.claude-plugin/plugin.json",
	"../../../.claude-plugin/marketplace.json",
	"../../../frontend/content/docs/mcp-ai-agents.md",
	"../../../frontend/content/docs/mcp-tool-reference.md",
	"../../../frontend/content/docs/README.md",
	"../../../frontend/content/docs/ru/mcp-ai-agents.md",
	"../../../frontend/lib/i18n/dict.ts",
	"../../../frontend/app/(marketing)/developer/page.tsx",
}

// The Russian translations advertise the same number in their own words, so the
// pattern has to read both languages or a translated page becomes a place the
// count can rot unguarded.
var advertisedCount = regexp.MustCompile(
	`(\d+)\s+(?:(?:curated\s+)?(?:platform\s+|MCP\s+)?tools|(?:отобранн\p{Cyrillic}+\s+)?инструмент\p{Cyrillic}*)`)

// TestToolReferenceDocumentsEveryKeptTool checks the published tool reference
// against the allowlist the server actually loads, in both directions.
//
// The reference documents one tool per table row, each row opening with the tool
// name in backticks and carrying four columns (tool, required, optional, notes),
// so the documented set is machine-readable. The four-column shape is what tells
// a tool row apart from the narrower tables the page also uses for prose. A count
// alone would not catch what actually costs a user their afternoon: a tool that is
// live but undocumented (they never learn it exists) or documented but absent
// (they ask an agent for something it cannot do).
func TestToolReferenceDocumentsEveryKeptTool(t *testing.T) {
	ov, err := LoadOverrides("does-not-exist.yaml")
	if err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}

	const refPath = "../../../frontend/content/docs/mcp-tool-reference.md"
	body, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read %s: %v", refPath, err)
	}

	documented := map[string]bool{}
	rowName := regexp.MustCompile("^\\|\\s*`([A-Za-z][A-Za-z0-9]*)`\\s*\\|")
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Count(line, "|") < 5 {
			continue
		}
		if m := rowName.FindStringSubmatch(line); m != nil {
			documented[m[1]] = true
		}
	}

	kept := map[string]bool{}
	for _, name := range ov.Keep {
		kept[name] = true
		if !documented[name] {
			t.Errorf("%s does not document the %q tool, which the server exposes", refPath, name)
		}
	}
	for name := range documented {
		if !kept[name] {
			t.Errorf("%s documents a %q tool that the server does not expose", refPath, name)
		}
	}
}
