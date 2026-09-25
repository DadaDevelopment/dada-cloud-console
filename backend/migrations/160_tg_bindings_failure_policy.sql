ALTER TABLE tg_bindings ADD COLUMN IF NOT EXISTS on_failure TEXT NOT NULL DEFAULT 'notice';
ALTER TABLE tg_bindings ADD COLUMN IF NOT EXISTS failure_notice TEXT NOT NULL DEFAULT '';
ALTER TABLE tg_bindings DROP CONSTRAINT IF EXISTS tg_bindings_on_failure_check;
ALTER TABLE tg_bindings ADD CONSTRAINT tg_bindings_on_failure_check CHECK (on_failure IN ('notice', 'silent'));
