package worker

import (
	"context"
	"errors"
	"fmt"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// keycloakGroupGVR is the Crossplane managed resource behind /orgs/<slug> and its
// role subgroups, rendered by renderer.RenderProjectGroups.
var keycloakGroupGVR = schema.GroupVersionResource{
	Group:    "group.keycloak.crossplane.io",
	Version:  "v1alpha1",
	Resource: "groups",
}

// retireProjectKeycloakGroups flips deletionPolicy from Orphan to Delete on the
// live Group CRs of a project that is being torn down, so the ArgoCD prune that
// follows the YAML removal deletes the groups in Keycloak instead of merely
// forgetting about them.
//
// The rendered CRs are deliberately Orphan while a project lives: an accidental
// prune (a bad sync, a chart refactor) must never take a real group tree and its
// memberships with it. That same policy is what left project client-a's five
// groups Ready in Keycloak weeks after the project was deleted — the CRs were
// gone, the groups were not. Deletion is the one moment where Delete is the
// correct policy, and it is applied only to the doomed project's own CRs.
//
// Best-effort by design: no cluster client (local dev), a CR that was never
// created, or a denied patch all degrade to the pre-existing behaviour (groups
// orphaned in Keycloak) rather than failing the teardown. The patch must land
// BEFORE the YAML is removed from git — once the file is gone ArgoCD may prune
// at any moment, and a CR still marked Orphan at that instant is unreachable.
func (w *DBWatcher) retireProjectKeycloakGroups(ctx context.Context, slug string) {
	if w.clients == nil || w.clients.Dynamic == nil {
		return
	}
	for _, name := range renderer.ProjectGroupCRNames(slug) {
		live, err := w.clients.Dynamic.Resource(keycloakGroupGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				log.Warn().Err(err).Str("project", slug).Str("group", name).
					Msg("db-watcher: read KC group CR for teardown; group may stay in Keycloak")
			}
			continue
		}
		if policy, _, _ := unstructured.NestedString(live.Object, "spec", "deletionPolicy"); policy == "Delete" {
			continue
		}
		patch := []byte(`{"spec":{"deletionPolicy":"Delete"}}`)
		if _, err := w.clients.Dynamic.Resource(keycloakGroupGVR).
			Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			log.Warn().Err(err).Str("project", slug).Str("group", name).
				Msg("db-watcher: patch KC group deletionPolicy; group may stay in Keycloak")
			continue
		}
		log.Info().Str("project", slug).Str("group", name).
			Msg("db-watcher: KC group CR marked for deletion (Orphan -> Delete)")
	}
}

// removeProjectGroupsFromGit git-rm's a project's Keycloak group CRs from the
// argo-infra state repo and pushes. Returns the removed path and the commit sha,
// with an empty sha when the file was already absent (idempotent re-drive, or a
// project created before the group CRs existed).
func removeProjectGroupsFromGit(mgr *git.Manager, slug, commitMsg, botName, botEmail string) (string, string, error) {
	path := renderer.ProjectGroupsGitPath(slug)
	if _, err := mgr.ReadFile(path); errors.Is(err, os.ErrNotExist) {
		return path, "", nil
	} else if err != nil {
		return path, "", fmt.Errorf("read KC groups file: %w", err)
	}
	sha, err := mgr.RemoveAndPush([]string{path}, commitMsg, botName, botEmail)
	if err != nil {
		return path, "", fmt.Errorf("git remove KC groups file: %w", err)
	}
	return path, sha, nil
}

// deleteProjectGroupsFile removes the deleted project's Keycloak group CRs from
// the default (argo-infra) manager and records the commit, mirroring the
// bootstrap half in bootstrapProject. Best-effort: every failure is logged and
// swallowed so a project teardown is never blocked by the IAM state repo.
func (w *DBWatcher) deleteProjectGroupsFile(ctx context.Context, op db.Operation, slug string) {
	defaultMgr, ok := w.managers[w.cfg.DefaultRepoURL]
	if !ok {
		log.Warn().Str("project", slug).Msg("db-watcher: no default repo manager; KC group CRs left in git")
		return
	}
	if err := defaultMgr.EnsureCloned(); err != nil {
		log.Warn().Err(err).Str("project", slug).Msg("db-watcher: clone default repo for KC group teardown")
		return
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete KC groups for project %s\n\nOperation: %s\n", slug, op.ID,
	)
	path, sha, err := removeProjectGroupsFromGit(defaultMgr, slug, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		log.Warn().Err(err).Str("project", slug).Str("path", path).Msg("db-watcher: remove KC group CRs from git")
		return
	}
	if sha == "" {
		log.Debug().Str("project", slug).Str("path", path).Msg("db-watcher: no KC group CRs in git to remove")
		return
	}
	opID := op.ID
	if err := db.InsertCommit(ctx, w.pool, sha, defaultMgr.RepoURL(), defaultMgr.Branch(),
		path, commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent"); err != nil {
		log.Warn().Err(err).Str("project", slug).Msg("db-watcher: record KC group removal commit")
	}
	log.Info().Str("project", slug).Str("path", path).Str("sha", sha).
		Msg("db-watcher: removed KC group CRs from git")
}
