package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// platformSelfHealInterval is the poll period of the self-heal sweeper. Slower
// than appHealthWatchInterval (3m) on purpose: the sweeper only ever acts on a
// signature already stamped into platformSelfHealFixes at build time, so
// nothing about its own state changes faster than a human editing this file
// and shipping a deploy -- there is no value in ticking at the watcher's pace.
const platformSelfHealInterval = 10 * time.Minute

// selfHealFix is one closed crash signature the platform knows how to cure by
// rebuilding: the platform-side bug behind cause_kind was fixed at FixedAt, so
// any app still running an image built before that instant is running the bad
// code and a fresh build (not a redeploy -- the image is what's broken, see
// queuePlatformSelfHealBuild below) clears it.
//
// This is a plain Go slice, not a database table, on purpose: the set of
// signatures the platform has ever closed changes at the rate of platform
// releases, is reviewed in the same PR as the fix itself, and needs no admin
// UI to edit. A DB table would just be a second place this list could drift
// from the code that actually implements each fix.
type selfHealFix struct {
	CauseKind string
	FixedAt   time.Time
	Note      string
}

// platformSelfHealFixes is the whole registry: append here, never edit an
// existing entry's CauseKind or FixedAt after it has shipped (either changes
// the meaning of every app_health_alerts row already claimed against it).
//
// app_entrypoint_import (backlog 0404): the build-time entrypoint autodetect
// ran a Python module's file directly (python app/main.py) instead of as a
// module on PYTHONPATH (python -m app.main), so any app whose entrypoint
// lived inside a package crashed on import at container start with an empty
// log and exit 0. Fixed by jenkins-pipelines d6dcafb (module-form entrypoint)
// and dada-argo b24dbbd7 (PYTHONPATH wiring), both live in prod by the
// FixedAt below. The fix is baked into the build, not the running pod, so an
// app already crash-looping on an old image stays down until it is rebuilt.
var platformSelfHealFixes = []selfHealFix{
	{
		CauseKind: notify.CauseKindEntrypointImport,
		FixedAt:   time.Date(2026, 8, 20, 5, 0, 0, 0, time.UTC),
		Note:      "entrypoint autodetect now runs the module form (python -m) with PYTHONPATH wired, not the file form that crashed on package-relative imports",
	},
}

// platformSelfHealWorker rebuilds apps stuck on an image affected by a
// signature platformSelfHealFixes says is closed. It reuses the exact
// "currently unhealthy" read every other consumer of app_health_alerts uses
// (COALESCE(last_seen_at, last_sent_at) inside appHealthAlertFreshWindow --
// see latestHealthAlertReasonWithTime, app_alerts.go, diagnose.go) rather than
// scanning the cluster itself, so it never disagrees with the console banner
// the owner is looking at.
type platformSelfHealWorker struct {
	h *Handler
}

// StartPlatformSelfHealWorker launches the self-heal sweeper goroutine.
// Gated on config so shipping this code and arming it against real customer
// repos stay two separate decisions -- same split as
// ReactivationCampaignEnabled and SignupEnabled. No-op without a pool, same
// as every other background loop's test/local-dev guard.
func (h *Handler) StartPlatformSelfHealWorker(ctx context.Context) {
	if h.pool == nil || h.cfg == nil || !h.cfg.PlatformSelfHealEnabled {
		return
	}
	if len(platformSelfHealFixes) == 0 {
		return
	}
	w := &platformSelfHealWorker{h: h}
	log.Printf("platform-selfheal: worker started interval=%s signatures=%d", platformSelfHealInterval, len(platformSelfHealFixes))
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyPlatformSelfHeal, "platform-selfheal", w.tick)
		t := time.NewTicker(platformSelfHealInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyPlatformSelfHeal, "platform-selfheal", w.tick)
			}
		}
	}()
}

// tick walks the whole registry once. A failure resolving candidates for one
// signature is logged and does not stop the rest of the registry, matching
// appHealthWatcher.tick's per-namespace isolation.
func (w *platformSelfHealWorker) tick(ctx context.Context) {
	for _, fix := range platformSelfHealFixes {
		candidates, err := w.h.platformSelfHealCandidates(ctx, fix)
		if err != nil {
			log.Printf("platform-selfheal: candidate query for cause_kind=%s failed: %v", fix.CauseKind, err)
			continue
		}
		for _, cand := range candidates {
			w.h.attemptPlatformSelfHeal(ctx, fix, cand)
		}
	}
}

