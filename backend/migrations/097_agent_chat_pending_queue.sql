-- Several writes in one model round used to collapse into a single card: the
-- first one paused the turn and every other call in that round was answered
-- with a "skipped" stub, so the model was told its own actions had vanished.
-- queued_writes carries the rest of the round with the open card, and mode
-- carries the autonomy the user had selected when the card was created, so the
-- continuation does not silently fall back to a different one.
ALTER TABLE agent_chat_pending_actions
    ADD COLUMN IF NOT EXISTS queued_writes JSONB,
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'edit';
