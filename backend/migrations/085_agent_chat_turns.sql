-- One row per console-agent turn. Until now the Usage value returned by
-- agentchat.RunTurn/ResumeTurn was discarded into '_', so everything a turn
-- actually cost or did was gone the moment the SSE stream closed: no latency,
-- no tokens, no tool arguments, no outcome. agent_chat_messages only ever kept
-- role/content/tool_name, which is a transcript, not telemetry -- there was
-- literally nothing for the eval harness in
-- docs/product/agent-eval-personas-and-cases.md to read.
--
-- trace_id doubles as the Langfuse trace id, so a row here and a trace there
-- are the same object under the same key. The same trace_id is stamped on
-- agent_chat_messages, which is how the user/tool/assistant rows of one turn
-- get glued back to the turn that produced them.
--
-- inventory_apps / inventory_projects hold what the engine established about
-- the user before the first LLM call. They exist to separate the two production
-- failure modes that look identical in a transcript: "the user genuinely has
-- nothing deployed" (inventory_apps = 0) versus "the agent never looked"
-- (inventory_apps IS NULL). Both real threads that lost users were the second
-- shape wearing the first one's answer. preflight_calls counts the lookups the
-- engine made on its own initiative, which is what the eval reads to tell
-- grounding apart from the model happening to ask.
--
-- pending_tool_name / pending_args record that a turn ended by putting up a
-- confirmation card rather than by answering; the eval scores that as its own
-- outcome, not as a failure and not as an answer.
--
-- latency_ms, prompt/completion/total_tokens and gateway_calls are per turn,
-- not per gateway round-trip: one turn is a whole ReAct loop.
CREATE TABLE IF NOT EXISTS agent_chat_turns (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id                TEXT        NOT NULL,
    user_sub                TEXT,
    org_id                  TEXT,
    project_id              UUID,
    env_id                  UUID,
    kind                    TEXT        NOT NULL DEFAULT 'turn',
    input_message           TEXT,
    output_text             TEXT,
    tool_calls              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    tool_call_count         INT         NOT NULL DEFAULT 0,
    write_call_count        INT         NOT NULL DEFAULT 0,
    preflight_calls         INT         NOT NULL DEFAULT 0,
    gateway_calls           INT         NOT NULL DEFAULT 0,
    prompt_tokens           BIGINT      NOT NULL DEFAULT 0,
    completion_tokens       BIGINT      NOT NULL DEFAULT 0,
    total_tokens            BIGINT      NOT NULL DEFAULT 0,
    model                   TEXT,
    latency_ms              INT         NOT NULL DEFAULT 0,
    outcome                 TEXT,
    error_code              TEXT,
    pending_tool_name       TEXT,
    pending_args            JSONB,
    context_project_present BOOLEAN     NOT NULL DEFAULT false,
    context_app_present     BOOLEAN     NOT NULL DEFAULT false,
    inventory_apps          INT,
    inventory_projects      INT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_turns_user_created ON agent_chat_turns (user_sub, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_chat_turns_created ON agent_chat_turns (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_chat_turns_outcome ON agent_chat_turns (outcome);
CREATE INDEX IF NOT EXISTS idx_agent_chat_turns_trace ON agent_chat_turns (trace_id);

ALTER TABLE agent_chat_messages ADD COLUMN IF NOT EXISTS trace_id TEXT;

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_trace ON agent_chat_messages (trace_id);
