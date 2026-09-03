package tggateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// reconcileInterval is how often Manager.Run diffs tg_bindings rows against
// live poller goroutines -- the self-heal-on-restart mechanism from the
// design doc.
const reconcileInterval = 5 * time.Second

// getUpdatesTimeoutSec is Telegram's own long-poll timeout, passed through on
// every getUpdates call.
const getUpdatesTimeoutSec = 30

// typingRefreshInterval must stay under Telegram's ~5s "typing" expiry so the
// indicator never blinks off while a poller is still waiting on the agent.
const typingRefreshInterval = 4 * time.Second

// a2aFailureFallback is sent to the chat on the first a2a.Send failure of a
// streak. getUpdates already advances the offset before Send runs, so a
// failed update is never retried -- there is nothing to wait for, and a
// prior 30s silent-then-warn grace period just meant the user's message
// vanished with no reply at all when the streak self-healed inside that
// window (reproduced live 2026-08-30: ~10% of turns against a healthy agent
// hit this path). warned still gates to one message per continuous streak,
// so a real outage still doesn't spam every failed message.
const a2aFailureFallback = "не получилось обработать сообщение, попробуйте отправить его ещё раз"

// ErrInvalidToken is returned by Manager.Bind when Telegram's getMe rejects
// the token -- the handler maps this to a synchronous 400.
type ErrInvalidToken struct{ cause error }

func (e ErrInvalidToken) Error() string { return fmt.Sprintf("invalid bot token: %v", e.cause) }
func (e ErrInvalidToken) Unwrap() error { return e.cause }

// runningPoller is one live long-poll goroutine plus what stops it.
type runningPoller struct {
	cancel context.CancelFunc
	token  string
}

// Manager owns every live poller goroutine and reconciles them against the
// tg_bindings table. It is the only thing in this package that touches
// goroutine lifecycle, which is what manager_test.go exercises against fake
// Store/TelegramClient/A2AClient implementations.
type Manager struct {
	store    Store
	tg       TelegramClient
	a2a      A2AClient
	runtime  RuntimeClient
	debounce *DebounceConfig

	mu      sync.Mutex
	pollers map[string]*runningPoller
}

// NewManager builds a Manager. Run must be called once to start the
// reconcile loop; Bind/Unbind/Get work standalone (each also nudges the
// poller set directly, so a caller does not have to wait for the next tick).
// Pass nil for runtime to use direct A2A (backward compatibility). A
// non-nil debounce turns on inbound batching (Agent Harness v2, Step 2):
// rapid-fire messages of one chat inside the quiet window become ONE agent
// turn with one reply; nil keeps the legacy immediate-dispatch behavior.
func NewManager(store Store, tg TelegramClient, a2a A2AClient, debounce *DebounceConfig) *Manager {
	return &Manager{store: store, tg: tg, a2a: a2a, runtime: NewNoopRuntimeClient(), debounce: debounce, pollers: map[string]*runningPoller{}}
}

// SetRuntimeClient configures Manager to route messages through agent-runtime
// instead of direct A2A. This enables conversation state, lifecycle hooks, and
// domain instructions. Call before Run() to activate the full platform.
func (m *Manager) SetRuntimeClient(runtime RuntimeClient) {
	m.runtime = runtime
}

// Run blocks running the reconcile loop until ctx is cancelled. Callers
// invoke it in its own goroutine from main.
func (m *Manager) Run(ctx context.Context) {
	m.Reconcile(ctx)
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Reconcile(ctx)
		}
	}
}

