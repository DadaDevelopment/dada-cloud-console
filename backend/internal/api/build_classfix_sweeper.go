package api

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// buildClassFixInterval mirrors the archive re-detect sweeper: a platform fix
// lands at deploy speed, not at request speed, so a 15-minute pass is enough
// to notice a class of failure just went away.
const buildClassFixInterval = 15 * time.Minute

// buildClassFixMaxAge bounds how far back the sweep reaches. A failure older
// than this is archaeology: the user who hit it has almost certainly moved on,
// and resurrecting their build now reads as the platform acting on a dead
// intent rather than finishing a live one.
const buildClassFixMaxAge = 7 * 24 * time.Hour

// buildClassFixMaxAttempts stops a build from being re-queued forever if the
// registered fix turns out not to actually cover its case.
const buildClassFixMaxAttempts = 3

// classFix names one failure class that a platform-side change (pipeline
// template, detector, build-agent image, ...) has closed. ID identifies the
// fix in audit metadata. FailReason is the builds.fail_reason value the fix
// closes. Signature, when non-empty, must appear as a case-insensitive
// substring of builds.error_message for a build to qualify; leave it empty
// when fail_reason alone is unambiguous. Framework, when non-empty, must
// equal git_repos.framework_override -- some fixes only close one detected
// shape (a pipeline template bug that only fires for one framework, say),
// and matching on fail_reason plus a generic error substring alone would
// retry a different framework's honestly-broken Dockerfile right along with
// it. FixedAt is the moment the fix landed in prod, UTC -- not the moment it
// was written or merged: everything that failed before FixedAt is a
// candidate, everything that failed after it failed for some other reason
// and must be left alone. Note carries the prose an operator reading this six
// months from now will not otherwise have: what broke, what the fix was, and
// a pointer to the commit or incident.
type classFix struct {
	ID         string
	FailReason string
	Signature  string
	Framework  string
	FixedAt    time.Time
	Note       string
}

// buildClassFixRegistry lists failure classes closed by a platform-side fix,
// whose victims should be re-queued automatically instead of staying dead
// until the user notices we already fixed it and presses Rebuild themselves.
//
// To add an entry once a fix is confirmed live in prod, append a classFix
// literal naming a short stable ID, the builds.fail_reason value it closes,
// an error_message substring (or "" if fail_reason alone is enough), a
// git_repos.framework_override value if the fix is framework-specific (or ""
// if not), the UTC instant the fix landed in prod (not when it was written),
// and a Note explaining what broke and what the fix was.
//
// Sweeping is driven entirely by this slice (or, in tests, an injected copy of
// it) so a registry entry never depends on prod data to be exercised.
var buildClassFixRegistry = []classFix{
	{
		ID:         "static-npm-template-20260821",
		FailReason: "dockerfile_build_failed",
		Signature:  "npm install",
		Framework:  "static",
		FixedAt:    time.Date(2026, 8, 21, 22, 18, 46, 0, time.UTC),
		Note:       "static template called npm unconditionally; jenkins-pipelines ad8ff3a, develop branch.",
	},
}

// classFixCandidate is one build that died on a failure class a platform fix
// has since closed.
type classFixCandidate struct {
	BuildID       uuid.UUID
	GitRepoID     uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	Branch        string
	HeadSHA       *string
	Attempt       int
	FailReason    string
	ClassFixID    string
}

// StartBuildClassFixSweeper re-queues builds that failed with a class of
// error a platform-side fix has since closed. The registry (build fix
// template, detector, build-agent image, ...) can name a class as fixed, but
// nothing about naming it moves the frozen "failed" row sitting under the
// user who hit it: builds.status does not change by itself, no new build
// appears, and no mail goes out. The user who hit the class first is left
// looking at a dead app forever, even though a retry today would succeed.
//
// The live case that motivated this (sess-0822e): tarotreaderhimu@gmail.com's
// app best-marriage-astrologer-in-guwahati failed three times on 2026-08-21
// on npm install inside a Dockerfile we generated ourselves. The fix shipped
// 2026-08-22. Without this sweeper the app stays unbuilt until the owner
// notices and presses Rebuild -- which, for a user who has already concluded
// the platform is broken, does not happen.
func (h *Handler) StartBuildClassFixSweeper(ctx context.Context) {
	if h.pool == nil {
		return
	}
	go h.runBuildClassFixSweeper(ctx, buildClassFixRegistry)
}

func (h *Handler) runBuildClassFixSweeper(ctx context.Context, registry []classFix) {
	ticker := time.NewTicker(buildClassFixInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sweepBuildClassFix(ctx, registry)
		}
	}
}

