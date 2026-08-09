package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

// gcMassCapTestSetup seeds one project/environment plus n App snapshots that
// are all dead by every GC signal: no live pod (empty live map), no git
// manifest anywhere in the repo (an empty bare remote, so appGitExists and
// appGitExistsElsewhere both miss), and last_synced_at stamped far enough in
// the past that markAfter has already elapsed. It also points cfg at that
// empty remote as the default repo, so gcRepo resolves a real, verifiable
// clone -- gitVerifiable=true is required for gcDecide to ever return gcMark.
func gcMassCapTestSetup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) *StatusReconciler {
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
	historyRewriteWriteAndAdd(t, seedDir, wt, "unrelated.yaml", "unrelated: 1\n")
	if _, err := wt.Commit("unrelated", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit unrelated: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

	projectID, envID := seedOrphanGCProjectEnv(t, ctx, pool, "masscap", "prod", "masscap-prod")
	longAgo := time.Now().Add(-24 * time.Hour)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("app-%d", i)
		seedOrphanGCAppSnapshot(t, ctx, pool, projectID, envID, name, "Unknown")
		if _, err := pool.Exec(ctx,
			`UPDATE resource_snapshots SET last_synced_at = $1 WHERE project_id = $2 AND environment_id = $3 AND name = $4`,
			longAgo, projectID, envID, name); err != nil {
			t.Fatalf("backdate last_synced_at for %s: %v", name, err)
		}
	}

	cfg := &config.Config{
		DefaultRepoURL:   remoteDir,
		DefaultBranch:    historyRewriteTestBranch,
		OrphanMarkAfter:  time.Hour,
		OrphanPurgeAfter: 24 * time.Hour,
	}

	return &StatusReconciler{
		pool:     pool,
		cfg:      cfg,
		managers: map[string]*git.Manager{},
		gcBase:   t.TempDir(),
	}
}

func countOrphaned(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectSlug string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots rs
		 JOIN projects p ON p.id = rs.project_id
		 WHERE p.name = $1 AND rs.kind = 'App' AND rs.phase = 'Orphaned'`,
		projectSlug).Scan(&n); err != nil {
		t.Fatalf("count orphaned: %v", err)
	}
	return n
}

// TestReconcileOrphans_MassMarkBurstRefused is the falsification test for
// gcMassMarkLimit: 8 snapshots are all dead by every signal (no live pod, no
// git manifest, last_synced_at long past markAfter) -- well over the limit of
// 5 -- so the whole burst must be refused. Zero rows may flip to Orphaned.
func TestReconcileOrphans_MassMarkBurstRefused(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	r := gcMassCapTestSetup(t, ctx, pool, 8)
	lastGC = time.Time{}
	r.reconcileOrphans(ctx, map[snapKey]bool{})

	got := countOrphaned(t, ctx, pool, "masscap")
	if got != 0 {
		t.Fatalf("orphaned rows = %d, want 0 -- an 8-row mark burst (over gcMassMarkLimit=%d) must be refused entirely", got, gcMassMarkLimit)
	}
}

// TestReconcileOrphans_UnderCapMarksNormally proves the cap is conditional,
// not a blanket "GC disabled": 3 dead snapshots, under the limit of 5, must
// all still get marked Orphaned. Without this case, a broken breaker that
// always suppresses marking (or never runs at all) would still pass the
// burst-refused assertion above.
func TestReconcileOrphans_UnderCapMarksNormally(t *testing.T) {
	pool := newOrphanGCTestPool(t)
	ctx := context.Background()

	r := gcMassCapTestSetup(t, ctx, pool, 3)
	lastGC = time.Time{}
	r.reconcileOrphans(ctx, map[snapKey]bool{})

	got := countOrphaned(t, ctx, pool, "masscap")
	if got != 3 {
		t.Fatalf("orphaned rows = %d, want 3 -- a 3-row burst is under gcMassMarkLimit=%d and must be marked in full", got, gcMassMarkLimit)
	}
}