// Reconcile diffs tg_bindings rows against live pollers: starts one for
// every live row missing a poller, stops any poller whose row is gone or no
// longer live, and restarts one whose token changed underneath it. This is
// what makes a pod restart self-heal with no manual re-bind step.
func (m *Manager) Reconcile(ctx context.Context) error {
	rows, err := m.store.List(ctx)
	if err != nil {
		log.Error().Err(err).Msg("tggateway: reconcile list bindings failed")
		return err
	}

	want := make(map[string]Binding, len(rows))
	for _, b := range rows {
		if b.Live() {
			want[b.AgentName] = b
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, running := range m.pollers {
		b, ok := want[name]
		if !ok || b.BotToken != running.token {
			running.cancel()
			delete(m.pollers, name)
		}
	}
	for name, b := range want {
		if _, ok := m.pollers[name]; ok {
			continue
		}
		m.startLocked(b)
	}
	return nil
}

// startLocked assumes m.mu is held.
func (m *Manager) startLocked(b Binding) {
	pctx, cancel := context.WithCancel(context.Background())
	m.pollers[b.AgentName] = &runningPoller{cancel: cancel, token: b.BotToken}
	go runPollerDebounced(pctx, m.tg, m.a2a, m.runtime, b, m.debounce)
}

// Bind validates token via getMe, upserts the row, and starts (or restarts,
// on a token change) its poller immediately rather than waiting for the next
// reconcile tick.
func (m *Manager) Bind(ctx context.Context, agentName, projectID, token string) (Binding, error) {
	username, err := m.tg.GetMe(ctx, token)
	if err != nil {
		return Binding{}, ErrInvalidToken{cause: err}
	}

	b := Binding{
		AgentName:   agentName,
		ProjectID:   projectID,
		BotToken:    token,
		BotUsername: username,
		Status:      StatusActive,
	}
	if err := m.store.Upsert(ctx, b); err != nil {
		return Binding{}, err
	}

	m.mu.Lock()
	if running, ok := m.pollers[agentName]; ok {
		running.cancel()
		delete(m.pollers, agentName)
	}
	m.startLocked(b)
	m.mu.Unlock()

	return b, nil
}

// Unbind removes the row and stops the poller. Safe to call for an agent
// with no binding (both steps are no-ops).
func (m *Manager) Unbind(ctx context.Context, agentName string) error {
	if err := m.store.Delete(ctx, agentName); err != nil {
		return err
	}
	m.mu.Lock()
	if running, ok := m.pollers[agentName]; ok {
		running.cancel()
		delete(m.pollers, agentName)
	}
	m.mu.Unlock()
	return nil
}

// Get returns the current binding for an agent, or ErrNotFound.
func (m *Manager) Get(ctx context.Context, agentName string) (Binding, error) {
	return m.store.Get(ctx, agentName)
}

// liveCount reports how many pollers are currently running (test helper).
func (m *Manager) liveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pollers)
}

// withTelegramIdentity prepends a bracketed metadata line carrying the
// sender's Telegram identity ahead of their message text. The agent's system
// prompt (tg-exchange-support-prompt) instructs the model to fall back to
// "telegram_username" for CRM person names, but this package used to hand
// the agent nothing but raw message text -- no username, first_name, or
// chat_id ever reached it, so it had no real value to fall back to and
// invented literal placeholder strings ("telegram_user") instead. This line
// is the only identity channel the A2A message/send envelope has room for
// (Send takes a plain text string, no separate metadata field). geo_lat/
// geo_lon are appended only on a native "Send location" share (the prompt's
// contract already documents these two fields on this same line).
func withTelegramIdentity(u TelegramUpdate) string {
	username := u.Username
	if username == "" {
		username = "unknown"
	}
	firstName := u.FirstName
	if firstName == "" {
		firstName = "unknown"
	}
	meta := fmt.Sprintf("telegram_username: %s | first_name: %s | chat_id: %d", username, firstName, u.ChatID)
	if u.HasLocation {
		meta += fmt.Sprintf(" | geo_lat: %f | geo_lon: %f", u.Latitude, u.Longitude)
	}
	return fmt.Sprintf("[%s]\n%s", meta, u.Text)
}

