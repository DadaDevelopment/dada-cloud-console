package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// locateServiceDatabase finds the resources.values.yaml that actually carries a
// named ServiceDatabaseV2 and returns that path together with the manifest.
//
// The path derived from appRef is only a first guess. It is right when the CR
// was rendered by this agent from the same appRef, and wrong in every case
// where the two drifted apart: a database bound to an app whose CR still sits
// in the project's standalone "service-databases-<project>" carrier, an appRef
// that names a database rather than the app owning the file (mlflow-db → the
// mlflow app), a carrier named after a single database (service-databases-
// codex-lb). Trusting the guess alone failed 17 of 21 SetDatabaseTier
// operations in production with "no ServiceDatabaseV2 in <path>", which left
// the storage quota undeliverable for the customer databases it was built for.
//
// The scan is over one environment's apps/ directory, so it cannot reach a
// database of another project, and the match is by kind AND name: a carrier
// file holding several databases must not hand back the first one.
func locateServiceDatabase(mgr *git.Manager, projectSlug, envSlug, appRef, name string) (valuesPath, manifest string, err error) {
	guess := renderer.ServiceDatabaseResourcesValuesGitPath(projectSlug, envSlug, appRef)
	raw, ok, err := readServiceDatabase(mgr, guess, name)
	if err != nil {
		return "", "", err
	}
	if ok {
		return guess, raw, nil
	}

	appsDir := renderer.EnvBaseGitPath(projectSlug, envSlug) + "/apps"
	entries, err := os.ReadDir(filepath.Join(mgr.LocalPath(), appsDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("no ServiceDatabaseV2 %q in %s and no apps/ under %s", name, guess, appsDir)
		}
		return "", "", fmt.Errorf("listing %s: %w", appsDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, app := range names {
		path := renderer.AppResourcesValuesGitPath(projectSlug, envSlug, app)
		if path == guess {
			continue
		}
		raw, ok, err := readServiceDatabase(mgr, path, name)
		if err != nil {
			return "", "", err
		}
		if ok {
			return path, raw, nil
		}
	}
	return "", "", fmt.Errorf("no ServiceDatabaseV2 %q anywhere under %s (guessed %s)", name, appsDir, guess)
}

// readServiceDatabase returns the named ServiceDatabaseV2 from one values file, or
// ok=false when the file is absent or holds no such manifest.
func readServiceDatabase(mgr *git.Manager, valuesPath, name string) (string, bool, error) {
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return "", false, err
	}
	return rv.ManifestOfKindNamed("ServiceDatabaseV2", name)
}
