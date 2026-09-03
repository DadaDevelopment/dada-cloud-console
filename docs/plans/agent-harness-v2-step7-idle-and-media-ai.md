# Plan: Agent Harness v2, Step 7 — Idle Scheduler + Real Media Resolvers

Two work items, one plan. Both were owner priorities (P1 idle/proactive from
the review; "реальная работа со всеми видами медиа" from today's chat).

## Part A: Idle Scheduler + Proactive Agent Invocation

Owner's dogfood case: `conversation.idle(30m)` -> follow-up. The scheduler
and the idle DETECTION are deterministic platform work; the CONTENT of the
follow-up is the agent's job -- invoked with a real invocation REASON, not a
fake user message.

### Where it lives

agent-runtime (owns conversations table; single-replica by deployment). A
goroutine scans idle conversations and runs an outbound agent invocation.

### Core concept: invocation cause

An idle follow-up is NOT a fake user turn. The A2A message is prefixed with
an explicit invocation envelope:

```
[invocation: cause=conversation_idle, idle=31m]
Составь follow-up сообщение для клиента... (harness instruction)
```

The user message we persist is `role='system'` (new allowed value alongside
user/assistant), so the history shows WHY the agent spoke, not a phantom
user line. The agent's reply goes back to the channel as usual.

### DB

No new tables. Two things ride existing ones:

1. `lifecycle_hooks` rows with trigger_event='conversation.idle' already
   exist (Step 1 migration). Their action_config carries:
   `{"agent_message": "..."} ` -- the harness instruction for the follow-up.
2. Per-conversation idle state: `conversations.metadata.idle_fired_at`
   (timestamp string) -- marks that THIS idle hook already fired for this
   conversation, so a 30-min hook fires once per idle period, not every
   scan. A new user message clears it (runtime.ProcessMessage deletes the
   key on any inbound user message).

### Flow (RunIdleScheduler, 1-minute tick)

1. Query: active conversations joined with idle hooks of their agent where
   `updated_at < now() - idle_minutes` and metadata has no idle_fired_at
   newer than the hook's threshold.
2. For each: mark idle_fired_at FIRST (single UPDATE ... WHERE metadata
   doesn't have a newer mark -- claim semantics, safe under concurrent
   ticks), then invoke.
3. Invocation: save system message, call A2A with the invocation envelope,
   save assistant reply, deliver to channel.

### Delivery back to the channel

agent-runtime doesn't know how to send to Telegram -- tg-gateway does. New
internal HTTP endpoint on tg-gateway: POST /outbound {agent_name, chat_id,
text, reply_to} -> SendMessageReply/SendMessage. agent-runtime calls it
(fire-and-forget with log on failure; the reply is already persisted, a
delivery failure is not a data loss).

Env: TG_GATEWAY_OUTBOUND_URL on agent-runtime (e.g.
http://dada-cloud-console-tg-gateway.argocd-prod:8082). Empty = scheduler
still runs and persists, just cannot deliver (logs say so).

### API surface (agent-runtime)

- POST /hooks {agent_name, name, trigger_event, trigger_config,
  action_type, action_config} -- create a hook without psql (dogfood UX).
- GET /hooks?agent_name=... -- list.
- DELETE /hooks/{id} -- remove.
- Scheduler enabled by env AGENT_RUNTIME_IDLE_TICK_SECONDS (default 60;
  0 disables).

## Part B: Real media resolvers (STT + vision)

Replace stubTranscribe/stubDescriber with real calls against the
OpenAI-compatible AI gateway (llmchat.Client's world), same as agent chat
uses -- but from tg-gateway, so the gateway needs its own configured client:

Env on tg-gateway:
- TG_MEDIA_GATEWAY_URL (LiteLLM base, e.g. http://ai-gateway-service...)
- TG_MEDIA_GATEWAY_KEY (static; the ServiceIdentity-refresh machinery lives
  console-side and is out of scope here -- static key first, rotation later)
- TG_MEDIA_STT_MODEL (e.g. "or-whisper" -- group name in the gateway)
- TG_MEDIA_VISION_MODEL (e.g. "or-gpt-4o-mini")

### STT (voice)

OpenAI-compatible POST /v1/audio/transcriptions (multipart: file, model).
Bounded: 120s cap. Success -> Transcript + TranscriptAvailable=true.
Failure -> stays unavailable (log one line).

### Vision (image)

/v1/chat/completions with image_url data URI (base64 of the cached file),
short instruction: "Опиши изображение по-русски в 1-2 предложениях".
Bounded: 60s cap. Success -> Description + DescriptionAvailable=true.

### Feature detection

Both resolvers are enabled only when their env vars are set; missing env =
current stub behavior (flags false). No config = no behavior change.

### Where

New file tggateway/media_ai.go: aiTranscriber/aiDescriber implementing the
same call signatures as the stubs, wired in runPollerDebounced behind env
detection. media.go's stubs stay as the zero-config fallback.

## Verification

### Part A
1. Unit: idle-claim SQL semantics (two ticks don't double-fire; new message
   clears idle_fired_at).
2. Unit: invocation envelope format; system-role persistence.
3. Integration: fake A2A + fake outbound endpoint; idle conversation ->
   one invocation, one delivery, idle_fired_at set; second tick no-ops.
4. Hooks API test via httptest on agentruntime.Server.

### Part B
1. Unit: transcription against a fake /v1/audio/transcriptions (multipart
   parsed, model field checked); failure -> unavailable.
2. Unit: vision against fake chat/completions accepting image_url data URI.
3. End-to-end poller test with env pointed at fakes: voice update ->
   transcript in the A2A context block.

### Both
Full build/vet/both-suites on the rig + E2E smoke on real Postgres.

## Explicitly out of scope

- KeyFunc-style rotation for the media key (static env key).
- TTS/sendVoice outbound (owner's symmetric multimodality -- separate step).
- Quiet hours (separate P1, needs timezone data on conversations; noted as
  mandatory before enabling proactive in PROD).