// a2aContextFor derives a stable A2A contextId for a Telegram chat. The A2A
// context IS the server-side conversation (kagent keeps session history per
// contextId), so keying it on the chat id gives the model the full dialogue:
// no repeated self-introductions, follow-ups that remember earlier turns.
func a2aContextFor(chatID int64) string {
	return fmt.Sprintf("tg-chat-%d", chatID)
}

// modelErrorMarkers are substrings that identify an upstream LLM/billing
// failure leaked into the reply text (observed live: kagent surfaces the raw
// anymodel.org HTTP 402 body, JSON with request ids, as the artifact text).
// Such text must never reach the Telegram user.
var modelErrorMarkers = []string{
	"Error code: 402",
	"billing_error",
	"payment_required",
	"Insufficient balance",
	"余额不足",
	"Run /compact",
}

// sanitizeModelReply masks upstream model/billing errors: if the reply looks
// like a leaked API error, the user gets the generic handoff line instead.
func sanitizeModelReply(reply string) string {
	for _, marker := range modelErrorMarkers {
		if strings.Contains(reply, marker) {
			return a2aFailureFallback
		}
	}
	return reply
}

// locationButtonMarker is a literal token the agent's system prompt is
// instructed to append on its own line when it wants the user offered the
// native "Send location" button. The gateway strips it before delivery and
// sends via SendMessageWithLocationButton instead of plain SendMessage --
// this is the only way the agent (which can only return text over A2A) can
// ask the transport layer for a keyboard.
const locationButtonMarker = "[[REQUEST_LOCATION_BUTTON]]"

// splitLocationButtonMarker reports whether reply asked for the location
// button and returns the reply text with the marker removed.
func splitLocationButtonMarker(reply string) (text string, wantsButton bool) {
	trimmed := strings.TrimRight(reply, "\n")
	if strings.HasSuffix(trimmed, locationButtonMarker) {
		return strings.TrimRight(strings.TrimSuffix(trimmed, locationButtonMarker), "\n 	"), true
	}
	return reply, false
}

// lastBatchChannelID returns the channel message id of the batch's LAST
// message, or "" -- the A2A-fallback path's reply anchor (the runtime path
// gets the same anchor from resp.ReplyToChannelMessageID).
func lastBatchChannelID(batch []TelegramUpdate) string {
	for i := len(batch) - 1; i >= 0; i-- {
		if batch[i].MessageID > 0 {
			return strconv.FormatInt(batch[i].MessageID, 10)
		}
	}
	return ""
}

