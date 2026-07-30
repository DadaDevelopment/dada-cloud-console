-- Audit path instrumentation: passive steps and failures were invisible.
--
-- Before this migration audit_events recorded ONLY successful write-actions and
-- carried no environment, so a user path read as "TriggerBuild then silence"
-- with no way to tell whether they ever looked at the result, and PR-preview
-- actions were indistinguishable from production ones.
--
-- environment_id: preview envs stop masquerading as repeated prod actions.
-- outcome:        'success' | 'failure' — a rejected attempt is now a row.
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS environment_id UUID REFERENCES environments(id);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS outcome VARCHAR(16) NOT NULL DEFAULT 'success';

-- Path analysis walks per-actor chains ordered by time, and slices by action.
CREATE INDEX IF NOT EXISTS idx_audit_events_actor_created ON audit_events (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_action_created ON audit_events (action, created_at DESC);
