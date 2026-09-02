-- Canonical Message foundation (Agent Harness v2, Step 1): widen
-- conversation_messages with channel-native identity so every later
-- capability (inbound debounce, interrupt/cancel, reply-to/quotes, media
-- attachments, edit/delete tracking) can build on the same row instead of a
-- second parallel message table. See
-- docs/plans/agent-harness-v2-step1-canonical-message.md
--
-- channel_message_id is the provider's own message id (Telegram message_id
-- as text, future-proof for channels whose ids are not integers). It is the
-- join key for resolving a Telegram reply_to_message back to our row.
--
-- reply_to_message_id is a SELF-FK to our own UUID, not the provider's id --
-- resolving provider id -> our UUID happens in Go via the
-- idx_conversation_messages_channel_message_id index before insert.
-- ON DELETE SET NULL: if the quoted message is later purged, the reply
-- itself must not disappear or become an orphan-reference error.
--
-- entities and attachments are reserved now (empty JSON array default) so
-- the link-resolver and media-inbound steps land without another migration;
-- neither is populated by this change.
ALTER TABLE conversation_messages
    ADD COLUMN IF NOT EXISTS channel_message_id TEXT,
    ADD COLUMN IF NOT EXISTS thread_id TEXT,
    ADD COLUMN IF NOT EXISTS source_sent_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reply_to_message_id UUID REFERENCES conversation_messages(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS entities JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS attachments JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS channel_metadata JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_conversation_messages_channel_message_id
    ON conversation_messages(conversation_id, channel_message_id)
    WHERE channel_message_id IS NOT NULL;
