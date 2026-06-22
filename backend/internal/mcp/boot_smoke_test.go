package mcp

import (
	"os"
	"testing"
)

// Smoke: the generated swagger.json (same bytes the server embeds) must parse and
// generate tools at boot. Guards against a malformed spec regen breaking MCP init.
func TestEmbeddedSpecBoots(t *testing.T) {
	raw, err := os.ReadFile("../api/docs/swagger.json")
	if err != nil {
		t.Fatalf("read swagger.json: %v", err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	tools := GenerateTools(spec)
	if len(tools) == 0 {
		t.Fatal("no tools generated")
	}
	for _, tl := range tools {
		if tl.Name == "createProject" {
			return
		}
	}
	t.Errorf("createProject tool missing; got %d tools", len(tools))
}
