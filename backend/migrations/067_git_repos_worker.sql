-- 067_git_repos_worker.sql
-- The upload flow's headline case is a Telegram bot: a long-polling worker that
-- listens on nothing. Until now the upload handler forced port 8080 on every
-- archive, so a bot was rendered with a Service, an ingress and a surrogate
-- domain, and the console cheerfully offered the user a link that can only 502.
--
-- CreateApp has carried a `worker` flag since it shipped (backend apps.go), but
-- it lives in the operation payload and there is no way to reach it from the
-- upload path: the app is materialized by the build-agent, from git_repos, long
-- after the HTTP request that detected the framework is gone. This column is
-- that carrier — detection writes it at upload time, HandoffDeploy reads it when
-- the first successful build creates the app, and no default hostname is minted.
--
-- Default false so every existing row (and the whole github flow, which detects
-- a port from a live checkout) keeps its current behaviour exactly.
ALTER TABLE git_repos
    ADD COLUMN IF NOT EXISTS worker BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN git_repos.worker IS
    'App has no listening port (bot, queue consumer). Set by upload-time source detection; suppresses the auto surrogate domain when the first build creates the app.';
