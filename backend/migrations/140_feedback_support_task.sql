-- 140_feedback_support_task.sql
-- Support tickets now dispatch onto the AgentSyncHub kanban instead of
-- launching an autofix run directly from an unauthenticated route. This
-- column links a ticket to the AgentSyncHub feature-request id
-- (support_task_id in the intake contract) that the ticket was filed as, so
-- the DadaAgent callback that names that task in the future can settle the
-- right row instead of guessing from cloud_task_id alone.

ALTER TABLE feedback ADD COLUMN IF NOT EXISTS support_task_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_feedback_support_task_id
  ON feedback (support_task_id)
  WHERE support_task_id <> '';
