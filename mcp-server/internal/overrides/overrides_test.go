package overrides

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/mcp-server/internal/reflect"
)

func sample() []reflect.GeneratedTool {
	return []reflect.GeneratedTool{
		{Name: "getAppLogs", Description: "logs"},
		{Name: "createDatabase", Description: "old desc"},
		{Name: "listProjects", Description: "list"},
	}
}

func has(tools []reflect.GeneratedTool, name string) (reflect.GeneratedTool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return reflect.GeneratedTool{}, false
}

func TestApply_RenameHideAnnotate(t *testing.T) {
	c := &Config{
		Hide:     []string{"getAppLogs"},
		Rename:   map[string]string{"createDatabase": "order_database"},
		Annotate: map[string]string{"createDatabase": "Order a managed Postgres DB."},
	}
	out := Apply(sample(), c)

	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (one hidden)", len(out))
	}
	if _, ok := has(out, "getAppLogs"); ok {
		t.Error("getAppLogs should be hidden")
	}
	renamed, ok := has(out, "order_database")
	if !ok {
		t.Fatal("createDatabase should be renamed to order_database")
	}
	if renamed.Description != "Order a managed Postgres DB." {
		t.Errorf("annotate not applied: %q", renamed.Description)
	}
	if _, ok := has(out, "listProjects"); !ok {
		t.Error("listProjects should pass through untouched")
	}
}

func TestLoad_MissingFileIsNoOp(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should be no-op, got err: %v", err)
	}
	out := Apply(sample(), c)
	if len(out) != 3 {
		t.Errorf("no-op config changed tool set: %d", len(out))
	}
}

func TestLoad_RealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(path, []byte("hide:\n  - getAppLogs\nrename:\n  createDatabase: order_database\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	out := Apply(sample(), c)
	if _, ok := has(out, "getAppLogs"); ok {
		t.Error("getAppLogs should be hidden from file")
	}
	if _, ok := has(out, "order_database"); !ok {
		t.Error("rename from file not applied")
	}
}
