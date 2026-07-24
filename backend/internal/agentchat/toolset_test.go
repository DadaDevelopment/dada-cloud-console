package agentchat

import (
	"os"
	"path/filepath"
	"testing"
)

func loadTestToolset(t *testing.T) *Toolset {
	t.Helper()
	specPath := filepath.Join("..", "api", "docs", "swagger.json")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read embedded swagger spec %q: %v", specPath, err)
	}
	ts, err := BuildToolset(spec, "http://localhost:8080")
	if err != nil {
		t.Fatalf("BuildToolset: %v", err)
	}
	return ts
}

func TestIsWrite_KnownWriteTool(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("restartApp") {
		t.Fatal("expected restartApp to be a registered tool")
	}
	if !ts.IsWrite("restartApp") {
		t.Fatal("expected restartApp to be classified as a write tool")
	}
}

func TestIsWrite_KnownReadTool(t *testing.T) {
	ts := loadTestToolset(t)
	if !ts.Has("listApps") {
		t.Fatal("expected listApps to be a registered tool")
	}
	if ts.IsWrite("listApps") {
		t.Fatal("expected listApps to be classified as a read tool")
	}
}

func TestIsWrite_AllWriteKeepToolsRegisteredAndClassified(t *testing.T) {
	ts := loadTestToolset(t)
	for _, name := range writeKeepTools {
		if !ts.Has(name) {
			t.Errorf("write tool %q is not registered in the toolset", name)
			continue
		}
		if !ts.IsWrite(name) {
			t.Errorf("write tool %q was not classified as a write tool", name)
		}
	}
}

func TestSecretDenyList_WinsOverReadAndWrite(t *testing.T) {
	ts := loadTestToolset(t)
	for name := range denyTools {
		if ts.Has(name) {
			t.Errorf("deny-listed tool %q must not be registered in the toolset (read or write)", name)
		}
		if ts.IsWrite(name) {
			t.Errorf("deny-listed tool %q must not report IsWrite==true", name)
		}
	}
}

func TestSecretDenyList_NotAccidentallyAddedToWriteKeep(t *testing.T) {
	for _, name := range writeKeepTools {
		if denyTools[name] {
			t.Fatalf("write tool %q is also deny-listed; deny-list must win, so it must never appear registered at all", name)
		}
	}
}
