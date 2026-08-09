package worker

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// gcMinInterval throttles the orphan sweep: the status reconciler ticks every
// ~30s but the GC only needs to run every few minutes, and each run pulls every
// project repo. 2 minutes keeps git load low while catching orphans promptly.
const gcMinInterval = 2 * time.Minute

// lastGC is package-tick state guarded implicitly by the single reconciler
// goroutine (tick is sequential), not a field, to keep the struct edit minimal.
var lastGC time.Time

// gcMassMarkLimit caps how many App snapshots one sweep may soft-delete. A
// healthy estate loses apps one at a time; a double-digit mark burst has never
// been a real mass deletion, it has been one bad signal applied to every row at
// once (2026-08-08: a single sweep marked the whole `platform` inventory and the
// purge that followed destroyed ~40 rows of live infrastructure). Marking is the
// one-way door — an unmarked row can always be marked on the next tick two
// minutes later, but a purged row is gone — so the sweep refuses the whole burst
// and screams instead of guessing which of the rows it got right.
const gcMassMarkLimit = 5

// reconcileOrphans garbage-collects App snapshots that no DeleteApp op ever
// cleaned up — rows stranded when an app is re-homed/renamed between projects
// and the incremental git-watcher missed the delete side of the diff (the
// example-project vs fin-core "profi" duplication). It is the missing full-state
// reconcile: git (app.yaml) is the authoritative "should exist" signal for k8s
// apps, the live Deployment set is a secondary keep-alive guard.
//
// Two-stage soft delete, so a transient git/pod gap can never lose data:
//   - not-live AND not-git-backed AND last_synced older than OrphanMarkAfter
//     -> phase=Orphaned + orphaned_at stamp (reversible, visible in the UI);
//   - already Orphaned AND orphaned_at older than OrphanPurgeAfter -> row deleted.
//
// An app that comes back (git manifest or live pod reappears) is un-marked.
// Uncertainty never prunes: if the project repo can't be verified this tick and
// there's no live pod, the snapshot is left untouched. Compose (VM) apps are
// excluded upstream by ListGCAppSnapshots — their desired spec is DB-owned.
func (r *StatusReconciler) reconcileOrphans(ctx context.Context, live map[snapKey]bool) {
	now := time.Now()
	if !lastGC.IsZero() && now.Sub(lastGC) < gcMinInterval {
		return
	}
	lastGC = now

	snaps, err := db.ListGCAppSnapshots(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("orphan-gc: list app snapshots")
		return
	}
	if len(snaps) == 0 {
		return
	}

	repos := map[uuid.UUID]*git.Manager{}
	var marked, cleared, purged int

	decisions := make([]gcAction, len(snaps))
	markCount := 0
	for i, s := range snaps {
		liveBacked := live[snapKey{s.EnvID, s.Name}]

		mgr, resolved := repos[s.ProjectID]
		if !resolved {
			mgr = r.gcRepo(ctx, s.ProjectID)
			repos[s.ProjectID] = mgr
		}

		gitVerifiable := mgr != nil
		gitBacked := gitVerifiable && appGitExists(mgr, s.ProjectSlug, s.EnvSlug, s.Name)
		if gitVerifiable && !gitBacked {
			if where, ok := appGitExistsElsewhere(mgr, s.ProjectSlug, s.EnvSlug, s.Name); ok {
				log.Warn().Str("project", s.ProjectSlug).Str("env", s.EnvSlug).
					Str("app", s.Name).Str("manifest", where).
					Msg("orphan-gc: manifest lives outside this row's project/env path — row misfiled, not deleted")
				gitBacked = true
			}
		}

		decisions[i] = gcDecide(liveBacked, gitBacked, gitVerifiable, s.Phase, s.LastSyncedAt, s.OrphanedAt, now, r.cfg.OrphanMarkAfter, r.cfg.OrphanPurgeAfter)
		if decisions[i] == gcMark {
			markCount++
		}
	}

	if markCount > gcMassMarkLimit {
		log.Error().Int("would_mark", markCount).Int("limit", gcMassMarkLimit).Int("snapshots", len(snaps)).
			Msg("orphan-gc: mass-mark burst refused — a shared signal, not this many deleted apps; no row soft-deleted this sweep")
	}

	for i, s := range snaps {
		action := decisions[i]
		if action == gcMark && markCount > gcMassMarkLimit {
			continue
		}

		switch action {
		case gcClear:
			if err := db.ClearSnapshotOrphan(ctx, r.pool, s.ID); err != nil {
				log.Error().Err(err).Str("app", s.Name).Msg("orphan-gc: clear")
				continue
			}
			cleared++
		case gcMark:
			if err := db.MarkSnapshotOrphaned(ctx, r.pool, s.ID, now); err != nil {
				log.Error().Err(err).Str("app", s.Name).Msg("orphan-gc: mark")
				continue
			}
			log.Info().Str("project", s.ProjectSlug).Str("env", s.EnvSlug).
				Str("app", s.Name).Msg("orphan-gc: marked orphaned (no git manifest, no live pod)")
			marked++
		case gcPurge:
			if err := db.DeleteSnapshotByID(ctx, r.pool, s.ID); err != nil {
				log.Error().Err(err).Str("app", s.Name).Msg("orphan-gc: purge")
				continue
			}
			log.Info().Str("project", s.ProjectSlug).Str("env", s.EnvSlug).
				Str("app", s.Name).Msg("orphan-gc: purged orphaned snapshot")
			purged++
		case gcNone:
		}
	}

	if marked+cleared+purged > 0 {
		log.Info().Int("marked", marked).Int("cleared", cleared).Int("purged", purged).
			Msg("orphan-gc: swept app snapshots")
	}

	r.reconcileChildOrphans(ctx, repos, now)
}

