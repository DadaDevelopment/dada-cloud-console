package mcp

import (
	"os"
	"testing"
)

func TestDefaultOverridesCurateToolSurface(t *testing.T) {
	raw, err := os.ReadFile("../api/docs/swagger.json")
	if err != nil {
		t.Fatalf("read swagger.json: %v", err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	all := GenerateTools(spec)

	ov, err := LoadOverrides("does-not-exist.yaml")
	if err != nil {
		t.Fatalf("LoadOverrides fallback: %v", err)
	}
	if len(ov.Keep) == 0 {
		t.Fatal("embedded default must carry a keep allowlist")
	}

	kept := ApplyOverrides(all, ov)

	if len(kept) >= len(all) {
		t.Fatalf("curation did not shrink the set: %d kept of %d", len(kept), len(all))
	}
	if len(kept) != len(ov.Keep) {
		t.Errorf("kept %d tools, keep-list has %d — some keep names don't match generated tools", len(kept), len(ov.Keep))
	}

	present := map[string]bool{}
	for _, tl := range kept {
		present[tl.Name] = true
	}
	for _, must := range []string{"createApp", "setEnvVar", "listProjects", "searchLogs", "getOperation", "listApps"} {
		if !present[must] {
			t.Errorf("core tool %q missing after curation", must)
		}
	}
	// recordBoxFunnelEvent/grantBox are annotated (the OpenAPI coverage gate
	// requires it) but must never become agent tools: the first is a marketing
	// ingest written by a landing page, the second is a human operator recording
	// a hand-provisioned box. The keep-list being an allowlist is what stops
	// them, and this asserts that guarantee holds rather than assuming it.
	for _, noise := range []string{
		"ingestLogs", "ingestMetrics", "gitInstallCallback", "gitHubOAuthCallback", "login",
		"recordBoxFunnelEvent", "grantBox",
	} {
		if present[noise] {
			t.Errorf("noise tool %q should have been curated out", noise)
		}
	}
}
