-- Sessions and per-user memory for the console assistant.
--
-- Until now the assistant's history was "the last 20 user/assistant rows in
-- this project/env scope". That is a sliding window, not a memory: a
-- conversation longer than 20 messages silently forgot its own beginning while
-- still calling itself one conversation, and a conversation resumed a week
-- later inherited 20 unrelated messages as though they were context.
--
-- A session is a run of messages in one scope with no idle gap longer than
-- AGENT_CHAT_SESSION_IDLE_MINUTES. Inside a session the assistant reads the
-- whole thing. Across sessions it reads one recursive summary per user:
-- new summary = f(old summary, the session that just ended). Sessions are
-- rows here rather than a value derived from timestamps at read time so the
-- folder can find exactly which conversations it has not folded yet, and so a
-- transcript row can be joined back to the conversation it belonged to.
--
-- ended_at is set when the user clears the context: that ends the conversation
-- immediately no matter what the idle gap says. folded_at is the guard against
-- folding the same session into the summary twice, which would compound the
-- same facts over and over.
CREATE TABLE IF NOT EXISTS agent_chat_sessions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_sub        TEXT        NOT NULL,
    project_id      UUID,
    env_id          UUID,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at        TIMESTAMPTZ,
    folded_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_sessions_scope
    ON agent_chat_sessions (user_sub, project_id, env_id, last_message_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_chat_sessions_unfolded
    ON agent_chat_sessions (last_message_at)
    WHERE folded_at IS NULL;

ALTER TABLE agent_chat_messages ADD COLUMN IF NOT EXISTS session_id UUID;

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_session
    ON agent_chat_messages (session_id, created_at);

-- A confirmation card pauses a turn and the user resumes it minutes later,
-- possibly after a page reload. The resumed half has to land in the session
-- that paused, so the session is pinned on the pending row rather than
-- re-derived at confirm time: the pending row's project/env is the scope the
-- TOOL will act on, which is legitimately not always the scope the
-- conversation started in, and re-deriving from it would split one exchange
-- across two conversations.
ALTER TABLE agent_chat_pending_actions ADD COLUMN IF NOT EXISTS session_id UUID;

-- One row per user. summary is the whole cross-session memory: it is rewritten
-- in full every fold rather than appended to, because an append-only memory
-- grows without bound and keeps facts that stopped being true.
--
-- folding_started_at is the claim marker. The fold is a recursive LLM call --
-- it reads the summary it is about to overwrite -- so two folds running at
-- once for one user compute from the same "before" and one of them silently
-- loses. The claim is therefore taken by a conditional UPDATE whose row count
-- is the answer, never by reading the column and then writing it. A claim
-- older than the abandonment window is taken over: a pod can die holding one.
CREATE TABLE IF NOT EXISTS agent_chat_user_memory (
    user_sub           TEXT        PRIMARY KEY,
    summary            TEXT        NOT NULL DEFAULT '',
    folded_sessions    INT         NOT NULL DEFAULT 0,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    folding_started_at TIMESTAMPTZ
);