// gcChildKinds are the child snapshot kinds the orphan GC reconciles: exactly
// the set doDeleteApp cascades. Other mirrored kinds (Secret, Client, Realm,
// KC/infra objects) are deliberately excluded — their git home is the infra
// repo (dada-argo), not the per-project tenant tree this GC can verify, so a
// git-absence test against the tenant tree would mislabel live infra rows.
var gcChildKinds = []string{"PublicApi", "Ingress", "ServiceDatabaseV2", "S3Bucket", "AIModel"}

// reconcileChildOrphans is the child-kind counterpart of the App sweep above:
// it garbage-collects PublicApi/Ingress/ServiceDatabaseV2/S3Bucket/AIModel
// snapshots stranded when the DeleteApp cascade missed them (its Execs ignore
// errors) or a watcher re-upserted a row during the Argo teardown window — the
// nextjs-fhvx20 incident: orphan PublicApi+Ingress rows polluted delete-impact
// previews for 5 days until manual DB surgery.
//
// A child should exist while ANY of three signals holds:
//   - a non-Orphaned App snapshot in the same project+env claims it
//     (ParentExists, computed in SQL by the doDeleteApp owning-link predicate);
//   - its name (or git-native spelling, e.g. dotted fqdn) appears anywhere in
//     the environment's git subtree;
//   - the backing cluster object is live (per-kind cluster-wide LIST).
//
// Uncertainty never prunes, same as the App sweep: an unresolvable repo or a
// failed kind LIST makes the row unverifiable (gcDecide's gitVerifiable=false
// path), so it is left untouched. The same two-stage mark/purge grace and the
// same repos cache (one clone per project per sweep) are reused.
func (r *StatusReconciler) reconcileChildOrphans(ctx context.Context, repos map[uuid.UUID]*git.Manager, now time.Time) {
	snaps, err := db.ListGCChildSnapshots(ctx, r.pool, gcChildKinds)
	if err != nil {
		log.Error().Err(err).Msg("orphan-gc: list child snapshots")
		return
	}
	if len(snaps) == 0 {
		return
	}

	liveNames := r.childLiveNames(ctx)
	treeCache := map[string]string{}
	var marked, cleared, purged int

	for _, s := range snaps {
		mgr, resolved := repos[s.ProjectID]
		if !resolved {
			mgr = r.gcRepo(ctx, s.ProjectID)
			repos[s.ProjectID] = mgr
		}

		liveSet, kindListable := liveNames[s.Kind]
		nameInTree := mgr != nil && anyTermIn(
			treeContent(filepath.Join(mgr.LocalPath(), renderer.EnvBaseGitPath(s.ProjectSlug, s.EnvSlug)), treeCache),
			s.SearchTerms,
		)
		liveBacked, gitBacked, verifiable := childGCSignals(
			s.ParentExists, kindListable, kindListable && liveSet[s.Name],
			mgr != nil, nameInTree,
		)

		switch gcDecide(liveBacked, gitBacked, verifiable, s.Phase, s.LastSyncedAt, s.OrphanedAt, now, r.cfg.OrphanMarkAfter, r.cfg.OrphanPurgeAfter) {
		case gcClear:
			if err := db.ClearSnapshotOrphan(ctx, r.pool, s.ID); err != nil {
				log.Error().Err(err).Str("kind", s.Kind).Str("name", s.Name).Msg("orphan-gc: clear child")
				continue
			}
			cleared++
		case gcMark:
			if err := db.MarkSnapshotOrphaned(ctx, r.pool, s.ID, now); err != nil {
				log.Error().Err(err).Str("kind", s.Kind).Str("name", s.Name).Msg("orphan-gc: mark child")
				continue
			}
			log.Info().Str("project", s.ProjectSlug).Str("env", s.EnvSlug).
				Str("kind", s.Kind).Str("name", s.Name).
				Msg("orphan-gc: marked child orphaned (no parent app, no git manifest, not live)")
			marked++
		case gcPurge:
			if err := db.DeleteSnapshotByID(ctx, r.pool, s.ID); err != nil {
				log.Error().Err(err).Str("kind", s.Kind).Str("name", s.Name).Msg("orphan-gc: purge child")
				continue
			}
			log.Info().Str("project", s.ProjectSlug).Str("env", s.EnvSlug).
				Str("kind", s.Kind).Str("name", s.Name).
				Msg("orphan-gc: purged orphaned child snapshot")
			purged++
		case gcNone:
		}
	}

	if marked+cleared+purged > 0 {
		log.Info().Int("marked", marked).Int("cleared", cleared).Int("purged", purged).
			Msg("orphan-gc: swept child snapshots")
	}
}