// platformSelfHealCandidate is one app eligible for a self-heal rebuild
// against a specific fix.
type platformSelfHealCandidate struct {
	Namespace     string
	AppName       string
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	GitRepoID     uuid.UUID
	Branch        string
	Provider      string
	CloneURL      string
}

// platformSelfHealCandidates finds every app that is simultaneously: alerted
// on fix.CauseKind and still currently unhealthy by the console's own
// freshness rule; backed by a linked git repo (nothing to rebuild without
// one, and those apps are not fixable by this mechanism -- they are simply
// skipped, not retried forever); last built (if ever) before fix.FixedAt, so
// a rebuild is not wasted on an app that already picked up the fix; and not
// yet claimed by this sweeper for this exact signature.
//
// The git_repos join is INNER on purpose: it is both the "has a repo" filter
// and the source of the environment/branch a rebuild needs, in one query.
func (h *Handler) platformSelfHealCandidates(ctx context.Context, fix selfHealFix) ([]platformSelfHealCandidate, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT a.namespace, a.app_name, e.project_id, e.id, gr.id, gr.production_branch, gr.provider, gr.clone_url
		 FROM app_health_alerts a
		 JOIN environments e ON e.namespace = a.namespace AND e.runtime = 'k8s'
		 JOIN git_repos gr ON gr.project_id = e.project_id
		                   AND gr.environment_id = e.id
		                   AND gr.app_name = a.app_name
		 WHERE a.cause_kind = $1
		   AND COALESCE(a.last_seen_at, a.last_sent_at) > now() - make_interval(secs => $2)
		   AND a.selfheal_rebuilt_at IS NULL
		   AND NOT EXISTS (
		         SELECT 1 FROM builds b
		         WHERE b.git_repo_id = gr.id
		           AND b.status = 'success'
		           AND b.finished_at >= $3
		       )`,
		fix.CauseKind, appHealthAlertFreshWindow.Seconds(), fix.FixedAt,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []platformSelfHealCandidate
	for rows.Next() {
		var c platformSelfHealCandidate
		if err := rows.Scan(&c.Namespace, &c.AppName, &c.ProjectID, &c.EnvironmentID, &c.GitRepoID, &c.Branch, &c.Provider, &c.CloneURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// attemptPlatformSelfHeal claims the (namespace, app_name) row for fix and,
// on a successful claim, queues one rebuild.
//
// The claim is a conditional UPDATE on selfheal_rebuilt_at IS NULL, same
// claim-before-act shape as claimAppHealthAlertSlot: it stamps the attempt
// BEFORE the build is queued, not after it succeeds, so "exactly one attempt
// per app per signature" holds even if the INSERT INTO builds below fails --
// a failed enqueue gets a failure verdict, not a retry, because a queue
// failure here is a database problem worth paging on, not a transient state
// worth re-attempting against the same git repo forever.
//
// Audit is the two-row intent/verdict split documented at
// auditActionPlatformSelfHealRebuild: a pending row written before the
// INSERT INTO builds, and a success/failure row written after, both sharing
// one OperationID so they join.
func (h *Handler) attemptPlatformSelfHeal(ctx context.Context, fix selfHealFix, cand platformSelfHealCandidate) {
	claimed, err := claimPlatformSelfHealSlot(ctx, h.pool, cand.Namespace, cand.AppName, fix.CauseKind)
	if err != nil {
		log.Printf("platform-selfheal: claim %s/%s cause=%s failed: %v", cand.Namespace, cand.AppName, fix.CauseKind, err)
		return
	}
	if !claimed {
		return
	}

	opID := uuid.New()
	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     cand.ProjectID,
		EnvironmentID: cand.EnvironmentID,
		OperationID:   opID,
		Action:        auditActionPlatformSelfHealRebuild,
		ResourceKind:  "App",
		ResourceName:  cand.AppName,
		Outcome:       auditOutcomePending,
		Metadata: map[string]any{
			"cause_kind": fix.CauseKind,
			"fixed_at":   fix.FixedAt.Format(time.RFC3339),
			"note":       fix.Note,
			"namespace":  cand.Namespace,
			"claimed_by": "platform-selfheal-worker",
		},
	})

	buildID, err := queuePlatformSelfHealBuild(ctx, h.pool, cand)
	if err != nil {
		log.Printf("platform-selfheal: queue rebuild %s/%s cause=%s failed: %v", cand.Namespace, cand.AppName, fix.CauseKind, err)
		h.recordSystemAudit(ctx, auditEntry{
			ProjectID:     cand.ProjectID,
			EnvironmentID: cand.EnvironmentID,
			OperationID:   opID,
			Action:        auditActionPlatformSelfHealRebuild,
			ResourceKind:  "App",
			ResourceName:  cand.AppName,
			Outcome:       auditOutcomeFailure,
			Metadata: map[string]any{
				"cause_kind": fix.CauseKind,
				"reason":     "queue_failed",
				"detail":     err.Error(),
			},
		})
		return
	}

	log.Printf("platform-selfheal: queued rebuild %s/%s build=%s cause=%s", cand.Namespace, cand.AppName, buildID, fix.CauseKind)
	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     cand.ProjectID,
		EnvironmentID: cand.EnvironmentID,
		OperationID:   opID,
		Action:        auditActionPlatformSelfHealRebuild,
		ResourceKind:  "Build",
		ResourceName:  cand.AppName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"cause_kind": fix.CauseKind,
			"build_id":   buildID.String(),
		},
	})
}

// claimPlatformSelfHealSlot atomically claims the right to rebuild
// (namespace, appName) for causeKind, succeeding only when no attempt is
// recorded yet. Race-free across replicas the same way claimAppHealthAlertSlot
// is: of two concurrent claims on the same row, exactly one affects it.
func claimPlatformSelfHealSlot(ctx context.Context, pool *pgxpool.Pool, namespace, appName, causeKind string) (bool, error) {
	ct, err := pool.Exec(ctx,
		`UPDATE app_health_alerts
		 SET selfheal_rebuilt_at = now(), selfheal_rebuilt_cause_kind = $3
		 WHERE namespace = $1 AND app_name = $2 AND selfheal_rebuilt_at IS NULL`,
		namespace, appName, causeKind,
	)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// queuePlatformSelfHealBuild inserts one queued manual build for the
// candidate's linked repo, mirroring TriggerBuild's INSERT (builds.go) minus
// the HTTP/claims plumbing: triggered_by is left NULL, the same "system" value
// builds.triggered_by already documents, and trigger stays 'manual' -- the
// schema's CHECK constraint has no dedicated self-heal value, and the audit
// trail this file writes is what actually distinguishes a self-heal rebuild
// from a user clicking the rebuild button, not the trigger column.
//
// For an archive-provider repo (upload-without-git, the main product flow)
// head_sha is not optional the way it is for a github repo: TriggerBuild
// derives it from clone_url via archiveUploadIDFromCloneURL because that
// upload id is the ONLY pointer build-agent has to the source -- there is no
// git HEAD to resolve later the way there is for github. Queuing an archive
// rebuild without it would be a build build-agent cannot execute, silently.
func queuePlatformSelfHealBuild(ctx context.Context, pool *pgxpool.Pool, cand platformSelfHealCandidate) (uuid.UUID, error) {
	var headSHA *string
	if cand.Provider == "archive" {
		id := archiveUploadIDFromCloneURL(cand.CloneURL)
		if id == "" {
			return uuid.Nil, fmt.Errorf("archive repo %s has no upload id in clone_url, a rebuild would be unexecutable", cand.GitRepoID)
		}
		headSHA = &id
	}
	var buildID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, head_sha, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, $5, $6, NULL, 'manual', 'queued')
		 RETURNING id`,
		cand.GitRepoID, cand.EnvironmentID, cand.AppName, placeholderCommitSHA(), cand.Branch, headSHA,
	).Scan(&buildID)
	return buildID, err
}
