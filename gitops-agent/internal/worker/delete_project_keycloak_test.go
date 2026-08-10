package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// TestRetireProjectKeycloakGroups_FlipsOrphanToDelete is the regression for the
// half of the teardown that git alone cannot do: removing the YAML prunes the
// CRs, but deletionPolicy Orphan means the groups survive in Keycloak (project
// client-a, deleted 2026-08-10, still had all five groups Ready). Every Group CR
// of the doomed project must be flipped to Delete, and no other project's CR may
// be touched.
func TestRetireProjectKeycloakGroups_FlipsOrphanToDelete(t *testing.T) {
	ctx := context.Background()

	var objs []runtime.Object
	for _, name := range renderer.ProjectGroupCRNames("client-a") {
		objs = append(objs, keycloakGroupCR(name, "Orphan"))
	}
	objs = append(objs, keycloakGroupCR("org-survivor", "Orphan"))

	dyn := newKeycloakGroupFake(objs...)
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: dyn}}

	w.retireProjectKeycloakGroups(ctx, "client-a")

	names := renderer.ProjectGroupCRNames("client-a")
	if len(names) != 5 {
		t.Fatalf("ProjectGroupCRNames = %v, want the org parent plus 4 role subgroups", names)
	}
	for _, name := range names {
		live, err := dyn.Resource(keycloakGroupGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get %q after retire: %v", name, err)
		}
		policy, _, _ := unstructured.NestedString(live.Object, "spec", "deletionPolicy")
		if policy != "Delete" {
			t.Errorf("%q deletionPolicy = %q; want Delete, else the prune leaves the group alive in Keycloak", name, policy)
		}
	}

	live, err := dyn.Resource(keycloakGroupGVR).Get(ctx, "org-survivor", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get survivor group: %v", err)
	}
	if policy, _, _ := unstructured.NestedString(live.Object, "spec", "deletionPolicy"); policy != "Orphan" {
		t.Errorf("foreign project group deletionPolicy = %q; want it untouched at Orphan", policy)
	}
}

// TestRetireProjectKeycloakGroups_ToleratesMissingAndAlreadyRetired covers the
// re-drive and the partially-provisioned project: a CR that was never created
// must not fail the teardown, and one already at Delete stays as it is.
func TestRetireProjectKeycloakGroups_ToleratesMissingAndAlreadyRetired(t *testing.T) {
	ctx := context.Background()

	names := renderer.ProjectGroupCRNames("acme")
	dyn := newKeycloakGroupFake(
		keycloakGroupCR(names[0], "Delete"),
		keycloakGroupCR(names[1], "Orphan"),
	)
	w := &DBWatcher{clients: &dadak8s.Clients{Dynamic: dyn}}

	w.retireProjectKeycloakGroups(ctx, "acme")

	for _, name := range names[:2] {
		live, err := dyn.Resource(keycloakGroupGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get %q: %v", name, err)
		}
		if policy, _, _ := unstructured.NestedString(live.Object, "spec", "deletionPolicy"); policy != "Delete" {
			t.Errorf("%q deletionPolicy = %q; want Delete", name, policy)
		}
	}
	for _, name := range names[2:] {
		if _, err := dyn.Resource(keycloakGroupGVR).Get(ctx, name, metav1.GetOptions{}); err == nil {
			t.Errorf("%q must stay absent; the retire step never creates CRs", name)
		}
	}
}

// TestRetireProjectKeycloakGroups_NoClientIsNoop guards local dev and any
// off-cluster run: with no dynamic client the retire must return silently rather
// than panic, so a project teardown still completes (degrading only to orphaned
// Keycloak groups, the pre-fix behaviour).
func TestRetireProjectKeycloakGroups_NoClientIsNoop(t *testing.T) {
	(&DBWatcher{}).retireProjectKeycloakGroups(context.Background(), "acme")
	(&DBWatcher{clients: &dadak8s.Clients{}}).retireProjectKeycloakGroups(context.Background(), "acme")
}

// newKeycloakGroupsRepo seeds a bare remote carrying the org-groups YAML of two
// projects and returns a Manager cloned from it, so removal is exercised against
// a real git push rather than a stub.
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
