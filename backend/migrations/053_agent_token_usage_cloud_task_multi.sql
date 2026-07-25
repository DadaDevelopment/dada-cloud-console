-- 053_agent_token_usage_cloud_task_multi.sql
-- Allow multiple agent_token_usage rows per cloud_task_id.
--
-- The cloud-task (claude -p) metering path writes one row per (invocation, model):
-- a single cloud_task fans out into several claude -p invocations, and one
-- invocation can span several models (modelUsage map), so cloud_task_id can no
-- longer be unique. platform_request_id stays the UNIQUE idempotency anchor for
-- both agent systems (ADR-015); the cloud-task path mints it deterministically as
-- ct-<cloud_task_id>-<seq>-<model>. Forward-only, additive; no data affected
-- (no cloud_task rows are written yet).

DROP INDEX IF EXISTS idx_agent_token_usage_cloud_task;

CREATE INDEX IF NOT EXISTS idx_agent_token_usage_cloud_task
    ON agent_token_usage (cloud_task_id) WHERE cloud_task_id IS NOT NULL;
