package api

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// archiveRedetectInterval is how often the sweeper re-asks the detector about
// archives it once refused. Detection improves at deploy speed, not at request
// speed, so a slow pass is enough: the point is that a user who uploaded
// yesterday stops waiting on a fix that already shipped.
const archiveRedetectInterval = 15 * time.Minute

// archiveRedetectMinAge keeps the sweeper away from a failure the user is
// still looking at: they may be re-uploading a corrected archive right now, and
// a build appearing under their hands reads as the platform fighting them.
const archiveRedetectMinAge = 15 * time.Minute

// archiveRedetectMaxAge bounds how far back the pass reaches. A failure older
// than this is archaeology, not a user waiting, and replaying it would deploy
// an app its owner walked away from.
const archiveRedetectMaxAge = 7 * 24 * time.Hour

// archiveRedetectMaxAttempts stops a shape the detector still cannot read from
// being re-queued forever.
const archiveRedetectMaxAttempts = 3

// redetectCandidate is one build that died on "no_dockerfile" and whose archive
// is worth re-reading.
type redetectCandidate struct {
	BuildID       uuid.UUID
	GitRepoID     uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	Branch        string
	CloneURL      string
}

// StartArchiveRedetectSweeper re-reads the archives behind builds that failed
// with "no_dockerfile" and re-queues the ones the detector can now name.
//
// "no_dockerfile" is the platform saying it could not tell what to build. When
// the detector later learns that exact shape, the verdict frozen on the user's
// row does not change by itself: git_repos.framework_override stays NULL and
// every future build of that archive dies the same way. The only escape was the
// user pressing Rebuild — which asks the person we failed to notice that we
// stopped failing. On 2026-08-13 a live upload ("tree") sat red for hours
// behind exactly that gap.
//
// The retry is bounded the same way the platform-failure retry is: old enough
// that the user is not mid-upload, young enough that they plausibly still want
// the app, at most a few attempts, and only when re-detection actually produces
// a framework. When it produces nothing, the row is left exactly as it was.
func (h *Handler) StartArchiveRedetectSweeper(ctx context.Context) {
	if h.pool == nil || h.sourceUploader == nil || !h.sourceUploader.Enabled() {
		return
	}
	go h.runArchiveRedetectSweeper(ctx)
}

func (h *Handler) runArchiveRedetectSweeper(ctx context.Context) {
	ticker := time.NewTicker(archiveRedetectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sweepArchiveRedetect(ctx)
		}
	}
}

func (h *Handler) sweepArchiveRedetect(ctx context.Context) {
	if h.pool == nil || h.sourceUploader == nil || !h.sourceUploader.Enabled() {
		return
	}
	candidates, err := listRedetectCandidates(ctx, h.pool)
	if err != nil {
		log.Warn().Err(err).Msg("archive re-detect sweep: read candidates")
		return
	}

	for _, c := range candidates {
		framework := h.redetectArchiveFramework(ctx, c.GitRepoID, c.CloneURL)
		if framework == "" {
			continue
		}
		if err := h.requeueRedetectedBuild(ctx, c, framework); err != nil {
			log.Warn().Err(err).Str("build", c.BuildID.String()).Msg("archive re-detect sweep: re-queue failed")
			continue
		}
		log.Info().Str("app", c.AppName).Str("framework", framework).
			Str("previous_build", c.BuildID.String()).Msg("archive re-detected; build re-queued automatically")
	}
}

// listRedetectCandidates returns the failed-on-no_dockerfile builds worth
// re-reading: newest of their app, recent but not fresh, still without a
// framework, and under the attempt ceiling.
func listRedetectCandidates(ctx context.Context, pool *pgxpool.Pool) ([]redetectCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT b.id, g.id, g.project_id, b.environment_id, b.app_name, b.branch, g.clone_url
		FROM   builds b
		JOIN   git_repos g ON g.id = b.git_repo_id
		WHERE  b.status = 'failed'
		  AND  b.fail_reason = 'no_dockerfile'
		  AND  b.attempt < $3
		  AND  g.provider = 'archive'
		  AND  g.framework_override IS NULL
		  AND  b.finished_at < NOW() - make_interval(secs => $1)
		  AND  b.finished_at > NOW() - make_interval(secs => $2)
		  AND  NOT EXISTS (
		           SELECT 1 FROM builds n
		           WHERE  n.git_repo_id = b.git_repo_id
		             AND  n.app_name    = b.app_name
		             AND  n.created_at  > b.created_at
		       )
		LIMIT 20
	`, archiveRedetectMinAge.Seconds(), archiveRedetectMaxAge.Seconds(), archiveRedetectMaxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []redetectCandidate
	for rows.Next() {
		var c redetectCandidate
		if err := rows.Scan(&c.BuildID, &c.GitRepoID, &c.ProjectID, &c.EnvironmentID, &c.AppName, &c.Branch, &c.CloneURL); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// requeueRedetectedBuild queues the replacement build and its audit row in one
// statement, so a build the user never asked for can never appear without the
// trace that explains it.
func (h *Handler) requeueRedetectedBuild(ctx context.Context, c redetectCandidate, framework string) error {
	commitSHA := placeholderCommitSHA()
	headSHA := archiveUploadIDFromCloneURL(c.CloneURL)
	var head *string
	if headSHA != "" {
		head = &headSHA
	}
	_, err := h.pool.Exec(ctx, `
		WITH queued AS (
			INSERT INTO builds
			  (git_repo_id, environment_id, app_name, commit_sha, branch, head_sha, triggered_by, trigger, status, attempt)
			SELECT $1, $2, $3, $4, $5, $6, $7, 'manual', 'queued', b.attempt + 1
			FROM   builds b WHERE b.id = $8
			RETURNING id, attempt
		)
		INSERT INTO audit_events (actor_id, project_id, environment_id, action, resource_kind, resource_name, outcome, metadata, actor_type)
		SELECT $7, $9, $2, 'BuildAutoRetried', 'Build', $3, 'success',
		       jsonb_build_object(
		           'build_id', q.id::text, 'previous_build_id', $8::text,
		           'previous_fail_reason', 'no_dockerfile', 'redetected_framework', $10::text,
		           'attempt', q.attempt, 'branch', $5
		       ),
		       'system'
		FROM   queued q
	`, c.GitRepoID, c.EnvironmentID, c.AppName, commitSHA, c.Branch, head,
		systemDeployActorID, c.BuildID, c.ProjectID, framework)
	return err
}
