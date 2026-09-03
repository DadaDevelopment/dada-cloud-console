# Plan: Agent Harness v2, Step 4 — Reply-to / Quotes Outbound

## Owner's ask

Агент должен понимать, на какое из сообщений пользователя он отвечает, и
отвечать нативным Telegram reply (reply_parameters), а не голым текстом.
Особенно важно при debounced batch из трёх разных вопросов.

## Decision: who picks the reply target

Two candidate models:

- A) Agent says it: output contract `reply_to_channel_message_id` in the
  response envelope. Requires structured agent output (envelope parsing of
  A2A text artifacts) — fragile, no reference schema, and the model must
  echo Telegram ids it was given in the context block.
- B) Platform decides: the reply target is the LAST user message of the
  batch being processed (which is exactly the message being answered in the
  natural reading order; when the agent answers three questions in one
  reply, the reply visually anchors to the last one).

This step ships B: deterministic, zero model cooperation, zero new prompt
surface. The structured output contract (A) is revisited only if B proves
visually wrong in practice — per the standing rule: platform owns
presentation, model owns content.

## Changes

### 1. TelegramClient: SendMessageReply

New method `SendMessageReply(ctx, token, chatID, replyToMessageID int64,
text string) error` posting sendMessage with `reply_parameters:
{message_id: N, allow_sending_without_reply: true}` —
allow_sending_without_reply so a reply to a deleted message degrades to a
plain message instead of failing the send.

### 2. Runtime carries the reply anchor back

`MessageResponse` gains `ReplyToChannelMessageID string` — the channel id of
the LAST user message of the batch (or "" when none carried an id). The
server layer fills it; runtime just passes it through. tggateway's
RuntimeMessageResponse mirrors it.

### 3. processBatch outbound

When resp.ReplyToChannelMessageID parses as int64 → SendMessageReply with
that id; else → plain SendMessage. Location-button path keeps precedence
(marker wins; the location keyboard is a stronger UI signal than the reply
anchor). Fallback A2A path: reply target = last batch message's id (computed
locally), same precedence rule.

### 4. Inbound reply already done (Step 1)

reply_to_message → ReplyToChannelMessageID already flows into
conversation_messages; nothing new inbound-side.

## Verification

1. fakeTelegram records SendMessageReply calls; poller test asserts: batch
   of 3 messages → single SendMessageReply to message_id of the LAST
   message; single message without id → plain SendMessage.
2. HTTP action test on the agentruntime server: response JSON contains
   reply_to_channel_message_id of the last batch message.
3. Full suites + vet on the rig.
