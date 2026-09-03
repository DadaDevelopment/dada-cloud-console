# Plan: Agent Harness v2, Step 5 — Link Resolver

## Owner's ask

URL detection — cheap and deterministic (platform's job); reading content —
agent's decision. When a user message carries a URL, the platform should
extract it into message entities and show the agent a Link metadata block
(title/description if cheaply fetchable), not force the model to guess from
a bare URL or spend a tool call on detection.

## Scope cut for this step

- Inbound only: parse Telegram `entities` (url / text_link) from the
  message, persist into conversation_messages.entities (column exists since
  Step 1), render into the A2A context block.
- Metadata fetch (title/description): bounded 3s timeout, best-effort, from
  the gateway side where egress exists. Failure = store URL alone; NEVER
  block or fail the message on link metadata.
- resolve_content(uri) universal resolver (youtube/github/pdf) — NOT here.
  That is a later capability; this step's entities + rendering already give
  the agent the URL and its cheap metadata.
- Outbound link previews (LinkPreviewOptions) — not here.

## Changes

### 1. tggateway/telegram.go

Parse `message.entities` / `caption_entities` into TelegramUpdate:
`Entities []TelegramEntity{Type, Offset, Length, URL}`. Only url and
text_link types are kept (mention/hashtag etc are noise for now). Extraction
of the actual URL text from Text via offset/length (UTF-16 byte offsets per
Telegram — bytes here because Go strings index by byte and Telegram's
offset/length are UTF-16 code units; for ASCII URLs they coincide, for
non-ASCII text a rune-aware converter is needed — implement utf16OffsetToByte
properly).

### 2. runtime contract

`RuntimeInboundMessage.Entities []LinkEntity{Type, URL}` — already-extracted
URLs, so the runtime doesn't re-parse text. gateway extracts from Telegram
entities; server layer passes through.

### 3. agentruntime

- Persist entities JSON into conversation_messages.entities.
- Render in A2A context: after a user message line containing URLs, append
  `[link] url` lines (one per unique URL). No fetching in the runtime — the
  gateway already tried; the runtime renders what it was given. Keep the
  runtime fetch-free (egress discipline: only the gateway fetches).

### 4. Link metadata fetch (tggateway)

Best-effort HEAD/GET with 3s cap, parse <title>. Result attached to the
entity as Title before sending to runtime. Failure → Title empty. The A2A
context line becomes `[link] https://... (Title)`.

## Verification

1. Unit: utf16 offset→byte conversion (ASCII, CJK, emoji prefix cases).
2. Unit: entity extraction from a Telegram update (url + text_link).
3. Unit: title fetch parses <title>, timeout/failure yields empty title.
4. Poller test: message with URL → runtime receives Entities with the URL;
   title fetch against a fake HTTP server.
5. A2A render test: user message with entity renders `[link]` line.
6. Full suites + vet on the rig.
