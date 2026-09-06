-- /finish forgets the user at the platform level: the identity tuple stops
-- being globally unique and becomes unique only among live conversations, so a
-- finished dialogue is archived instead of deleted and the next inbound message
-- opens a brand new conversation row (new id -> new agent context -> new state).
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_agent_name_channel_external_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_conversations_live_identity
    ON conversations (agent_name, channel, external_id)
    WHERE status = 'active';
