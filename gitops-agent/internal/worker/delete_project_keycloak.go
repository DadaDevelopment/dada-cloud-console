package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// groupRetireWaitTimeout bounds how long a teardown waits for ArgoCD to deliver
// deletionPolicy: Delete to the live Group CRs before it gives up on removing the
// file. ArgoCD's default reconcile period is three minutes.
const groupRetireWaitTimeout = 4 * time.Minute

// groupRetirePollInterval is how often the live CRs are re-read while waiting.
const groupRetirePollInterval = 10 * time.Second

// retireProjectGroupsInGit rewrites a doomed project's Keycloak group manifest
// with deletionPolicy: Delete and pushes it, so the prune that follows the file
// removal reaches through the Crossplane provider and deletes the groups in
// Keycloak instead of merely forgetting them.
//
// The rendered CRs are Orphan while a project lives: an accidental prune (a bad
// sync, a chart refactor) must never take a real group tree and its memberships
// with it. That policy is what left project client-a's five groups Ready in
// Keycloak weeks after the project was deleted.
//
// The policy is flipped through git, not with a patch on the live CR: ArgoCD
// self-heal restores spec from git within a couple of minutes (measured on
// org-ssa, 2026-08-11: a kubectl patch to Delete was back to Orphan in under two
// minutes), so a patched CR is Orphan again by the time the prune runs.
//
// The rewritten manifest carries no Memberships CRs. Their external state lives
// inside the group, which the group deletion takes with it.
func retireProjectGroupsInGit(mgr *git.Manager, slug, commitMsg, botName, botEmail string) (string, string, error) {
	path := renderer.ProjectGroupsGitPath(slug)
	if _, err := mgr.ReadFile(path); errors.Is(err, os.ErrNotExist) {
		return path, "", nil
	} else if err != nil {
		return path, "", fmt.Errorf("read KC groups file: %w", err)
	}
	yaml, err := renderer.RenderProjectGroups(renderer.ProjectGroupSpec{
		ProjectSlug:    slug,
		DeletionPolicy: renderer.DeletionPolicyDelete,
	})
	if err != nil {
		return path, "", fmt.Errorf("render KC groups for teardown: %w", err)
	}
	sha, err := mgr.CommitAndPush(path, yaml, commitMsg, botName, botEmail)
	if err != nil {
		return path, "", fmt.Errorf("git commit KC groups teardown policy: %w", err)
	}
	return path, sha, nil
}

// waitProjectGroupsRetired blocks until every live Group CR of the project reports
// deletionPolicy: Delete, or the wait times out. A CR that does not exist counts
// as retired — a project can be torn down before its groups were ever created.
//
// Returns false when the policy has not landed everywhere. The caller must then
// leave the manifest in git: removing it while a CR is still Orphan is exactly
// the bug this path exists to fix, and the manifest already carries Delete, so a
// later re-drive finishes the job.
//
// With no cluster client (local dev, off-cluster runs) the wait reports true: the
// agent cannot observe convergence and the pre-fix behaviour — remove the file,
// possibly orphaning the groups — is still better than leaving dead manifests in
// the IAM state repo forever.
func (w *DBWatcher) waitProjectGroupsRetired(ctx context.Context, slug string) bool {
	if w.clients == nil || w.clients.Dynamic == nil {
		return true
	}
	names := renderer.ProjectGroupCRNames(slug)
	deadline := time.Now().Add(groupRetireWaitTimeout)
	for {
		pending := 0
		for _, name := range names {
			live, err := w.clients.Dynamic.Resource(keycloakGroupGVR).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if !apierrors.IsNotFound(err) {
					log.Warn().Err(err).Str("project", slug).Str("group", name).
						Msg("db-watcher: read KC group CR while waiting for teardown policy")
					pending++
				}
				continue
			}
			policy, _, _ := unstructured.NestedString(live.Object, "spec", "deletionPolicy")
			if policy != renderer.DeletionPolicyDelete {
				pending++
			}
		}
		if pending == 0 {
			return true
		}
		if time.Now().After(deadline) {
			log.Warn().Str("project", slug).Int("pending", pending).
				Msg("db-watcher: KC group CRs still not marked for deletion; leaving manifest in git")
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(groupRetirePollInterval):
		}
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

// deleteProjectKeycloakGroups tears down a deleted project's Keycloak groups in
// the two steps ArgoCD forces: commit deletionPolicy Delete, wait for it to reach
// the live CRs, then remove the manifest so the prune deletes the real groups.
//
// Best-effort throughout: every failure is logged and swallowed so a project
// teardown is never blocked by the IAM state repo.
func (w *DBWatcher) deleteProjectKeycloakGroups(ctx context.Context, op db.Operation, slug string) {
	defaultMgr, ok := w.managers[w.cfg.DefaultRepoURL]
	if !ok {
		log.Warn().Str("project", slug).Msg("db-watcher: no default repo manager; KC group CRs left in git")
		return
	}
	if err := defaultMgr.EnsureCloned(); err != nil {
		log.Warn().Err(err).Str("project", slug).Msg("db-watcher: clone default repo for KC group teardown")
		return
	}

	retireMsg := fmt.Sprintf(
		"[DADA Console] Mark KC groups of project %s for deletion\n\nOperation: %s\n", slug, op.ID,
	)
	path, retireSha, err := retireProjectGroupsInGit(defaultMgr, slug, retireMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		log.Warn().Err(err).Str("project", slug).Str("path", path).Msg("db-watcher: mark KC groups for deletion")
		return
	}
	if retireSha == "" {
		log.Debug().Str("project", slug).Str("path", path).Msg("db-watcher: no KC group CRs in git to remove")
		return
	}
	w.recordGroupCommit(ctx, op, defaultMgr, path, retireMsg, retireSha, slug)
	log.Info().Str("project", slug).Str("path", path).Str("sha", retireSha).
		Msg("db-watcher: KC group CRs marked for deletion (Orphan -> Delete)")

	if !w.waitProjectGroupsRetired(ctx, slug) {
		return
	}

	removeMsg := fmt.Sprintf(
		"[DADA Console] Delete KC groups for project %s\n\nOperation: %s\n", slug, op.ID,
	)
	path, removeSha, err := removeProjectGroupsFromGit(defaultMgr, slug, removeMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		log.Warn().Err(err).Str("project", slug).Str("path", path).Msg("db-watcher: remove KC group CRs from git")
		return
	}
	if removeSha == "" {
		return
	}
	w.recordGroupCommit(ctx, op, defaultMgr, path, removeMsg, removeSha, slug)
	log.Info().Str("project", slug).Str("path", path).Str("sha", removeSha).
		Msg("db-watcher: removed KC group CRs from git")
}

// recordGroupCommit stores one teardown commit against the operation, logging a
// failed insert rather than failing the teardown.
func (w *DBWatcher) recordGroupCommit(
	ctx context.Context, op db.Operation, mgr *git.Manager, path, msg, sha, slug string,
) {
	opID := op.ID
	if err := db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
		path, msg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent"); err != nil {
		log.Warn().Err(err).Str("project", slug).Str("sha", sha).
			Msg("db-watcher: record KC group teardown commit")
	}
}
