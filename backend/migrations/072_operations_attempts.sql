-- Retry budget for the box operations worker (box_operations_worker.go).
--
-- Every other operations row is driven forward by gitops-agent's own
-- pipeline, which has its own retry bookkeeping outside this table. Box
-- operations are claimed and executed in one shot by the console backend, so
-- the attempt count has to be persisted on the row itself: without it, a
-- crash between claim and terminal write would forget how many times an
-- operation already failed, and a permanently-broken payload would retry
-- forever instead of going Failed.

ALTER TABLE operations ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0;
