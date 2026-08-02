-- Manual builds insert a synthetic commit_sha ("manual-<timestamp>") to satisfy
-- the UNIQUE(git_repo_id, commit_sha) idempotency constraint, since there is no
-- real commit tied to the trigger yet. commit_sha itself can never be overwritten
-- with the real value afterwards: that would collide with a push build already
-- sitting on that same commit.
--
-- head_sha holds the real resolved HEAD commit sha that build-agent looks up
-- after the fact for manual builds, so the console has something honest to show
-- instead of the "manual-" placeholder. It is nullable and best-effort: a lookup
-- failure, an anonymous/private repo without a usable token, or a non-github
-- provider leaves it NULL.
ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS head_sha TEXT;
