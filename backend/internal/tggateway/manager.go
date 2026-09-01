package tggateway

import (
	"context"
	"fmt"
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
	store Store
	tg    TelegramClient
	a2a   A2AClient

	mu      sync.Mutex
	pollers map[string]*runningPoller
}

// NewManager builds a Manager. Run must be called once to start the
// reconcile loop; Bind/Unbind/Get work standalone (each also nudges the
// poller set directly, so a caller does not have to wait for the next tick).
func NewManager(store Store, tg TelegramClient, a2a A2AClient) *Manager {
	return &Manager{store: store, tg: tg, a2a: a2a, pollers: map[string]*runningPoller{}}
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
	go runPoller(pctx, m.tg, m.a2a, b)
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
// (Send takes a plain text string, no separate metadata field).
func withTelegramIdentity(u TelegramUpdate) string {
	username := u.Username
	if username == "" {
		username = "unknown"
	}
	firstName := u.FirstName
	if firstName == "" {
		firstName = "unknown"
	}
	return fmt.Sprintf("[telegram_username: %s | first_name: %s | chat_id: %d]\n%s", username, firstName, u.ChatID, u.Text)
}

// runPoller is one binding's long-poll loop: getUpdates -> A2A -> reply.
// Exits when ctx is cancelled (Reconcile/Unbind stopping this binding).
func runPoller(ctx context.Context, tg TelegramClient, a2a A2AClient, b Binding) {
	var offset int64
	var failing bool
	var warned bool

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

		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			stopTyping := startTyping(ctx, tg, b.BotToken, u.ChatID)
			reply, err := a2a.Send(ctx, b.AgentName, withTelegramIdentity(u))
			stopTyping()
			if err != nil {
				if !failing {
					failing = true
					warned = false
				}
				log.Warn().Err(err).Str("agent", b.AgentName).Msg("tggateway: a2a send failed")
				if !warned {
					warned = true
					if sendErr := tg.SendMessage(ctx, b.BotToken, u.ChatID, a2aFailureFallback); sendErr != nil {
						log.Warn().Err(sendErr).Str("agent", b.AgentName).Msg("tggateway: warning send failed")
					}
				}
				continue
			}
			failing = false
			if sendErr := tg.SendMessage(ctx, b.BotToken, u.ChatID, reply); sendErr != nil {
				log.Warn().Err(sendErr).Str("agent", b.AgentName).Msg("tggateway: reply send failed")
			}
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
