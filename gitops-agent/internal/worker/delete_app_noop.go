package worker

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// appManifestFileName is the file whose presence makes an app real to ArgoCD:
// the tenant-apps ApplicationSet generates one Application per app.yaml found
// under the projects tree, so a folder without it is not deployed and a folder
// with it is, regardless of what the database believes.
const appManifestFileName = "app.yaml"

// strayAppManifests returns every app.yaml under repoRoot that belongs to an app
// named appName, as repo-relative paths, sorted.
//
// It is meant to be called only after a delete found nothing to remove at the
// paths it computed. At that point the six expected paths are known absent, so
// anything this returns is a manifest the delete could not see: the same app
// living under a project or environment slug that no longer matches what the
// database resolves today (a rename), which is exactly how a delete can report
// success while ArgoCD keeps the workload alive.
//
// Only .../apps/<appName>/app.yaml is matched, so an app folder that merely
// contains a nested file of that name cannot be mistaken for the app itself.
func strayAppManifests(repoRoot, appName string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != appManifestFileName {
			return nil
		}
		dir := filepath.Dir(path)
		if filepath.Base(dir) != appName || filepath.Base(filepath.Dir(dir)) != "apps" {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

// verifyDeleteRemovedApp reports whether a delete that produced no commit may be
// recorded as a successful deletion.
//
// A delete that removes nothing pushes nothing, and RemoveAndPush reports that
// as an empty SHA and no error. Recording it as Committed is a lie whenever the
// app is still deployed: on 2026-08-08 a user's DeleteApp reached Committed with
// an empty git_commit, the app kept running, the console showed no fault, and
// the user deleted the same app twice more over the next eleven hours before an
// attempt finally produced a commit.
//
// The git tree decides, not the database: an app whose manifest is nowhere in
// the repo is genuinely not deployed, so deleting it is an honest no-op (an
// imported repo that never shipped, or a repeat of a delete that already
// worked). A manifest found anywhere else means the delete missed it, and the
// operation must fail loudly instead of claiming the app is gone.
func verifyDeleteRemovedApp(repoRoot, appName, expectedPath string) error {
	stray, err := strayAppManifests(repoRoot, appName)
	if err != nil {
		return fmt.Errorf("scan gitops tree for manifests of app %q: %w", appName, err)
	}
	if len(stray) == 0 {
		return nil
	}
	return fmt.Errorf(
		"delete removed nothing at %s, but app %q is still deployed from %s: refusing to report it as deleted",
		expectedPath, appName, strings.Join(stray, ", "),
	)
}
