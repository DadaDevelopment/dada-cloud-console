package main

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/mcp-server/internal/overrides"
	"github.com/dada-tuda/console/mcp-server/internal/reflect"
)

const (
	realSpec     = "../../internal/reflect/testdata/backend-swagger.json"
	realOverride = "../../overrides.yaml"
)

func boot(t *testing.T) []reflect.GeneratedTool {
	t.Helper()
	spec, err := reflect.LoadSpec(context.Background(), realSpec, "")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	tools := reflect.GenerateTools(spec)
	ov, err := overrides.Load(realOverride)
	if err != nil {
		t.Fatalf("Load overrides: %v", err)
	}
	return overrides.Apply(tools, ov)
}

func find(tools []reflect.GeneratedTool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// TestBootRealSpec is the dev-proof: full boot pipeline (spec -> toolgen ->
// overrides) against the real backend spec, no live DB needed.
func TestBootRealSpec(t *testing.T) {
	tools := boot(t)

	if len(tools) < 30 {
		t.Fatalf("after overrides, %d tools, want >= 30", len(tools))
	}

	// Known tools must exist.
	for _, want := range []string{"createDatabase", "promoteModel", "listProjects"} {
		if !find(tools, want) {
			t.Errorf("expected tool %q missing", want)
		}
	}

	// Hidden tools must be gone (from real overrides.yaml).
	for _, hidden := range []string{"getAppLogs", "getAppMetrics", "getAppServerMetrics", "searchLogs"} {
		if find(tools, hidden) {
			t.Errorf("tool %q should be hidden by overrides.yaml", hidden)
		}
	}

	// State endpoints kept.
	for _, kept := range []string{"getAppState", "getAppServerState"} {
		if !find(tools, kept) {
			t.Errorf("state tool %q should be kept", kept)
		}
	}
}
