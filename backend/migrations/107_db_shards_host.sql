-- Network address of every shard.
--
-- The registry knew which instance a database lives on but not how to reach it,
-- because nothing in the control plane ever dialled a shard directly: the
-- composition selects a provider-sql ProviderConfig by shard name and the
-- address stays inside that ProviderConfig secret. The connection router needs
-- the opposite - a datname -> host:port table - and hand-maintaining it in the
-- pg-router chart is the one place where a shard move can silently point a
-- tenant at the wrong instance. So the address moves into the registry the
-- placement decision already lives in, and the router renders its table from
-- there.
ALTER TABLE db_shards ADD COLUMN IF NOT EXISTS host TEXT NOT NULL DEFAULT '';
ALTER TABLE db_shards ADD COLUMN IF NOT EXISTS port INTEGER NOT NULL DEFAULT 5432;

-- Backfill mirrors the addresses the provider-sql ProviderConfigs already use
-- (crossplane-platform-system values.yaml). A shard with an empty host is
-- simply absent from the rendered table: an unaddressable shard must fall back
-- to the wildcard rather than emit a line pointing nowhere.
UPDATE db_shards SET host = 'postgresql.databases.svc.cluster.local'
 WHERE name = 'shard-1' AND host = '';
UPDATE db_shards SET host = 'pg-shard-0-postgresql.databases.svc.cluster.local'
 WHERE name = 'shard-0' AND host = '';
