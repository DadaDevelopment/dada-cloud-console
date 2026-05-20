package terraform_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/terraform"
)

func TestPrepareWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "test-workspace")

	if err := terraform.PrepareWorkspace(workspaceDir); err != nil {
		t.Fatalf("PrepareWorkspace error: %v", err)
	}

	for _, wantFile := range []string{"main.tf", "variables.tf"} {
		path := filepath.Join(workspaceDir, wantFile)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", wantFile, err)
		}
	}
}

func TestCleanWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "cleanup-test")
	if err := terraform.PrepareWorkspace(workspaceDir); err != nil {
		t.Fatal(err)
	}
	if err := terraform.CleanWorkspace(workspaceDir); err != nil {
		t.Fatalf("CleanWorkspace error: %v", err)
	}
	if _, err := os.Stat(workspaceDir); !os.IsNotExist(err) {
		t.Error("expected workspace to be removed")
	}
}
