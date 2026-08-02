-- 093_audit_events_env_fk_set_null.sql
-- Migration 044 fixed exactly this class of bug for operations and
-- resource_snapshots: preview teardown deletes the environments row directly,
-- and any FK to environments with no ON DELETE action makes that delete fail
-- with 23503. Migration 068 then added audit_events.environment_id without an
-- ON DELETE action, which re-arms the same failure for every environment that
-- has ever been audited -- which, since 068, is every environment.
--
-- The failure is not a clean no-op: doDeletePreviewEnv removes the app folders
-- and the namespace-policy file and pushes that commit BEFORE deleting the row,
-- so a 23503 leaves a preview with no manifests, a live environments row and a
-- Failed operation.
--
-- SET NULL, not CASCADE: audit is a trail and must outlive the thing it
-- describes. Deleting audit rows to satisfy a teardown would destroy the only
-- record that the teardown happened, and the namespace name is preserved in
-- resource_name / metadata regardless. Same choice 044 made for operations.
--
-- Forward-only, idempotent (constraint name is stable from 068).

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_environment_id_fkey,
    ADD CONSTRAINT audit_events_environment_id_fkey
        FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE SET NULL;