// childGCSignals folds the child-specific evidence into the three inputs
// gcDecide already understands. The key move: cluster listability is folded
// into the verifiability bit — death requires proving BOTH git-absence AND
// cluster-absence, so a failed kind LIST must park the row exactly like an
// unresolvable repo does. Aliveness (parent/live/git hits) short-circuits in
// gcDecide before verifiability is consulted, so a live row is still cleared
// even when the other signal source is down.
func childGCSignals(parentExists, kindListable, clusterLive, repoResolved, nameInTree bool) (liveBacked, gitBacked, verifiable bool) {
	return parentExists || (kindListable && clusterLive),
		repoResolved && nameInTree,
		repoResolved && kindListable
}

// childLiveNames lists the cluster objects backing each GC'd child kind and
// returns kind → set of live object names. A kind absent from the map means
// its LIST failed this sweep — callers must treat its rows as unverifiable,
// never as cluster-absent. All five CRs/objects are cluster-scoped or listed
// across all namespaces in one call, and snapshot names equal object names
// (the same match rule the status reconcilers use).
func (r *StatusReconciler) childLiveNames(ctx context.Context) map[string]map[string]bool {
	out := map[string]map[string]bool{}

	crds := map[string]schema.GroupVersionResource{
		"PublicApi":         pgvr("publicapis"),
		"ServiceDatabaseV2": pgvr("servicedatabasesv2"),
		"S3Bucket":          pgvr("s3buckets"),
		"AIModel":           inferenceServiceGVR,
	}
	for kind, gvr := range crds {
		list, err := r.clients.Dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Warn().Err(err).Str("kind", kind).Msg("orphan-gc: list cluster objects (kind skipped)")
			continue
		}
		set := make(map[string]bool, len(list.Items))
		for i := range list.Items {
			set[list.Items[i].GetName()] = true
		}
		out[kind] = set
	}

	ings, err := r.client.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("orphan-gc: list ingresses (kind skipped)")
	} else {
		set := make(map[string]bool, len(ings.Items))
		for i := range ings.Items {
			set[ings.Items[i].Name] = true
		}
		out["Ingress"] = set
	}
	return out
}