// mediaCacheDir is where downloaded media files land. Overridable via env
// (TG_GATEWAY_MEDIA_DIR) for deployments with a real volume; the default is
// a temp dir because a cache miss just re-downloads.
func mediaCacheDir() string {
	if dir := os.Getenv("TG_GATEWAY_MEDIA_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "tg-media")
}

// runPoller is one binding's long-poll loop: getUpdates -> Runtime/A2A -> reply.
// Exits when ctx is cancelled (Reconcile/Unbind stopping this binding).
// Debouncing is disabled; see runPollerDebounced for the batching variant.
func runPoller(ctx context.Context, tg TelegramClient, a2a A2AClient, runtime RuntimeClient, b Binding) {
	runPollerDebounced(ctx, tg, a2a, runtime, b, nil)
}

// runPollerDebounced is the poller core. One getUpdates poll is one batch of
// work: every update in the poll goes to the agent as ONE runtime call with
// the whole batch in Messages (one reply back). When cfg is non-nil a
// Debouncer additionally merges ACROSS polls: rapid-fire messages landing in
// consecutive polls inside the quiet window join the same batch, capped by
// the max window so a continuous stream still gets served. Per-chat
// serialization is enforced by interruptState (Agent Harness v2, Step 3,
// cancel_and_restart): a new batch for a chat with a run in flight CANCELS
// that run, waits for it to release, and starts fresh -- never two
// concurrent agent calls for one chat / A2A contextId, and a stale run's
// reply never reaches the user. Different chats dispatch concurrently.
func runPollerDebounced(ctx context.Context, tg TelegramClient, a2a A2AClient, runtime RuntimeClient, b Binding, cfg *DebounceConfig) {
	var offset int64

	var stateMu sync.Mutex
	failing := false
	warned := false

	links := NewLinkTitleFetcher()
	media := NewMediaDownloader(tg, os.Getenv("TELEGRAM_API_BASE"), mediaCacheDir())
	trans, desc := newMediaAIResolvers(mediaAIConfigFromEnv())
	transcribeHook = trans
	describeHook = desc
	log.Info().Str("whisper", mediaAIConfigFromEnv().WhisperBaseURL).
		Str("vision_model", mediaAIConfigFromEnv().VisionModel).
		Msg("tggateway: media resolvers wired")
	runs := newInterruptState()
	defer runs.forgetAll()

	processBatch := func(batch []TelegramUpdate) {
		if len(batch) == 0 {
			return
		}
		chatID := batch[0].ChatID

		runCtx, done, superseded := runs.begin(chatID, ctx)
		defer done()
		if superseded {
			log.Debug().Str("agent", b.AgentName).Int64("chatID", chatID).
				Msg("tggateway: superseded an in-flight run (interrupt: cancel_and_restart)")
		}

		stopTyping := startTyping(runCtx, tg, b.BotToken, chatID)
		defer stopTyping()

		req := RuntimeMessageRequest{
			AgentName:  b.AgentName,
			Channel:    "telegram",
			ExternalID: fmt.Sprintf("%d", chatID),
			Actor: RuntimeActor{
				ExternalID: fmt.Sprintf("%d", batch[0].UserID),
				Username:   batch[0].Username,
				Metadata:   map[string]any{"first_name": batch[0].FirstName},
			},
		}
		for _, u := range batch {
			content := u.Text
			if u.HasLocation {
				content = fmt.Sprintf("[location_shared: lat=%f, lon=%f]\n%s", u.Latitude, u.Longitude, u.Text)
			}
			var attachment *RuntimeAttachment
			if u.Attachment != nil {
				resolveAttachment(runCtx, media, b.BotToken, u.Attachment, u.MessageID)
				attachment = &RuntimeAttachment{
					Kind:                 u.Attachment.Kind,
					FileID:               u.Attachment.FileID,
					FilePath:             u.Attachment.FilePath,
					MimeType:             u.Attachment.MimeType,
					FileName:             u.Attachment.FileName,
					DurationSec:          u.Attachment.DurationSec,
					SizeBytes:            u.Attachment.SizeBytes,
					Transcript:           u.Attachment.Transcript,
					TranscriptAvailable:  u.Attachment.TranscriptAvailable,
					Description:          u.Attachment.Description,
					DescriptionAvailable: u.Attachment.DescriptionAvailable,
				}
			}
			req.Messages = append(req.Messages, RuntimeInboundMessage{
				Content:                 content,
				ChannelMessageID:        strconv.FormatInt(u.MessageID, 10),
				ThreadID:                threadIDOrEmpty(u.ThreadID),
				SourceSentAt:            sentAtOrNil(u.SentAt),
				ReplyToChannelMessageID: replyIDOrEmpty(u.ReplyToMessageID),
				Links:                   enrichEntities(runCtx, links, u.Entities),
				Attachment:              attachment,
			})
		}

		var reply string
		var procErr error
		resp, err := runtime.ProcessMessage(runCtx, req)
		if err == nil {
			reply = resp.Text
		} else if runCtx.Err() != nil {
			return
		} else {
			log.Debug().Err(err).Msg("tggateway: runtime unavailable, falling back to direct A2A")
			var texts []string
			for _, u := range batch {
				r, sendErr := a2a.SendWithContext(runCtx, b.AgentName, a2aContextFor(chatID), withTelegramIdentity(u))
				if sendErr != nil {
					procErr = sendErr
					break
				}
				texts = append(texts, r)
			}
			if procErr == nil {
				reply = strings.Join(texts, "\n")
			}
		}

		if procErr != nil {
			if runCtx.Err() != nil {
				return
			}
			stateMu.Lock()
			if !failing {
				failing = true
				warned = false
			}
			log.Warn().Err(procErr).Str("agent", b.AgentName).Msg("tggateway: message processing failed")
			if !warned {
				warned = true
				if sendErr := tg.SendMessage(ctx, b.BotToken, chatID, a2aFailureFallback); sendErr != nil {
					log.Warn().Err(sendErr).Str("agent", b.AgentName).Msg("tggateway: warning send failed")
				}
			}
			stateMu.Unlock()
			return
		}
		stateMu.Lock()
		failing = false
		stateMu.Unlock()

		if !runs.claimReply(chatID, runCtx) {
			log.Debug().Str("agent", b.AgentName).Int64("chatID", chatID).
				Msg("tggateway: run superseded before reply, dropping computed reply")
			return
		}

		sendText, wantsButton := splitLocationButtonMarker(sanitizeModelReply(reply))
		var sendErr error
		switch {
		case wantsButton:
			sendErr = tg.SendMessageWithLocationButton(ctx, b.BotToken, chatID, sendText)
		default:
			anchor := resp.ReplyToChannelMessageID
			if anchor == "" {
				anchor = lastBatchChannelID(batch)
			}
			if replyTo, parseErr := strconv.ParseInt(anchor, 10, 64); parseErr == nil && replyTo > 0 {
				sendErr = tg.SendMessageReply(ctx, b.BotToken, chatID, replyTo, sendText)
			} else {
				sendErr = tg.SendMessage(ctx, b.BotToken, chatID, sendText)
			}
		}
		if sendErr != nil {
			log.Warn().Err(sendErr).Str("agent", b.AgentName).Msg("tggateway: reply send failed")
		}
	}

	var deb *Debouncer
	if cfg != nil {
		deb = NewDebouncer(*cfg, func(key string, batch []TelegramUpdate) {
			processBatch(batch)
		})
		defer deb.Close()
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := tg.GetUpdates(ctx, b.BotToken, offset, getUpdatesTimeoutSec)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn().Err(err).Str("agent", b.AgentName).Msg("tggateway: getUpdates failed")
			sleepOrDone(ctx, backoff)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = time.Second

		if len(updates) == 0 {
			continue
		}

		batch := make([]TelegramUpdate, 0, len(updates))
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			batch = append(batch, u)
		}

		if deb != nil {
			for _, u := range batch {
				deb.Enqueue(fmt.Sprintf("agent=%s chat=%d", b.AgentName, u.ChatID), u)
			}
		} else {
			go processBatch(batch)
		}
	}
}

// startTyping fires Telegram's "typing" chat action immediately and repeats
// it on typingRefreshInterval until the returned stop func is called, since a
// single call expires client-side well before a slow agent reply lands.
func startTyping(ctx context.Context, tg TelegramClient, token string, chatID int64) func() {
	tctx, cancel := context.WithCancel(ctx)
	send := func() {
		if err := tg.SendChatAction(tctx, token, chatID, "typing"); err != nil {
			log.Warn().Err(err).Int64("chatID", chatID).Msg("tggateway: sendChatAction failed")
		}
	}
	go func() {
		send()
		ticker := time.NewTicker(typingRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return cancel
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// threadIDOrEmpty/replyIDOrEmpty stringify a Telegram int64 id, treating 0
// (the field's absent-value in TelegramUpdate) as "no id" rather than the
// literal string "0" -- RuntimeMessageRequest's fields are empty-string
// optional, matching agentruntime.SaveMessageInput's convention.
func threadIDOrEmpty(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func replyIDOrEmpty(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// sentAtOrNil omits a zero time.Time rather than serializing Telegram's
// unix-epoch default -- SourceSentAt is meant to be absent, not 1970-01-01,
// when a Telegram update carried no message.date.
func sentAtOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
