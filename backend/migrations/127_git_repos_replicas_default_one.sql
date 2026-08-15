-- 127_git_repos_replicas_default_one.sql
-- git_repos.replicas has defaulted to 2 since 023_git_repos_app_spec.sql, and
-- that default -- not any code path, not any user -- is what put `replicas: 2`
-- into argo-infra for every app the console materialized from a repo.
--
-- The path: an INSERT that omits the column (the archive-upload flow in
-- uploadsource.go names port and worker but not replicas) gets 2 from the
-- schema; build-agent's repoSelect reads r.replicas; HandoffDeploy puts it in
-- the first-build CreateApp payload; the gitops-agent renderer writes it to
-- values.yaml and the console commits it under its own name. Confirmed in
-- argo-infra history: "[DADA Console] Create App megafactory" (45865c14),
-- a2ahub-landing, dada-development-site, upload-static-test, routine-upload-*
-- all landed with replicas: 2, while apps created through POST /api/v1/apps in
-- the same window landed with 1.
--
-- Nobody ever asked for two. The create form sends 1, CreateApp defaults to 1,
-- and ConnectGitRepo was already patched to send 1 in 52f00c47 ("default
-- git-imported apps to 1 replica") -- a symptom fix that left the schema, and
-- therefore every other INSERT, still minting 2.
--
-- The backfill covers rows born under the old default: git_repos.replicas is
-- read at FIRST-build materialization, so a repo connected months ago but not
-- yet built would still create a two-replica app after the default changed.
-- Only the exact old default is touched: a repo deliberately set to 3+ keeps
-- its value, and rows already at 1 are untouched.
--
-- Live app specs are not rewritten here. They live in resource_snapshots and in
-- git, were cut to 1 by hand in argo-infra 914bc1e3, and the status reconciler
-- patches summary_json.replicas from the observed Deployment on every pass
-- (statusreconciler.go), so the snapshot converges to what the cluster actually
-- runs without a data migration guessing at it.
--
-- Forward-only, idempotent.

ALTER TABLE git_repos
    ALTER COLUMN replicas SET DEFAULT 1;

UPDATE git_repos SET replicas = 1 WHERE replicas = 2;

COMMENT ON COLUMN git_repos.replicas IS
    'Replica count handed to the first-build CreateApp for this repo. Defaults to 1: a second pod is a resource the user did not ask for and, for a worker, an outright outage (see build-agent workerReplicas).';
