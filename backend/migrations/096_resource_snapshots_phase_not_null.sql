-- 096_resource_snapshots_phase_not_null.sql
-- resource_snapshots.phase has been nullable since 001, but every Go reader
-- scans it into a plain `string` (models.ResourceSnapshot.Phase, and the
-- GROUP BY phase aggregate in admin_overview.go). pgx cannot scan SQL NULL
-- into *string, so ONE row with phase IS NULL turns into a 500 for every
-- caller that touches it: /admin/overview answered
-- "failed to aggregate projects" and the whole platform board rendered zeros,
-- and the owning project's app list answered "failed to scan app".
--
-- Prod had exactly one such row (2026-07-31, project volume-export-test-8b671f4e),
-- seeded by an integration test whose INSERT omitted the phase column.
--
-- Nothing in the codebase ever writes NULL deliberately: the two production
-- writers (gitops-agent db.UpsertSnapshot, backend databases.go) always pass a
-- phase string. Making the column NOT NULL states that contract in the schema,
-- so a writer that forgets the column fails loudly at write time instead of
-- poisoning every read of the table.
--
-- Backfill uses 'Unknown', the same label the readers already substitute for an
-- empty phase; the DEFAULT is '' so an omitted column keeps the empty-string
-- meaning readers already handle rather than inventing a phase.
--
-- Forward-only, idempotent.

UPDATE resource_snapshots SET phase = 'Unknown' WHERE phase IS NULL;

ALTER TABLE resource_snapshots ALTER COLUMN phase SET DEFAULT '';

ALTER TABLE resource_snapshots ALTER COLUMN phase SET NOT NULL;