// treeContent returns the concatenated contents of every file under root,
// cached per root for the duration of one sweep. A missing or unreadable root
// yields "" — the caller's git-backed test then simply fails, which is safe
// because verifiability is decided by repo resolution, not by this scan.
func treeContent(root string, cache map[string]string) string {
	if v, ok := cache[root]; ok {
		return v
	}
	var sb strings.Builder
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, readErr := os.ReadFile(path); readErr == nil {
			sb.Write(b)
			sb.WriteByte('\n')
		}
		return nil
	})
	cache[root] = sb.String()
	return cache[root]
}

// anyTermIn reports whether any non-empty search term occurs in content.
func anyTermIn(content string, terms []string) bool {
	for _, t := range terms {
		if t != "" && strings.Contains(content, t) {
			return true
		}
	}
	return false
}

// gcAction is one orphan-GC decision for a single App snapshot.
type gcAction int

const (
	gcNone  gcAction = iota // leave untouched
	gcClear                 // reverse a soft-delete: app is alive again
	gcMark                  // soft-delete: stamp phase=Orphaned
	gcPurge                 // physically delete the row
)

// gcDecide is the pure orphan-GC decision, extracted so the branching is
// table-testable. Rules:
//   - alive (a live pod OR an app.yaml in git) -> clear if currently Orphaned, else none.
//   - uncertain (git not verifiable AND no live pod) -> none; never prune on doubt.
//   - dead (git verifiably absent AND no live pod):
//     not yet Orphaned -> mark once last_synced is older than markAfter;
//     already Orphaned  -> purge once orphaned_at is older than purgeAfter.
func gcDecide(liveBacked, gitBacked, gitVerifiable bool, phase string,
	lastSynced time.Time, orphanedAt *time.Time, now time.Time,
	markAfter, purgeAfter time.Duration,
) gcAction {
	if liveBacked || gitBacked {
		if phase == "Orphaned" {
			return gcClear
		}
		return gcNone
	}
	if !gitVerifiable {
		return gcNone
	}
	if phase != "Orphaned" {
		if now.Sub(lastSynced) >= markAfter {
			return gcMark
		}
		return gcNone
	}
	if orphanedAt != nil && now.Sub(*orphanedAt) >= purgeAfter {
		return gcPurge
	}
	return gcNone
}

// gcRepo resolves and freshens the GC's own clone of a project's repo, returning
// nil when the repo can't be verified this tick (integration lookup, decrypt,
// clone, or sync failure) — callers treat nil as "unknown", never as "absent
// from git". The sync is a hard reset (see Manager.SyncHard) because every GC
// signal below is a filesystem probe: a worktree that lags the remote answers
// "still in git" for deleted apps and freezes their snapshots forever.
func (r *StatusReconciler) gcRepo(ctx context.Context, projectID uuid.UUID) *git.Manager {
	mgr, err := r.gcManagerFor(ctx, projectID)
	if err != nil || mgr == nil {
		if err != nil {
			log.Warn().Err(err).Str("project", projectID.String()).Msg("orphan-gc: resolve repo")
		}
		return nil
	}
	if err := mgr.EnsureCloned(); err != nil {
		log.Warn().Err(err).Str("project", projectID.String()).Msg("orphan-gc: clone")
		return nil
	}
	if err := mgr.SyncHard(); err != nil {
		log.Warn().Err(err).Str("project", projectID.String()).Msg("orphan-gc: sync repo (rows left unverified)")
		return nil
	}
	return mgr
}

