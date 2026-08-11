package worker

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// projectManifestFileName is the file that makes a project real to the
// tenant-apps ApplicationSet: renderer.ProjectGitPath places exactly one of
// these per project, directly under clusters/<cluster>/projects/<slug>/.
const projectManifestFileName = "project.yaml"

// projectsDirName is the path segment every project tree hangs off, across
// every cluster prefix (today only beget-prod, but the scan must not assume
// that is the only one -- a project's own tree surviving under a stale or
// second cluster prefix is exactly the leak this guard exists to catch).
const projectsDirName = "projects"

// strayProjectManifests returns every project.yaml or app.yaml under repoRoot
// that still belongs to projectSlug, as repo-relative paths, sorted.
//
// It is meant to be called only after a project delete found nothing to
// remove at the path it computed. At that point the expected tree is known
// absent, so anything this returns is a manifest the delete could not see:
// the same project living under a cluster prefix that no longer matches what
// the database resolves today, or a partially-rendered tree the top-level
// remove missed.
//
// A path counts only when the "projects" segment is directly followed by a
// segment equal to projectSlug (exact match, so a project named "foo" cannot
// match a sibling tree "foo-bar"), and:
//   - the file is project.yaml sitting directly in that projects/<slug> dir
//     (mirrors renderer.ProjectGitPath: the slug segment is its immediate
//     parent, nothing deeper), or
//   - the file is app.yaml anywhere further down that projects/<slug> subtree
//     (mirrors renderer.AppGitPath, whatever the environment/app segments are).
//
// This deliberately ignores which cluster prefix precedes "projects", so a
// tree under clusters/beget-prod/projects/<slug>/... and one under
// clusters/other-cluster/projects/<slug>/... are both caught, even though the
// delete only ever targets clusters/beget-prod/projects/<slug>.
func strayProjectManifests(repoRoot, projectSlug string) ([]string, error) {
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
		name := d.Name()
		if name != projectManifestFileName && name != appManifestFileName {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")

		idx := -1
		for i, part := range parts {
			if part == projectsDirName && i+1 < len(parts) && parts[i+1] == projectSlug {
				idx = i + 1
				break
			}
		}
		if idx == -1 {
			return nil
		}
		if name == projectManifestFileName && idx != len(parts)-2 {
			return nil
		}

		found = append(found, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

// verifyDeleteRemovedProject reports whether a project delete that produced
// no commit may be recorded as a successful deletion.
//
// A delete that removes nothing pushes nothing, and RemoveAndPush reports
// that as an empty SHA and no error. Recording it as Committed and then
// running wipeProjectRows is a lie whenever the project's tree is still in
// git: the database rows are gone but the namespace, apps, and every other
// resource the project owns keep running with nothing left to identify them
// by, exactly the class of leak project_deleteproject_leaves_live_namespaces
// and project_deleteapp_reported_success_without_git_commit both describe.
//
// The git tree decides, not the database: a project whose manifests are
// nowhere in the repo is genuinely not deployed, so deleting it is an honest
// no-op (a project that never rendered, or a repeat of a delete that already
// worked). Any manifest found elsewhere means the delete missed it, and the
// operation must fail loudly instead of wiping the project's rows.
func verifyDeleteRemovedProject(repoRoot, projectSlug, expectedPath string) error {
	stray, err := strayProjectManifests(repoRoot, projectSlug)
	if err != nil {
		return fmt.Errorf("scan gitops tree for manifests of project %q: %w", projectSlug, err)
	}
	if len(stray) == 0 {
		return nil
	}
	return fmt.Errorf(
		"delete removed nothing at %s, but project %q is still deployed from %s: refusing to report it as deleted",
		expectedPath, projectSlug, strings.Join(stray, ", "),
	)
}
