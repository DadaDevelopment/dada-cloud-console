-- 110_audit_events_project_fk_set_null.sql
-- Same class of bug 093 fixed for environment_id, one level up: audit_events
-- FK-references projects(id) and operations(id) with NO ON DELETE action (001),
-- so DeleteProject could not delete a project that had ever been audited without
-- first getting the audit rows out of the way. wipeProjectRows solved that by
-- DELETEing them -- which means every project teardown destroyed the only record
-- of what the user did inside it. Measured 2026-08-09: after the ban sweep
-- removed 17 projects, all 18 accounts of that wave had zero audit_events while
-- ux_events still showed 13-1532 events each, so "did nothing" was an artifact
-- of the delete, not a fact about the users.
--
-- SET NULL, not CASCADE, for the same reason 044 and 093 chose it: audit is a
-- trail and must outlive the thing it describes. actor_id, action,
-- resource_kind, resource_name and metadata all survive, and wipeProjectRows now
-- stashes the deleted project's id and name into metadata before detaching, so a
-- detached row is still attributable.
--
-- Forward-only, idempotent (constraint names are the stable defaults from 001).

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_project_id_fkey,
    ADD CONSTRAINT audit_events_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL;

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_operation_id_fkey,
    ADD CONSTRAINT audit_events_operation_id_fkey
        FOREIGN KEY (operation_id) REFERENCES operations(id) ON DELETE SET NULL;
