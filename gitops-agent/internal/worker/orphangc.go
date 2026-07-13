package worker

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"

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

	for _, s := range snaps {
		liveBacked := live[snapKey{s.EnvID, s.Name}]

		mgr, resolved := repos[s.ProjectID]
		if !resolved {
			mgr = r.gcRepo(ctx, s.ProjectID)
			repos[s.ProjectID] = mgr
		}

		gitVerifiable := mgr != nil
		gitBacked := gitVerifiable && appGitExists(mgr, s.ProjectSlug, s.EnvSlug, s.Name)

		switch gcDecide(liveBacked, gitBacked, gitVerifiable, s.Phase, s.LastSyncedAt, s.OrphanedAt, now, r.cfg.OrphanMarkAfter, r.cfg.OrphanPurgeAfter) {
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
//       not yet Orphaned -> mark once last_synced is older than markAfter;
//       already Orphaned  -> purge once orphaned_at is older than purgeAfter.
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
// nil when the repo can't be verified this tick (integration lookup, decrypt, or
// clone failure) — callers treat nil as "unknown", never as "absent from git".
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
	_, _ = mgr.Pull()
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
