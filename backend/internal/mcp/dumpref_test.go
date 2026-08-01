package mcp

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestDumpToolReference dumps the curated tool surface -- every tool with its
// method, path, required arguments and each property's type and description --
// so the published reference can be written from what the server actually
// exposes rather than from memory.
//
// It skips unless DUMP_TOOL_REFERENCE names an output path, so it costs nothing
// in CI:
//
//	DUMP_TOOL_REFERENCE=/tmp/tools.txt go test ./internal/mcp -run DumpToolReference
//
// Use it whenever the keep-list in default_overrides.yaml changes, then update
// frontend/content/docs/mcp-tool-reference.md. TestToolReferenceDocumentsEveryKeptTool
// fails until you do.
func TestDumpToolReference(t *testing.T) {
	out := os.Getenv("DUMP_TOOL_REFERENCE")
	if out == "" {
		t.Skip("set DUMP_TOOL_REFERENCE to a path to generate")
	}
	raw, err := os.ReadFile("../api/docs/swagger.json")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	ov, err := LoadOverrides("does-not-exist.yaml")
	if err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}
	tools := ApplyOverrides(GenerateTools(spec), ov)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	var b strings.Builder
	fmt.Fprintf(&b, "TOTAL %d\n\n", len(tools))
	for _, tl := range tools {
		fmt.Fprintf(&b, "## %s\n", tl.Name)
		fmt.Fprintf(&b, "method: %s %s\n", tl.Method, tl.PathTemplate)
		req, _ := tl.InputSchema["required"].([]string)
		fmt.Fprintf(&b, "required: %s\n", strings.Join(req, ", "))
		props, _ := tl.InputSchema["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pm, _ := props[n].(map[string]any)
			fmt.Fprintf(&b, "  - %s (%v) %v\n", n, pm["type"], pm["description"])
		}
		fmt.Fprintf(&b, "\n")
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %d tools to %s", len(tools), out)
}
