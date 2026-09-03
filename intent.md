# Intent: Agent Harness Platform v2 — Human-Grade Conversation Layer

Author: owner (chat 2026-09-02/03, after tg-agent-tools demo passed successfully).
Status: in-progress. Previous intent (registration funnel analytics) is DONE/parked —
see chat_history_lookup if needed; this file now tracks the harness platform work.

## Where we are

Phase 1-3 of the original harness plan shipped and is code-complete + build/test
verified (see docs/plans/agent-harness-conversation-runtime.md,
docs/STATUS-agent-runtime.md): conversations table, conversation_messages,
lifecycle_hooks, hook_executions, agent-runtime service, tg-gateway integration
with A2A fallback. Verified live: go build/vet clean, agentruntime store tests
pass against real Postgres (GetOrCreate idempotency, message history order,
metadata update, idle listing), agent-runtime binary boots and a lifecycle hook
(conversation.created -> metadata action) fires and is recorded in
hook_executions. A2A call to a real kagent Agent was NOT exercised (no cluster
DNS from the sandbox container) — that link is still unverified end-to-end.

Demo happened, went fine, production tg-agent-tools (kagent Agent name:
tg-exchange-support) untouched throughout. Owner's call now: platform work
continues for real, not demo-safe placeholder — pick up the full backlog from
their post-demo product review.

## The ask (owner's words, condensed from the review message)

Core architectural principle owner set: **kagent decides what to say/do.
Conversation platform decides how and when it reaches the channel.** Typing,
delays, batching, read receipts, stale-run cancellation, media resolving —
none of that is prompt or skill; it is harness/runtime.

Owner's own priority table (P0 first):

- P0 Canonical Message + Telegram IDs + source_sent_at — foundation for everything else
- P0 inbound debounce (quiet_window aggregation of rapid-fire messages into one turn)
- P0 interrupt/cancel a stale agent run when the user sends a new message mid-generation
- P0 proper reply-to / quotes (native Telegram reply, not just plain text)
- P0 image + voice inbound (attachments, not raw file_id passed to the model)
- P0 link resolver (URL entities -> Link metadata, cheap/deterministic; deep read is agent's call)
- P1 delayed typing policy (human-like, configurable start/duration)
- P1 delayed read policy (human-like; capability-gated — only real for Telegram Business connections, noop for plain bot API)
- P1 outbound voice/images/files (symmetric multimodality)
- P1 edit/delete/reactions (inbound events + agent-issued edit_own_message/delete_own_message/react)
- P1 idle scheduler + proactive agent invocation (already started, Phase 5 of original plan)
- P1 quiet hours / timezone (mandatory once proactive follow-ups exist)
- P2 segmented multi-message responses (agent-issued structured segments, not a regex splitter)
- P2 sticker/GIF/video-note semantics
- P2 human takeover ownership (conversation.owner: agent | human:<id>)

Owner explicitly rejected: doing everything as prompt engineering, and any
automatic "smart" heuristic that fakes intelligence the platform doesn't
actually have (e.g. auto-splitting a long reply with a regex instead of a real
segmented-output primitive).

Full design detail (Message shape, Attachment shape, per-policy YAML sketches,
6-module architecture diagram, Telegram API specifics for read/typing/reply/
media-group/link-preview) is in the owner's message in this chat — treat it as
the source spec, do not re-derive it from scratch.

## Constraints carried over (still binding)

- Sandbox-only live testing: agent-sandbox project, do not touch other
  projects, do not create new projects (CLAUDE.md hard rule).
- Trunk-based: commit/push straight to main, no feature branches.
- No comments in source code; docstrings/doc-comments on exported symbols only,
  matching existing repo convention (long doc comments above types/funcs).
- Own every failure surfacing during this work; verify with real builds/tests
  before claiming done (go-build docker container + dada-pg container are the
  working local verification rig — go 1.25-alpine, Postgres 16-alpine, bridge
  network, migrations applied via `cmd/migrate`).
- Production agent (tg-exchange-support) stays on direct A2A path (noop
  runtime client) until each new capability is verified against the sandbox
  agent-runtime deployment first.

## Open questions / risks flagged during design review

1. A2A protocol has no native multi-turn/history field in what this repo's
   client implements — history is currently injected as a text block. Canonical
   Message + reply-to needs a real structured channel to kagent or this stays
   a workaround. Needs a decision: extend the A2A envelope, or keep textual
   context injection and accept its limits.
2. Read receipts: Telegram Bot API cannot mark a message read as a human doc
   proper feature (only implicit via reply). `readBusinessMessage` and
   `messages.readHistory`-equivalent behavior exist only for Business
   connections. Platform must expose read_policy as a capability the binding
   declares support for, not assume it always works.
3. Debounce and interrupt both need per-conversation in-memory run state
   (active generation goroutine + cancel func, pending-message buffer) living
   somewhere with the same single-replica constraint tg-gateway already has
   for its Telegram long-poll (two pollers on one bot token race). Whichever
   service owns this (agent-runtime is the natural owner) inherits that
   constraint too.
4. Voice/vision need real STT/vision backends wired in — this environment has
   no confirmed credentials/endpoint for either yet. Build the Attachment
   pipeline and interface now; the actual model call is a stub until an
   endpoint is confirmed.
5. media_group_id (Telegram albums) aggregation and inbound debounce are two
   different aggregation concerns that must not fight each other (album parts
   arrive as separate updates too).

## Immediate execution order for this session

1. [x] DONE (commit 9b049d97, pushed). Canonical Message schema + Telegram
   identity plumbing. Build/vet clean, 6/6 store tests on real PG, 18/18
   tggateway tests, migration verified via \d.
2. [x] DONE (commit cd65273b, pushed). Inbound debounce. Debouncer
   (quiet 2.5s default / max 8s, per-chat keying, no-drop flush) +
   Messages[] batch contract through gateway->runtime->DB (each message
   its own row, one A2A call per batch) + temporal rendering in the A2A
   history block ("user [sent 22:41 UTC, 3m ago]: ..."). 5 new debounce
   tests + all pre-existing green; E2E smoke on the rig: 2-message batch
   -> 2 rows with own channel ids and source_sent_at. Debounce is OFF by
   default (env TG_GATEWAY_DEBOUNCE_QUIET_MS/_MAX_MS) -- production
   tg-exchange-support keeps the legacy immediate path until the harness
   is deployed alongside it.
3. [ ] NEXT. Interrupt/cancel stale run: active-run tracking per chat
   (run id + cancel func + generation counter), new message during an
   active run supersedes it per interrupt_policy; the per-chat mutex from
   Step 2 is the placeholder to replace.
4. [ ] Reply-to/quotes outbound (sendMessage with reply_parameters,
   agent output contract for which message it answers).
5. [ ] Link resolver (URL entities -> Link metadata, deterministic).
6. [ ] Media (voice/image) inbound: Attachment schema + stub resolver,
   real STT/vision pending credentials.

Each step: real code, real go build + go vet + go test against the
go-build/dada-pg rig, commit to main once green. No claiming "done" without a
build/test artifact to point at.
