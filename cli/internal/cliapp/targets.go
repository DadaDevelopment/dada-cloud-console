package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dada-tuda/console/cli/internal/auth"
)

// Target is the deploy destination remembered for one working directory, so
// a second `ddc deploy` in the same folder never asks again.
type Target struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	EnvID       string `json:"env_id"`
	EnvName     string `json:"env_name"`
	AppName     string `json:"app_name"`
}

type targetsFile struct {
	Targets map[string]Target `json:"targets"`
}

// targetsPath is ~/.config/ddc/targets.json. The map lives outside the user's
// project so deploying never dirties their repo or their .gitignore.
func targetsPath() (string, error) {
	dir, err := auth.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "targets.json"), nil
}

func targetKey(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func loadTargets() (targetsFile, error) {
	path, err := targetsPath()
	if err != nil {
		return targetsFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return targetsFile{Targets: map[string]Target{}}, nil
	}
	if err != nil {
		return targetsFile{}, err
	}
	var out targetsFile
	if err := json.Unmarshal(data, &out); err != nil {
		return targetsFile{Targets: map[string]Target{}}, nil
	}
	if out.Targets == nil {
		out.Targets = map[string]Target{}
	}
	return out, nil
}

// LookupTarget returns the destination remembered for dir, if any.
func LookupTarget(dir string) (Target, bool, error) {
	key, err := targetKey(dir)
	if err != nil {
		return Target{}, false, err
	}
	file, err := loadTargets()
	if err != nil {
		return Target{}, false, err
	}
	t, ok := file.Targets[key]
	return t, ok, nil
}

// RememberTarget records dir's destination. A write failure is reported but
// is never fatal to a deploy that already succeeded.
func RememberTarget(dir string, t Target) error {
	key, err := targetKey(dir)
	if err != nil {
		return err
	}
	file, err := loadTargets()
	if err != nil {
		return err
	}
	file.Targets[key] = t

	path, err := targetsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}
