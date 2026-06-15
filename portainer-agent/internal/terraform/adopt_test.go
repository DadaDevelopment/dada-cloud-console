package terraform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareAdoptWorkspace(t *testing.T) {
	dir := t.TempDir()
	importID := "c3403bf7-255a-4ff7-8041-33296a0eca65"

	if err := PrepareAdoptWorkspace(dir, importID); err != nil {
		t.Fatalf("PrepareAdoptWorkspace: %v", err)
	}

	// variables.tf must be copied from the shared template.
	if _, err := os.Stat(filepath.Join(dir, "variables.tf")); err != nil {
		t.Fatalf("variables.tf not written: %v", err)
	}

	mainBytes, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("read main.tf: %v", err)
	}
	main := string(mainBytes)

	checks := []string{
		`import {`,
		`to = beget_compute_instance.app_server`,
		`id = "` + importID + `"`,
		`ignore_changes = all`,
		`ssh_keys = []`,
		`backend "pg" {}`,
	}
	for _, c := range checks {
		if !strings.Contains(main, c) {
			t.Errorf("main.tf missing %q\n---\n%s", c, main)
		}
	}

	// Adopt must NOT create a deploy SSH key (would mutate an external VM).
	if strings.Contains(main, "beget_ssh_key") {
		t.Errorf("adopt main.tf must not declare beget_ssh_key\n---\n%s", main)
	}
}
