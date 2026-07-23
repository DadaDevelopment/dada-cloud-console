-- 041_upload_source.sql
-- Upload-deploy (slice A): an uploaded archive is a third git_repos provider,
-- alongside github/gitlab. No new columns — the S3 object key is stored in
-- clone_url as "s3://<bucket>/<key>", installation_id stays NULL, and
-- production_branch/repo_full_name get sentinel values ('upload',
-- 'upload/<app>'). The rest of the build/deploy pipeline (builds table →
-- poller → build-agent → Jenkins) is untouched.
--
-- Idempotent (matches the DROP IF EXISTS + fixed-name ADD CONSTRAINT pattern
-- of 014_preview_environments.sql: relaxing environments.type CHECK).

ALTER TABLE git_repos
    DROP CONSTRAINT IF EXISTS git_repos_provider_check;

ALTER TABLE git_repos
    ADD CONSTRAINT git_repos_provider_check
    CHECK (provider IN ('github', 'gitlab', 'archive'));
