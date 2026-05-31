-- 008_servicedatabase_v2.sql
-- Migrate the database resource kind from the legacy cluster-scoped
-- "ServiceDatabase" to the namespaced "ServiceDatabaseV2" CRD
-- (platform.dada-tuda.ru/v1alpha1, spec.appRef + spec.database).
--
-- The gitops-agent renderer now emits kind: ServiceDatabaseV2, and the
-- console read/uniqueness paths query kind = 'ServiceDatabaseV2'. Rename
-- existing rows so databases created before this migration remain visible
-- and keep enforcing name uniqueness. Idempotent.

UPDATE resource_snapshots
   SET kind = 'ServiceDatabaseV2'
 WHERE kind = 'ServiceDatabase';

UPDATE operations
   SET resource_kind = 'ServiceDatabaseV2'
 WHERE resource_kind = 'ServiceDatabase';

UPDATE audit_events
   SET resource_kind = 'ServiceDatabaseV2'
 WHERE resource_kind = 'ServiceDatabase';
