-- 088_git_repos_created_by_backfill_by_project.sql
--
-- 086 backfilled git_repos.created_by from the audit row that recorded the
-- repo being connected, joined on (environment_id, resource_name). That join
-- can never match the rows 086 targets: audit_events.environment_id was only
-- populated for ConnectGitRepo later, so every historic connect row -- exactly
-- the rows written while created_by was still NULL -- carries
-- environment_id = NULL [live psql 08-03: all three remaining NULL repos have
-- a successful ConnectGitRepo row with environment_id IS NULL]. 086 applied
-- cleanly and changed nothing for them, and its projects.owner_id fallback did
-- not fire either (owner_id is NULL on those projects).
--
-- The consequence is not cosmetic: build-agent's handoffActor
-- (build-agent/internal/db/deploy.go) attributes a push-triggered redeploy to
-- git_repos.created_by, and falls back to the synthetic system actor when it is
-- NULL. Cohort analysis excludes that actor, so those deploys stay invisible in
-- the funnel -- the very blindness the attribution fix was meant to end
-- (observed live: DeployImageVersion on reels-tracker 08-02 18:34Z landed on
-- the zero uuid with initiator='system').
--
-- This migration re-runs the same backfill on the key the historic rows DO
-- carry: (project_id, resource_name). git_repos is unique per
-- (project_id, environment_id, app_name), so a project that connected the same
-- app in two environments resolves both to the same human -- correct, since the
-- audit row names who connected that app in that project. Same conservative
-- rules as 086: only rows still NULL are touched, only actors that still exist
-- in users are accepted, the earliest connect wins, and a repo with no evidence
-- is left NULL rather than pointed at the system user.
DO $$
BEGIN
  IF to_regclass('public.git_repos') IS NULL
     OR to_regclass('public.audit_events') IS NULL
     OR to_regclass('public.users') IS NULL THEN
    RAISE NOTICE 'git_repos_created_by_backfill_by_project skipped: a required table (git_repos/audit_events/users) does not exist yet';
    RETURN;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_name = 'git_repos' AND column_name = 'created_by'
  ) THEN
    RAISE NOTICE 'git_repos_created_by_backfill_by_project skipped: git_repos.created_by does not exist yet (037 not applied)';
    RETURN;
  END IF;

  UPDATE git_repos gr
     SET created_by = resolved.actor_id
    FROM (
      SELECT DISTINCT ON (ae.project_id, ae.resource_name)
             ae.project_id, ae.resource_name, ae.actor_id
        FROM audit_events ae
        JOIN users u ON u.id = ae.actor_id
       WHERE ae.outcome = 'success'
         AND ae.project_id IS NOT NULL
         AND (
              (ae.action = 'ConnectGitRepo' AND ae.resource_kind = 'GitRepo')
           OR (ae.action = 'UploadSourceArchive' AND ae.resource_kind = 'Build')
         )
       ORDER BY ae.project_id, ae.resource_name, ae.created_at ASC
    ) resolved
   WHERE gr.created_by IS NULL
     AND gr.project_id = resolved.project_id
     AND gr.app_name = resolved.resource_name;
END $$;
