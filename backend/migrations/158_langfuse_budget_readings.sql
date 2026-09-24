-- The Langfuse budget guard's last usage reading, one row per project (keyed
-- by its public key). Langfuse answers only 100 v2/metrics requests a day and
-- a reading costs ten, so a restarted agent-runtime pod starts from this row
-- instead of reading again; without it every rollout after the day's requests
-- ran out left the pod holding back all traces (2026-09-20..24).
CREATE TABLE IF NOT EXISTS langfuse_budget_readings (
    project_key  TEXT PRIMARY KEY,
    day_units    BIGINT NOT NULL,
    month_units  BIGINT NOT NULL,
    read_at      TIMESTAMPTZ NOT NULL
);
