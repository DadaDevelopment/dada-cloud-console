package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// keycloakGroupCR builds a live Group CR as the keycloak-config chart renders it:
// deletionPolicy Orphan, which is what makes a prune forget the group instead of
// deleting it.
func keycloakGroupCR(name, deletionPolicy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "group.keycloak.crossplane.io/v1alpha1",
		"kind":       "Group",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"deletionPolicy": deletionPolicy,
			"forProvider":    map[string]any{"name": name},
		},
	}}
}

func newKeycloakGroupFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{keycloakGroupGVR: "GroupList"},
		objs...,
	)
}

// newKeycloakGroupsRepo seeds a bare remote carrying the org-groups YAML of the
// given projects and returns a Manager cloned from it, so the teardown is
// exercised against a real git push rather than a stub.
func newKeycloakGroupsRepo(t *testing.T, slugs ...string) *git.Manager {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	seedDir := filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	for _, slug := range slugs {
		yaml, err := renderer.RenderProjectGroups(renderer.ProjectGroupSpec{ProjectSlug: slug})
		if err != nil {
			t.Fatalf("render groups for %s: %v", slug, err)
		}
		historyRewriteWriteAndAdd(t, seedDir, wt, renderer.ProjectGroupsGitPath(slug), yaml)
	}
	if _, err := wt.Commit("seed org groups", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    historyRewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("clone: %v", err)
	}
	return mgr
}

// TestRetireProjectGroupsInGit_WritesDeletePolicy is the regression for the half
// of the teardown git alone cannot do: removing the YAML prunes the CRs, but
// deletionPolicy Orphan means the groups survive in Keycloak (project client-a,
// deleted 2026-08-10, still had all five groups Ready). The policy must be
// flipped in the manifest, and only for the doomed project.
func TestRetireProjectGroupsInGit_WritesDeletePolicy(t *testing.T) {
	mgr := newKeycloakGroupsRepo(t, "client-a", "survivor")

	path, sha, err := retireProjectGroupsInGit(mgr, "client-a", "retire kc groups", "bot", "bot@dada")
	if err != nil {
		t.Fatalf("retireProjectGroupsInGit: %v", err)
	}
	if sha == "" {
		t.Fatal("a present file must produce a commit; empty sha means the policy never reached git")
	}
	if path != renderer.ProjectGroupsGitPath("client-a") {
		t.Errorf("path = %q, want the rendered org-groups path", path)
	}

	doomed, err := mgr.ReadFile(path)
	if err != nil {
		t.Fatalf("read retired manifest: %v", err)
	}
	if strings.Contains(doomed, "deletionPolicy: Orphan") {
		t.Error("retired manifest still carries deletionPolicy: Orphan; the prune would leave the groups in Keycloak")
	}
	if n := strings.Count(doomed, "deletionPolicy: Delete"); n != 9 {
		t.Errorf("deletionPolicy: Delete count = %d, want 9 (org parent + 4 subgroups + 4 Roles)", n)
	}

	survivor, err := mgr.ReadFile(renderer.ProjectGroupsGitPath("survivor"))
	if err != nil {
		t.Fatalf("read foreign manifest: %v", err)
	}
	if !strings.Contains(survivor, "deletionPolicy: Orphan") {
		t.Error("a living project's manifest was flipped to Delete; an accidental prune would wipe its real groups")
	}
}

// TestRetireProjectGroupsInGit_AbsentFileIsNoop keeps the teardown idempotent: a
// re-driven DeleteProject, or a project bootstrapped before group CRs existed,
// must produce no commit and no error.
func TestRetireProjectGroupsInGit_AbsentFileIsNoop(t *testing.T) {
	mgr := newKeycloakGroupsRepo(t, "survivor")

	path, sha, err := retireProjectGroupsInGit(mgr, "never-had-groups", "retire kc groups", "bot", "bot@dada")
	if err != nil {
		t.Fatalf("retireProjectGroupsInGit on absent file: %v", err)
	}
	if sha != "" {
		t.Errorf("sha = %q, want empty: nothing was there to retire", sha)
	}
	if path != renderer.ProjectGroupsGitPath("never-had-groups") {
		t.Errorf("path = %q, want the rendered org-groups path", path)
	}
}