func (h *Handler) sweepBuildClassFix(ctx context.Context, registry []classFix) {
	if h.pool == nil || len(registry) == 0 {
		return
	}
	candidates, err := listClassFixCandidates(ctx, h.pool, registry)
	if err != nil {
		log.Warn().Err(err).Msg("build class-fix sweep: read candidates")
		return
	}
	for _, c := range candidates {
		if err := h.requeueClassFixedBuild(ctx, c); err != nil {
			log.Warn().Err(err).Str("build", c.BuildID.String()).Str("class_fix", c.ClassFixID).
				Msg("build class-fix sweep: re-queue failed")
			continue
		}
		log.Info().Str("app", c.AppName).Str("class_fix", c.ClassFixID).
			Str("previous_build", c.BuildID.String()).Msg("build class fix closed; build re-queued automatically")
	}
}

// listClassFixCandidates returns, for every entry in registry, the failed
// builds that entry's fix covers: matching fail_reason (and error_message
// signature, when set), finished before the fix landed, recent enough that
// the user plausibly still wants the app, under the attempt ceiling, and the
// newest build of their app -- so a user who already retried by hand, or who
// already succeeded, is left untouched.
func listClassFixCandidates(ctx context.Context, pool *pgxpool.Pool, registry []classFix) ([]classFixCandidate, error) {
	var candidates []classFixCandidate
	for _, fix := range registry {
		rows, err := pool.Query(ctx, `
			SELECT b.id, g.id, g.project_id, b.environment_id, b.app_name, b.branch, b.head_sha, b.attempt
			FROM   builds b
			JOIN   git_repos g ON g.id = b.git_repo_id
			WHERE  b.status = 'failed'
			  AND  b.fail_reason = $1
			  AND  ($2 = '' OR b.error_message ILIKE '%' || $2 || '%')
			  AND  ($6 = '' OR g.framework_override = $6)
			  AND  b.attempt < $3
			  AND  b.finished_at < $4
			  AND  b.finished_at > NOW() - make_interval(secs => $5)
			  AND  NOT EXISTS (
			           SELECT 1 FROM builds n
			           WHERE  n.git_repo_id = b.git_repo_id
			             AND  n.app_name    = b.app_name
			             AND  n.created_at  > b.created_at
			       )
			LIMIT 20
		`, fix.FailReason, fix.Signature, buildClassFixMaxAttempts, fix.FixedAt, buildClassFixMaxAge.Seconds(), fix.Framework)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c classFixCandidate
			if err := rows.Scan(&c.BuildID, &c.GitRepoID, &c.ProjectID, &c.EnvironmentID, &c.AppName, &c.Branch, &c.HeadSHA, &c.Attempt); err != nil {
				rows.Close()
				return nil, err
			}
			c.FailReason = fix.FailReason
			c.ClassFixID = fix.ID
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return candidates, nil
}

// requeueClassFixedBuild queues the replacement build and its audit row in
// one statement, the same discipline requeueRedetectedBuild follows: an
// automatic build the user never asked for can never appear without the
// trace that explains it.
//
// The new row gets a fresh placeholder commit_sha, not the failed build's own
// commit_sha: builds carries UNIQUE(git_repo_id, commit_sha), and the failed
// build already holds that value in that repo, so reusing it would collide.
// head_sha (the real, human-meaningful commit) is carried over unchanged.
func (h *Handler) requeueClassFixedBuild(ctx context.Context, c classFixCandidate) error {
	commitSHA := placeholderCommitSHA()
	_, err := h.pool.Exec(ctx, `
		WITH queued AS (
			INSERT INTO builds
			  (git_repo_id, environment_id, app_name, commit_sha, branch, head_sha, triggered_by, trigger, status, attempt)
			SELECT $1, $2, $3, $4, $5, b.head_sha, $6, 'class_fix', 'queued', b.attempt + 1
			FROM   builds b WHERE b.id = $7
			RETURNING id, attempt
		)
		INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata, actor_type)
		SELECT $6, $8, $2, 'BuildAutoRetried', 'Build', $3, 'success',
		       jsonb_build_object(
		           'build_id', q.id::text, 'previous_build_id', $7::text,
		           'previous_fail_reason', $9::text, 'class_fix_id', $10::text,
		           'attempt', q.attempt, 'branch', $5
		       ),
		       'system'
		FROM   queued q
	`, c.GitRepoID, c.EnvironmentID, c.AppName, commitSHA, c.Branch,
		systemDeployActorID, c.BuildID, c.ProjectID, c.FailReason, c.ClassFixID)
	return err
}