// gcManagerFor mirrors DBWatcher.managerFor but keeps a SEPARATE manager cache
// cloned under r.gcBase, so the GC's read-only pulls never race the git-watcher's
// shared working copy. Projects with no integration row use the platform default
// repo, exactly like the shared-repo write path.
func (r *StatusReconciler) gcManagerFor(ctx context.Context, projectID uuid.UUID) (*git.Manager, error) {
	integration, err := db.GetIntegration(ctx, r.pool, projectID)
	if err != nil {
		return nil, err
	}

	var rc git.RepoConfig
	if integration == nil {
		rc = git.RepoConfig{
			RepoURL:   r.cfg.DefaultRepoURL,
			Branch:    r.cfg.DefaultBranch,
			Username:  r.cfg.DefaultUsername,
			Token:     r.cfg.DefaultToken,
			LocalBase: r.gcBase,
		}
	} else {
		token, err := crypto.DecryptToken(r.cfg.EncryptionKey, integration.TokenEncrypted)
		if err != nil {
			return nil, err
		}
		rc = git.RepoConfig{
			RepoURL:   integration.RepoURL,
			Branch:    integration.Branch,
			Username:  integration.Provider,
			Token:     token,
			LocalBase: r.gcBase,
		}
	}

	if mgr, ok := r.managers[rc.RepoURL]; ok {
		return mgr, nil
	}
	mgr := git.New(rc)
	r.managers[rc.RepoURL] = mgr
	return mgr, nil
}

// appGitExists reports whether the app's app.yaml is present in the worktree —
// the authoritative "this k8s app should exist" signal. doDeleteApp removes this
// file, so its absence means the app was deleted (or never git-backed).
func appGitExists(mgr *git.Manager, projectSlug, envSlug, app string) bool {
	rel := renderer.AppGitPath(projectSlug, envSlug, app)
	_, err := os.Stat(filepath.Join(mgr.LocalPath(), rel))
	return err == nil
}

// appGitExistsElsewhere reports whether an app.yaml for this app name exists
// anywhere else in the repo, and where. appGitExists asks a single question —
// "is the manifest at the path built from THIS ROW's project and env slug?" —
// so it answers "deleted from git" for a row that is merely filed under the
// wrong project or environment. That is not a hypothetical: the platform's
// adopted infrastructure (jenkins, nexus) had snapshots under `platform` while
// its manifests lived under `delivery`, and the sweep read the mismatch as a
// deletion and purged the rows (2026-08-08, ~40 rows of live infra lost).
//
// A manifest found under another project is deliberately treated as "alive":
// the row is misfiled, and re-homing it is a repair someone can make later,
// while a purge is a decision nobody can take back. The cost of being wrong
// here is a duplicate row in the console; the cost of being wrong the other way
// is deleted inventory for a running system.
func appGitExistsElsewhere(mgr *git.Manager, projectSlug, envSlug, app string) (string, bool) {
	own := renderer.AppGitPath(projectSlug, envSlug, app)
	pattern := filepath.Join(mgr.LocalPath(),
		renderer.AppGitPath("*", "*", app))

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", false
	}
	for _, m := range matches {
		rel, err := filepath.Rel(mgr.LocalPath(), m)
		if err != nil || rel == own {
			continue
		}
		return rel, true
	}
	return "", false
}
