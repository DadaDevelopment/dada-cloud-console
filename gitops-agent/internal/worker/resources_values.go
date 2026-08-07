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
	return upsertManifestsFile(mgr, valuesPath, crYAML)
}

// upsertManifestsFile upserts one or more rendered CRs into the app's
// resources.values.yaml in a single load/marshal cycle, returning one
// FileChange. loadResourcesValues reads from disk, so callers that need several
// manifests in the same file MUST pass them here together rather than calling
// upsertManifestFile repeatedly (which would each re-read the unmodified file
// and drop the earlier upserts).
func upsertManifestsFile(mgr *git.Manager, valuesPath string, crYAMLs ...string) (git.FileChange, error) {
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return git.FileChange{}, err
	}
	for _, crYAML := range crYAMLs {
		if err := rv.Upsert(crYAML); err != nil {
			return git.FileChange{}, err
		}
	}
	out, err := rv.Marshal()
	if err != nil {
		return git.FileChange{}, err
	}
	return git.FileChange{Path: valuesPath, Content: out}, nil
}

// manifestsFileIsEmpty reports whether a rendered resources.values.yaml carries
// no manifests at all. A carrier app whose file reaches that state renders zero
// resources, which ArgoCD refuses to auto-sync: the generated Application stops
// with "auto-sync will wipe out all resources" (allowEmpty is off on the
// tenant-apps ApplicationSet), so the CRs it owns are never pruned and the
// physical database or bucket survives the delete. Callers use this to remove
// the carrier app itself instead of committing an empty file.
func manifestsFileIsEmpty(f git.FileChange) (bool, error) {
	rv, err := renderer.ParseResourcesValues(f.Content)
	if err != nil {
		return false, err
	}
	return len(rv.Manifests) == 0, nil
}

// standaloneOwnerAppPaths lists every git file of a resources-carrier owner app
// ("service-databases-<project>", "models-<project>", "s3-buckets-<project>").
// Removing them in one commit drops the app from the tenant-apps generator, so
// ArgoCD tears the Application down and its resources-finalizer prunes the CRs
// it owned. The prune render stays valid because the source is the shared
// helm/app-resources chart with ignoreMissingValueFiles: true — unlike a
// workload app, no per-app chart path disappears with the file.
func standaloneOwnerAppPaths(projectSlug, envSlug, ownerApp string) []string {
	return []string{
		renderer.AppGitPath(projectSlug, envSlug, ownerApp),
		renderer.AppResourcesValuesGitPath(projectSlug, envSlug, ownerApp),
		renderer.AppHelmValuesGitPath(projectSlug, envSlug, ownerApp),
	}
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
