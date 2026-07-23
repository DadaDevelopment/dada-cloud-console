-- 044_preview_teardown_fks.sql
-- Preview environment teardown (DeletePreviewEnv worker + TTL reaper) deletes
-- the environments row directly, but two FKs from 001_initial_schema.sql had
-- no ON DELETE action and made every such delete fail with 23503 (seen live
-- 2026-07-23: op 26f053db, the DeletePreviewEnv operation row itself pinned
-- the environment it was deleting).
--
--   operations.environment_id        -> SET NULL: operations are an audit/state
--                                      trail and must outlive the environment.
--   resource_snapshots.environment_id -> CASCADE: snapshots mirror live gitops
--                                      state of that environment; without the
--                                      environment they are dead rows (the git
--                                      watcher repopulates on recreation).
--
-- Forward-only, idempotent (constraint names are stable from 001).

ALTER TABLE operations
    DROP CONSTRAINT IF EXISTS operations_environment_id_fkey,
    ADD CONSTRAINT operations_environment_id_fkey
        FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE SET NULL;

ALTER TABLE resource_snapshots
    DROP CONSTRAINT IF EXISTS resource_snapshots_environment_id_fkey,
    ADD CONSTRAINT resource_snapshots_environment_id_fkey
        FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE CASCADE;
