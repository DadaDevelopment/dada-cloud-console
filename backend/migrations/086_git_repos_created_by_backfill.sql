-- 086_git_repos_created_by_backfill.sql
--
-- 037 added git_repos.created_by but never backfilled it. Every row created
-- before both live writers (ConnectGitRepo, UploadSourceArchive) started
-- setting it still has created_by = NULL, so build-agent's handoffActor
-- (backend/internal/db/deploy.go) falls back to the fixed system actor for a
-- push-triggered redeploy on those repos, and the audit-path analysis loses
-- the human who actually connected the repo.
--
-- Resolution, conservative and idempotent -- only rows still NULL are ever
-- touched, and re-running this file is a no-op the second time:
--
--   1. the actor of the EARLIEST successful audit_events row that recorded
--      this exact repo being connected/uploaded. There is no repo_id column
--      on audit_events, so the join is by (environment_id, resource_name)
--      -- resource_name is the app_name, and git_repos has one row per
--      (project_id, environment_id, app_name) by UNIQUE constraint (013).
--      Both live writers record this shape:
--        ConnectGitRepo  -> resource_kind='GitRepo', ResourceName=app_name
--        UploadSourceArchive -> resource_kind='Build', ResourceName=app_name
--      Only an actor that still exists in users is accepted.
--   2. else the project's owner_id (projects.owner_id), when set and it
--      still resolves to a user.
--   3. else left NULL -- an internal/ownerless project. Never invent an
--      owner, and never write the synthetic system user id here: that would
--      be indistinguishable from a real backfill and defeat the whole point
--      of this migration.
DO $$
BEGIN
  IF to_regclass('public.git_repos') IS NULL
     OR to_regclass('public.audit_events') IS NULL
     OR to_regclass('public.projects') IS NULL
     OR to_regclass('public.users') IS NULL THEN
    RAISE NOTICE 'git_repos_created_by_backfill skipped: a required table (git_repos/audit_events/projects/users) does not exist yet';
    RETURN;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_name = 'git_repos' AND column_name = 'created_by'
  ) THEN
    RAISE NOTICE 'git_repos_created_by_backfill skipped: git_repos.created_by does not exist yet (037 not applied)';
    RETURN;
  END IF;

  UPDATE git_repos gr
     SET created_by = resolved.actor_id
    FROM (
      SELECT DISTINCT ON (ae.environment_id, ae.resource_name)
             ae.environment_id, ae.resource_name, ae.actor_id
        FROM audit_events ae
        JOIN users u ON u.id = ae.actor_id
       WHERE ae.outcome = 'success'
         AND (
              (ae.action = 'ConnectGitRepo' AND ae.resource_kind = 'GitRepo')
           OR (ae.action = 'UploadSourceArchive' AND ae.resource_kind = 'Build')
         )
       ORDER BY ae.environment_id, ae.resource_name, ae.created_at ASC
    ) resolved
   WHERE gr.created_by IS NULL
     AND gr.environment_id = resolved.environment_id
     AND gr.app_name = resolved.resource_name;

  UPDATE git_repos gr
     SET created_by = p.owner_id
    FROM projects p
   WHERE gr.project_id = p.id
     AND gr.created_by IS NULL
     AND p.owner_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM users u WHERE u.id = p.owner_id);
END $$;
