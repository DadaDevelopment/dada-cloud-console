-- 065_box_sessions_attachments.sql
-- Dada Box, slice 2 + slice 6: the box credential and the resources attached to
-- a box while it runs.
--
-- NUMBERING. 060-062 are taken (box leads, boxes, box grants). 063 and 064 are
-- reserved for the sibling metering branch (`claude/box-metering`, which owns
-- 063_box_usage and may own 064), so this slice starts at 065 -- the same
-- discipline that moved boxes off 058 in phase 2. Renumbering later is not free:
-- a migration that has run in prod cannot be renamed.
--
-- FOUR TABLES, ONE FILE, on purpose: box_sessions is what authenticates a caller
-- to the other three, so a deployment that had attachments without sessions would
-- have rows nothing could reach.

-- (1) box_sessions -- the box credential.
--
-- A LINE-FOR-LINE COPY OF THE app_deploy_hooks SHAPE (migration 039): only the
-- sha256 hash and a short plaintext prefix are stored, the plaintext is shown
-- exactly once at creation, and withdrawal is revoked_at rather than DELETE.
--
-- Why each of those three, since a shortcut in any of them is tempting:
--   * hash only: the operations table is long-lived, replicated and readable by
--     every agent that polls it, so a live secret anywhere near it is a secret at
--     rest in the widest blast radius the platform has. Compare
--     CreateAppServerPayload.SSHPrivateKey, which has to be SCRUBBED after the
--     operation goes terminal precisely because it does carry one. Box sessions
--     were designed the other way round, so there is no scrub step to forget.
--   * token_prefix: a caller with four boxes has to be able to tell their tokens
--     apart in a list without the list revealing any of them.
--   * revoked_at, not DELETE: a revoked credential that leaves no row cannot be
--     audited, and "when was this revoked" is the first question after an
--     incident.
CREATE TABLE IF NOT EXISTS box_sessions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    box_id       UUID        NOT NULL REFERENCES boxes(id) ON DELETE CASCADE,
    project_id   UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,

    -- sha256 hex of the plaintext. UNIQUE so a lookup by hash is the whole
    -- authentication and two sessions can never collide on one credential.
    token_hash   TEXT        NOT NULL UNIQUE,
    -- "dadabox_" + 6 hex. Enough to identify, useless to authenticate with.
    token_prefix TEXT        NOT NULL,

    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Expiry is a column and not a policy: a box lives hours, so a credential
    -- that outlives every box it could ever open is a standing liability.
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- The authentication path is a lookup by hash filtered to live sessions, and the
-- revoke path is "every live session of this box". Both get an index; the partial
-- one mirrors idx_app_deploy_hooks_active.
CREATE INDEX IF NOT EXISTS idx_box_sessions_live
    ON box_sessions (box_id)
    WHERE revoked_at IS NULL;

-- (2) box_attachments -- managed resources attached to a running box.
--
-- The resource lives OUTSIDE the box. That is architecture, not deployment: a
-- disposable body must not own the customer's database, or deleting the body
-- deletes the data and crystallization has nothing left to carry.
--
-- NO SECRET IN THIS TABLE. injected_keys records WHICH env keys were written into
-- the box's 0600 env file; the values only ever exist in that file. A row that
-- also carried the DSN would put the credential back at rest in the control
-- plane, undoing the reason the injection path exists.
CREATE TABLE IF NOT EXISTS box_attachments (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    box_id         UUID        NOT NULL REFERENCES boxes(id) ON DELETE CASCADE,
    -- The identity carrier (D1). Attachments hang off the box's environment so
    -- crystallization promotes the same row and nothing has to be migrated.
    environment_id UUID        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,

    kind           TEXT        NOT NULL CHECK (kind IN ('postgres', 's3')),
    name           TEXT        NOT NULL,
    -- The resource as the provider named it (database name, bucket name).
    resource_name  TEXT        NOT NULL DEFAULT '',
    env_prefix     TEXT        NOT NULL DEFAULT '',
    injected_keys  TEXT[]      NOT NULL DEFAULT '{}',

    status         TEXT        NOT NULL DEFAULT 'Attached'
                   CHECK (status IN ('Attaching', 'Attached', 'Failed', 'Detached')),
    error_message  TEXT        NOT NULL DEFAULT '',

    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    detached_at    TIMESTAMPTZ
);

-- One live attachment per (box, name): re-attaching the same name must rotate the
-- existing one rather than silently produce a second credential the box's env can
-- only hold one of. Detached rows are kept, so the name is reusable.
CREATE UNIQUE INDEX IF NOT EXISTS idx_box_attachments_live_name
    ON box_attachments (box_id, name)
    WHERE detached_at IS NULL;

-- (3) box_exposures -- published ports.
--
-- hostname is assigned by the PLATFORM under its wildcard and is never chosen by
-- the caller: custom domains are a crystallization feature, which also removes
-- most of the phishing incentive on a throwaway body.
CREATE TABLE IF NOT EXISTS box_exposures (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    box_id       UUID        NOT NULL REFERENCES boxes(id) ON DELETE CASCADE,
    port         INTEGER     NOT NULL CHECK (port > 0 AND port < 65536),
    hostname     TEXT        NOT NULL,
    url          TEXT        NOT NULL DEFAULT '',
    -- wildcard|acme|none. Recorded because the per-host ACME path is measured
    -- specifically to prove why the wildcard is the one we ship: 50 certificates
    -- per domain per week means 50 boxes a week would end the product.
    cert         TEXT        NOT NULL DEFAULT 'wildcard',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    withdrawn_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_box_exposures_live_port
    ON box_exposures (box_id, port)
    WHERE withdrawn_at IS NULL;

-- (4) box_crystallizations -- one promotion attempt and its verification report.
--
-- The report is stored because ADR-019 makes verification the deliverable, not a
-- log line: a manifest comparison nobody can re-read after the fact is a check
-- that was performed and then thrown away. It is JSONB rather than columns because
-- its shape is the verifier's business and will grow (socket sets, per-volume
-- restores, the exclusion lists it printed).
--
-- verified is a real column and not derived from status: a crystallization can
-- reach a terminal state with verification having FAILED, and those two facts must
-- be separately queryable or a dashboard cannot tell "finished" from "correct".
CREATE TABLE IF NOT EXISTS box_crystallizations (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    box_id         UUID        NOT NULL REFERENCES boxes(id) ON DELETE CASCADE,
    environment_id UUID        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,

    vm_name        TEXT        NOT NULL,
    domain         TEXT        NOT NULL DEFAULT '',
    os_slug        TEXT        NOT NULL DEFAULT '',

    status         TEXT        NOT NULL DEFAULT 'Running'
                   CHECK (status IN ('Running', 'Verified', 'Failed', 'RollingBack', 'RolledBack')),
    stage          TEXT        NOT NULL DEFAULT 'none',
    verified       BOOLEAN     NOT NULL DEFAULT false,
    error_message  TEXT        NOT NULL DEFAULT '',

    report         JSONB,
    carry          JSONB,

    duration_ms    BIGINT      NOT NULL DEFAULT 0,
    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ
);

-- One crystallization in flight per box. Partial rather than plain: two concurrent
-- promotions of one box would race on the same VM root and the same ports, and the
-- DATABASE is what must refuse that, not a pair of racing API replicas.
CREATE UNIQUE INDEX IF NOT EXISTS idx_box_crystallizations_inflight
    ON box_crystallizations (box_id)
    WHERE status IN ('Running', 'RollingBack');

CREATE INDEX IF NOT EXISTS idx_box_crystallizations_box
    ON box_crystallizations (box_id, created_at DESC);
