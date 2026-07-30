-- 061_boxes.sql
-- Dada Box, slice 1: the box object and its lifecycle skeleton.
--
-- D1 (docs/plans/2026-07-29-box-backend-slice.md): a box OWNS exactly one
-- environments row with runtime='box', type='dev'. environments.id is the
-- identity carrier in this schema -- env_vars, resource_snapshots,
-- domain_hostnames and operations are all keyed by it -- so crystallization
-- later flips that SAME row to runtime='vm', type='prod', app_server_id=<new>.
-- Attached databases, buckets, injected env and published hostnames therefore
-- survive promotion with zero data migration. Minting a new environment row on
-- promotion would recreate the exact "crystallization lost my state" failure the
-- product exists to prevent.
--
-- The price of D1 is that 'box' becomes a third value of environments.runtime,
-- so every branch on that column had to be audited. That audit is a separate,
-- blocking task (see the same plan) -- not a side effect of this file.

-- ① Extend the environments runtime CHECK to accept 'box'.
--
-- Prod can already carry a drifted constraint from an earlier bootstrap whose
-- schema_migrations is missing this version, and in that state the migration
-- role may not own environments (the lesson of 004_vm_track.sql). So the failure
-- is swallowed ONLY when 'box' is already accepted; if a runtime CHECK exists
-- that does not admit 'box', the migration must fail loudly rather than leave
-- the API inserting rows the database will reject at runtime.
--
-- The DROP+ADD pair is inside one PL/pgSQL block on purpose: the implicit
-- subtransaction around an EXCEPTION handler rolls the DROP back if the ADD
-- raises, so environments is never left with no runtime constraint at all.
DO $$
BEGIN
    ALTER TABLE environments DROP CONSTRAINT IF EXISTS environments_runtime_check;
    ALTER TABLE environments ADD CONSTRAINT environments_runtime_check
        CHECK (runtime IN ('k8s', 'vm', 'box'));
EXCEPTION
    WHEN insufficient_privilege THEN
        -- Re-raise unless the desired outcome already holds. "Already holds"
        -- means: no surviving CHECK on runtime forbids 'box'. We inspect the
        -- constraint definition rather than probing with an INSERT because a
        -- probe would need a real project_id and would write to a tenant table.
        IF EXISTS (
            SELECT 1 FROM pg_constraint
             WHERE conrelid = 'public.environments'::regclass
               AND contype  = 'c'
               AND pg_get_constraintdef(oid) LIKE '%runtime%'
               AND pg_get_constraintdef(oid) NOT LIKE '%''box''%'
        ) THEN
            RAISE;
        END IF;
END;
$$;

-- ② The boxes table.
CREATE TABLE IF NOT EXISTS boxes (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,

    -- UNIQUE, not merely a FK: two boxes must never share the identity carrier
    -- that crystallization promotes in place. A Deleted box keeps its row, and
    -- therefore keeps its environment_id, so that environment can never be
    -- re-bound to a second box -- which is the point.
    environment_id UUID        NOT NULL UNIQUE REFERENCES environments(id) ON DELETE CASCADE,

    name           TEXT        NOT NULL,
    image          TEXT        NOT NULL,
    profile        TEXT        NOT NULL,
    region         TEXT        NOT NULL DEFAULT '',

    -- Lifecycle phases. Deliberately the same vocabulary as the phase label of
    -- dada_boxes{phase} (internal/metrics/box.go) so the gauge is a GROUP BY on
    -- this column and cannot drift from the state machine.
    status         TEXT        NOT NULL DEFAULT 'Requested'
                   CHECK (status IN ('Requested', 'Booting', 'Ready', 'Idle',
                                     'Sleeping', 'Crystallizing', 'Failed',
                                     'Deleting', 'Deleted')),
    error_message  TEXT        NOT NULL DEFAULT '',

    -- Runtime coordinates. Opaque handles owned by the runtime: the control
    -- plane relays them and never interprets them (internal/box.Instance).
    -- Written by the box-agent webhook, never by the tenant.
    instance_ref   TEXT        NOT NULL DEFAULT '',
    node_ref       TEXT        NOT NULL DEFAULT '',
    ssh_host       TEXT        NOT NULL DEFAULT '',
    ssh_port       INTEGER,
    mcp_url        TEXT        NOT NULL DEFAULT '',

    -- TTL and hibernation clocks. Seconds rather than intervals so the reaper's
    -- arithmetic is comparable to the runbook's numbers without casting.
    ttl_seconds          INTEGER NOT NULL DEFAULT 28800,  -- 8h hard TTL from claim
    idle_timeout_seconds INTEGER NOT NULL DEFAULT 900,    -- 15min idle -> sleep
    expires_at     TIMESTAMPTZ,
    last_active_at TIMESTAMPTZ,
    slept_at       TIMESTAMPTZ,

    -- Spend cap. NULL = plan default; reaching it SUSPENDS, never deletes, so
    -- the customer's data survives their own runaway.
    spend_cap_rub  NUMERIC(12,2),

    -- Newest box-agent sample, taken OUTSIDE the guest. The authoritative
    -- activity/billing signal: a heartbeat from inside the guest may only ask
    -- for MORE billing, never less, so it can never live here alone.
    last_sample_json JSONB,
    last_sample_at   TIMESTAMPTZ,

    -- Set when crystallization succeeds; the environment row is flipped to
    -- runtime='vm' and pointed at the same app server.
    app_server_id  UUID        REFERENCES app_servers(id) ON DELETE SET NULL,

    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);

-- A deleted box's name is reusable: the uniqueness only binds live rows (same
-- shape as idx_app_deploy_hooks_active). Unique rather than a plain index so the
-- database, not a racing pair of API replicas, is what refuses the duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS idx_boxes_project_name_live
    ON boxes (project_id, name)
    WHERE status <> 'Deleted';

-- The collector groups dada_boxes by status every refresh interval, and the
-- reaper scans live boxes by clock; both want this.
CREATE INDEX IF NOT EXISTS idx_boxes_status ON boxes (status);
CREATE INDEX IF NOT EXISTS idx_boxes_project ON boxes (project_id);

-- The box-agent webhook resolves tenancy by instance_ref (it is never trusted to
-- report project_id/org_id), so that lookup is on the hot path of every sample.
CREATE INDEX IF NOT EXISTS idx_boxes_instance_ref
    ON boxes (instance_ref)
    WHERE instance_ref <> '';
