# Plan: Agent Harness v2, Step 1 — Canonical Message (foundation)

## Scope

Only Step 1 of intent.md's execution order: extend `conversation_messages`
with the fields every later capability (debounce, interrupt, reply-to, media)
needs, and wire Telegram's real message identity end-to-end. No debounce, no
interrupt, no media resolution in this step — those build on top of this.

## Why this is the right first cut

Every later P0 item reads at least one of: `channel_message_id` (reply-to,
interrupt bookkeeping), `source_sent_at` (temporal awareness, humanized
delay policy), `reply_to_message_id` (quotes), or `thread_id` (forum topics).
Doing this once now avoids a second migration touching every later feature.

## Changes

### 1. Migration `150_canonical_message.sql`

Add to `conversation_messages` (ALTER, not a new table — this is the same
message log, richer):

```sql
ALTER TABLE conversation_messages
    ADD COLUMN channel_message_id TEXT,
    ADD COLUMN thread_id TEXT,
    ADD COLUMN source_sent_at TIMESTAMPTZ,
    ADD COLUMN reply_to_message_id UUID REFERENCES conversation_messages(id) ON DELETE SET NULL,
    ADD COLUMN entities JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN attachments JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN edited_at TIMESTAMPTZ,
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN channel_metadata JSONB NOT NULL DEFAULT '{}';

CREATE INDEX idx_conversation_messages_channel_message_id
    ON conversation_messages(conversation_id, channel_message_id)
    WHERE channel_message_id IS NOT NULL;
```

`reply_to_message_id` is a self-FK to our own UUID, not Telegram's numeric id
— resolving Telegram's `reply_to_message.message_id` to our UUID happens in
Go via the `channel_message_id` index before insert. `entities` and
`attachments` are reserved now (empty array default) so the link-resolver and
media steps don't need another migration; they are not populated yet.

### 2. `internal/tggateway/telegram.go`

Extend `TelegramUpdate` with:
- `MessageID int64` — Telegram's own message_id (becomes `channel_message_id`)
- `SentAt time.Time` — parsed from `message.date` (unix seconds)
- `ReplyToMessageID int64` — 0 if not a reply
- `ThreadID int64` — 0 if not in a forum topic (`message_thread_id`)

Parse these in `GetUpdates` from the existing `tgUpdate` struct (add the
corresponding json fields: `message_id`, `date`, `reply_to_message.message_id`,
`message_thread_id`).

### 3. `internal/agentruntime/store.go`

`Message` struct gains: `ChannelMessageID`, `ThreadID`, `SourceSentAt`,
`ReplyToMessageID *uuid.UUID`, `Entities`, `Attachments`, `EditedAt`,
`DeletedAt`, `ChannelMetadata`.

`SaveMessage` signature grows too much for positional args — replace with a
`SaveMessageInput` struct (role, content, metadata, plus the new optional
fields) and one `FindMessageByChannelID(ctx, conversationID, channelMessageID)
(Message, error)` for reply resolution. Existing callers (runtime.go) updated
to the new signature; this is a small, contained blast radius (one caller).

### 4. `internal/agentruntime/runtime.go` + `server.go`

`MessageRequest` gains `ChannelMessageID`, `ThreadID`, `SourceSentAt`,
`ReplyToChannelMessageID` (the *Telegram* id from the incoming update; runtime
resolves it to our internal UUID via `FindMessageByChannelID` before saving).
`ProcessMessage` passes these into `SaveMessageInput` for the user message.

### 5. `internal/tggateway/manager.go` + `runtime_client.go`

`RuntimeMessageRequest` gains the same fields; `runPoller` fills them from the
extended `TelegramUpdate`.

## Explicitly NOT in this step

- No debounce, no interrupt/cancel, no reply-to on the *outbound* side
  (`sendMessage` with `reply_parameters`) — that's consuming this data, comes
  with Step 4.
- No entity/attachment population — columns exist, stay empty arrays for now.
- No change to A2A history-injection format — still a text block; revisited
  only if Step 4 (reply-to) proves the text block insufficient.

## Verification (must pass before commit)

1. `go build ./...` in the go-build container — must be clean.
2. `go vet ./...` — must be clean.
3. `go test ./internal/agentruntime/... ./internal/tggateway/...` against
   dada-pg (`TEST_DATABASE_URL`) — existing 4 store tests plus new ones for
   `FindMessageByChannelID` and reply resolution.
4. Manual smoke: apply migration 150 via `cmd/migrate` against dada-pg,
   confirm columns exist via `\d conversation_messages`, insert a message with
   `channel_message_id` set, insert a second one with `reply_to_message_id`
   pointing at the first's UUID, confirm the FK holds and a delete of the
   first nulls the second's reference (ON DELETE SET NULL).

## Risk

Widening `Message`/`SaveMessage` touches the one real caller (`runtime.go`)
and the two tests already green — acceptable blast radius. The self-FK on
`conversation_messages(id)` is safe (nullable, ON DELETE SET NULL, no cycle
risk since it always points to an earlier row).
