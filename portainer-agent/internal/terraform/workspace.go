package terraform

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*
var templateFS embed.FS

// PrepareWorkspace copies the Terraform templates into workspaceDir.
// workspaceDir should be unique per AppServer (e.g. /var/lib/tf-workspaces/{uuid}).
func PrepareWorkspace(workspaceDir string) error {
	if err := os.MkdirAll(workspaceDir, 0750); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}

	return fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Strip "templates/" prefix
		relPath := path[len("templates/"):]
		// Strip .tmpl suffix — Terraform files are static, not Go templates
		destName := relPath
		if strings.HasSuffix(destName, ".tmpl") {
			destName = destName[:len(destName)-len(".tmpl")]
		}

		data, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}
		dest := filepath.Join(workspaceDir, destName)
		if err := os.WriteFile(dest, data, 0640); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		return nil
	})
}

// CleanWorkspace removes the workspace directory entirely.
func CleanWorkspace(workspaceDir string) error {
	return os.RemoveAll(workspaceDir)
}