// TestWaitProjectGroupsRetired_TrueOnlyWhenPolicyLanded is the guard against the
// bug that made the first fix useless: ArgoCD self-heal restores spec from git,
// so the manifest may say Delete while the cluster still says Orphan (measured on
// org-ssa, 2026-08-11 — a kubectl patch was reverted in under two minutes).
// Removing the manifest in that window orphans the groups again, so the wait must
// report false until every CR has converged.
func TestWaitProjectGroupsRetired_TrueOnlyWhenPolicyLanded(t *testing.T) {
	ctx := context.Background()
	names := renderer.ProjectGroupCRNames("client-a")
	if len(names) != 5 {
		t.Fatalf("ProjectGroupCRNames = %v, want the org parent plus 4 role subgroups", names)
	}

	var mixed []runtime.Object
	for i, name := range names {
		policy := renderer.DeletionPolicyDelete
		if i == len(names)-1 {
			policy = "Orphan"
		}
		mixed = append(mixed, keycloakGroupCR(name, policy))
	}
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: newKeycloakGroupFake(mixed...)}}

	cancelCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if w.waitProjectGroupsRetired(cancelCtx, "client-a") {
		t.Error("wait reported converged while one CR was still Orphan; the removal would orphan that group")
	}

	var all []runtime.Object
	for _, name := range names {
		all = append(all, keycloakGroupCR(name, renderer.DeletionPolicyDelete))
	}
	all = append(all, keycloakGroupCR("org-survivor", "Orphan"))
	converged := &DBWatcher{clients: &dadak8s.Clients{Dynamic: newKeycloakGroupFake(all...)}}
	if !converged.waitProjectGroupsRetired(ctx, "client-a") {
		t.Error("wait reported pending while every CR of the project was already Delete")
	}
}

// TestWaitProjectGroupsRetired_MissingCRsCountAsRetired covers the project torn
// down before its groups were ever created, and the local/off-cluster run: no CRs
// and no client must both let the teardown finish rather than stall it.
func TestWaitProjectGroupsRetired_MissingCRsCountAsRetired(t *testing.T) {
	ctx := context.Background()

	empty := &DBWatcher{clients: &dadak8s.Clients{Dynamic: newKeycloakGroupFake()}}
	if !empty.waitProjectGroupsRetired(ctx, "acme") {
		t.Error("absent CRs must count as retired; nothing can orphan a group that was never created")
	}
	if !(&DBWatcher{}).waitProjectGroupsRetired(ctx, "acme") {
		t.Error("no cluster client must not stall the teardown")
	}
	if !(&DBWatcher{clients: &dadak8s.Clients{}}).waitProjectGroupsRetired(ctx, "acme") {
		t.Error("no dynamic client must not stall the teardown")
	}
}

// TestRemoveProjectGroupsFromGit_RemovesOnlyTheDoomedProject is the regression
// for the git half: bootstrapProject writes org-groups-<slug>.yaml and nothing
// removed it, so a deleted project's Group CRs stayed on the branch (found by
// hand on client-a, 2026-08-10). The doomed project's file must be gone from the
// pushed tree and every other project's file must survive.
func TestRemoveProjectGroupsFromGit_RemovesOnlyTheDoomedProject(t *testing.T) {
	mgr := newKeycloakGroupsRepo(t, "client-a", "survivor")

	path, sha, err := removeProjectGroupsFromGit(mgr, "client-a", "delete kc groups", "bot", "bot@dada")
	if err != nil {
		t.Fatalf("removeProjectGroupsFromGit: %v", err)
	}
	if sha == "" {
		t.Fatal("a present file must produce a commit; empty sha means nothing was removed")
	}
	if path != renderer.ProjectGroupsGitPath("client-a") {
		t.Errorf("path = %q, want the rendered org-groups path", path)
	}
	if _, err := os.Stat(filepath.Join(mgr.LocalPath(), path)); !os.IsNotExist(err) {
		t.Errorf("org-groups file still on disk after removal: %v", err)
	}
	if _, err := mgr.ReadFile(renderer.ProjectGroupsGitPath("survivor")); err != nil {
		t.Errorf("a foreign project's org-groups file was removed too: %v", err)
	}
}

// TestRemoveProjectGroupsFromGit_AbsentFileIsNoop keeps the removal idempotent:
// a re-driven DeleteProject, or a project bootstrapped before group CRs existed,
// must produce no commit and no error.
func TestRemoveProjectGroupsFromGit_AbsentFileIsNoop(t *testing.T) {
	mgr := newKeycloakGroupsRepo(t, "survivor")

	path, sha, err := removeProjectGroupsFromGit(mgr, "never-had-groups", "delete kc groups", "bot", "bot@dada")
	if err != nil {
		t.Fatalf("removeProjectGroupsFromGit on absent file: %v", err)
	}
	if sha != "" {
		t.Errorf("sha = %q, want empty: nothing was there to remove", sha)
	}
	if path != renderer.ProjectGroupsGitPath("never-had-groups") {
		t.Errorf("path = %q, want the rendered org-groups path", path)
	}
}
