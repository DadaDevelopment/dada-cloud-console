package api

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/backend/internal/sourcedetect"
)

// archiveObjectKeyFromCloneURL splits a git_repos.clone_url of the form
// "s3://<bucket>/<key>" into its object key, or "" when the URL is not an
// archive upload location.
func archiveObjectKeyFromCloneURL(cloneURL string) string {
	const prefix = "s3://"
	if !strings.HasPrefix(cloneURL, prefix) {
		return ""
	}
	_, key, ok := strings.Cut(strings.TrimPrefix(cloneURL, prefix), "/")
	if !ok {
		return ""
	}
	return key
}

// redetectArchiveFramework re-runs framework detection against the archive the
// user already uploaded, and records the result on the git_repos row.
//
// Detection runs once, at upload time, and its verdict is frozen in
// git_repos.framework_override. When the detector cannot name a framework the
// column stays NULL and every later build of that archive dies with
// "no_dockerfile" — including builds triggered after we taught the detector
// that exact shape. That is what happened to a live upload of manifest-less
// python scripts on 2026-08-13 (fixed in 770a5197): the fix reached new
// uploads only, while the user's stored archive stayed undetectable forever.
//
// So a manual rebuild re-asks the question. It only ever fills a blank: a
// framework the user (or an earlier detection) already chose is never
// overwritten, and any failure here is logged and ignored — the build still
// gets queued, exactly as before.
func (h *Handler) redetectArchiveFramework(ctx context.Context, gitRepoID uuid.UUID, cloneURL string) string {
	if h.sourceUploader == nil || !h.sourceUploader.Enabled() {
		return ""
	}
	key := archiveObjectKeyFromCloneURL(cloneURL)
	if key == "" {
		return ""
	}

	data, err := h.sourceUploader.GetObject(ctx, key, uploadSourceMaxBytes)
	if err != nil {
		log.Warn().Err(err).Str("git_repo_id", gitRepoID.String()).Msg("rebuild: cannot read stored archive for re-detection")
		return ""
	}
	detected, err := sourcedetect.Detect(data)
	if err != nil || detected.Framework == "" {
		return ""
	}

	port := detected.Port
	if port <= 0 {
		port = 8080
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE git_repos
		    SET framework_override = $2, port = $3, worker = $4, updated_at = NOW()
		  WHERE id = $1 AND framework_override IS NULL`,
		gitRepoID, detected.Framework, port, isWorkerUpload(detected.Port),
	); err != nil {
		log.Warn().Err(err).Str("git_repo_id", gitRepoID.String()).Msg("rebuild: cannot store re-detected framework")
		return ""
	}
	log.Info().Str("git_repo_id", gitRepoID.String()).Str("framework", detected.Framework).
		Msg("rebuild: framework re-detected from stored archive")
	return detected.Framework
}
