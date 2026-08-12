-- 113_cloud_tasks_autofix_inflight_guard.sql
-- 2026-08-11 23:21, artemmendeleev on fonbet-value: clicked "Auto-fix with AI"
-- twice inside 6 seconds. TriggerAutofix had no in-flight check, so both
-- clicks minted an install token, called DadaAgent and inserted their own
-- cloud_tasks row. Two runs raced the same repo, and the nine follow-up chat
-- messages (one 26404 chars) are the user manually finishing what the pair of
-- runs did not resolve between them.
--
-- A partial unique index is the only guard that holds across replicas: this
-- backend runs more than one pod, so an in-memory mutex would only stop the
-- second click from hitting the SAME pod, not the second click landing on a
-- different one. The row in cloud_tasks is the authoritative "a run is
-- already going" fact -- status='running' for task_type='autofix' already
-- means exactly that, nothing new to invent.
--
-- Forward-only, additive.
CREATE UNIQUE INDEX IF NOT EXISTS idx_cloud_tasks_autofix_inflight
    ON cloud_tasks (project_id, environment_id, app_name)
    WHERE task_type = 'autofix' AND status = 'running';
