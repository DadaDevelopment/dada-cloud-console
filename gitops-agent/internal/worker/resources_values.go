package worker

import (
	"errors"
	"os"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// loadResourcesValues reads an app's resources.values.yaml from the local
// worktree. A missing file yields an empty (non-nil) ResourcesValues, matching
// the ADR 0005 contract that an absent values file is safe.
func loadResourcesValues(mgr *git.Manager, valuesPath string) (*renderer.ResourcesValues, error) {
	content, err := mgr.ReadFile(valuesPath)
	if errors.Is(err, os.ErrNotExist) {
		return renderer.ParseResourcesValues("")
	}
	if err != nil {
		return nil, err
	}
	return renderer.ParseResourcesValues(content)
}

// upsertManifestFile loads the app's resources.values.yaml, upserts the rendered
// CR (keyed by kind+name), and returns the resulting file as a single
// FileChange. Caller commits it (preserving single-commit-per-operation).
func upsertManifestFile(mgr *git.Manager, valuesPath, crYAML string) (git.FileChange, error) {
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return git.FileChange{}, err
	}
	if err := rv.Upsert(crYAML); err != nil {
		return git.FileChange{}, err
	}
	out, err := rv.Marshal()
	if err != nil {
		return git.FileChange{}, err
	}
	return git.FileChange{Path: valuesPath, Content: out}, nil
}

// removeManifestsFile loads the app's resources.values.yaml, removes each
// (kind,name) entry, and returns the resulting file as a single FileChange plus
// whether anything was removed. When nothing matched (and the file was absent),
// changed is false and the caller can skip committing.
func removeManifestsFile(mgr *git.Manager, valuesPath string, keys [][2]string) (git.FileChange, bool, error) {
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return git.FileChange{}, false, err
	}
	changed := false
	for _, k := range keys {
		if rv.Remove(k[0], k[1]) {
			changed = true
		}
	}
	if !changed {
		return git.FileChange{}, false, nil
	}
	out, err := rv.Marshal()
	if err != nil {
		return git.FileChange{}, false, err
	}
	return git.FileChange{Path: valuesPath, Content: out}, true, nil
}
