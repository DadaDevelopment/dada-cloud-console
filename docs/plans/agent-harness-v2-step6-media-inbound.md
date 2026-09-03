# Plan: Agent Harness v2, Step 6 — Media Inbound (voice/image attachments)

## Owner's ask

Image + voice inbound: real users constantly send both. Attachments are a
platform abstraction, not raw Telegram file_ids passed to the model. Form
has social meaning: a voice message must stay visible as `[voice, 34s]` in
the agent context, not silently become text.

## Scope for this step (honest about what can be real)

- Telegram parsing: voice/photo/document/video_note message types -> update
  carries an Attachment descriptor (file_id, mime, duration/size when
  present).
- File download: getFile + https download of the bytes (Bot API), cached to
  disk under a configurable dir. This part CAN be fully real.
- STT/vision: interface + stub resolver that returns "transcription
  unavailable" -- real backends deferred pending credentials (owner's
  constraint from intent.md risk #4). The pipeline shape is final; the
  model call inside it is a stub.
- Persistence: attachments JSONB column (reserved in Step 1) goes live.
- A2A rendering: `[voice 34s]: "<transcript or unavailable notice>"`,
  `[image]: <vision description or notice>`, `[document name.ext]`.

## Changes

### 1. tggateway/telegram.go

Extend tgUpdate parsing: voice (duration), photo (array of sizes -> pick
largest below some sane cap), document (file_name, mime), video_note
(duration). TelegramUpdate gains `Attachment *TelegramAttachment`.

### 2. tggateway/media.go

- MediaDownloader: getFile via Bot API -> file_path -> download from
  https://api.telegram.org/file/bot<token>/<path>, store under
  TG_GATEWAY_MEDIA_DIR (default /tmp/tg-media), filename = messageID_ext.
- StubTranscriber / StubDescriber: return typed "unavailable" results.

### 3. Contract: RuntimeInboundMessage gains Attachment

```go
type RuntimeAttachment struct {
    Kind        string  // voice|image|document|video_note
    FileID      string
    FilePath    string  // local cached path (may be empty on failure)
    MimeType    string
    DurationSec int     // voice/video_note
    FileName    string  // document
    SizeBytes   int64
    Transcript  string  // stub: "" + TranscriptAvailable=false
    TranscriptAvailable bool
    Description string
    DescriptionAvailable bool
}
```

InboundMessage mirrors it. Messages with an attachment carry empty Content
+ the attachment; gateway skips the "no text" filter for media messages.

### 4. agentruntime

- SaveMessageInput gains Attachments []any -> JSONB attachments column.
- A2A render: attachment lines in renderMessage:
  - voice: `user [voice 34s]: "..."` (transcript) or `user [voice 34s]: [transcription unavailable]`
  - image: `user [image]: <description>` / `user [image]: [description unavailable]`
  - document: `user [document name.pdf, 1234 bytes]`

### 5. runPollerDebounced

Media updates: download (bounded 20s), fill descriptor, pass through. Bot
API failures degrade to descriptor without FilePath -- message still flows.

## Verification

1. Unit: attachment parsing from update JSON (voice, photo sizes pick,
   document).
2. Unit: downloader against httptest fake Bot API + fake file server
   (bytes on disk, error path).
3. Render tests: voice/image/document lines, unavailable variants.
4. Poller test: voice update -> runtime receives Attachment with FilePath
   set, transcript unavailable.
5. E2E smoke: attachment persisted to attachments JSONB.
6. Full suites + vet on the rig.
