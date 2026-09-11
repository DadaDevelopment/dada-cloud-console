// Package tggateway is the tg-gateway service (backend/cmd/tg-gateway): the
// Telegram <-> kagent agent bridge. It owns the tg_bindings table, exposes an
// internal (no-auth, ClusterIP-only) HTTP API the console backend proxies
// through, and runs one long-poll goroutine per bound Telegram bot token.
//
// Single replica is a hard requirement end to end: two goroutines polling
// getUpdates on the same bot token race each other and Telegram answers the
// second with 409 Conflict, exactly like the hand-wired telemost-bot before
// it (recreate: true). The reconcile loop is what makes a pod restart
// self-heal -- every row in tg_bindings gets its poller rebuilt on boot,
// with no manual re-bind step.
package tggateway

import "time"

// Status is the lifecycle state of one binding. Text rather than a bool so a
// future "paused" state needs no schema change.
type Status string

// StatusActive is the only status this package currently writes. A row in
// any other status is treated as not-live by Reconcile (its poller is
// stopped/never started), which gives an operator a manual escape hatch
// (UPDATE ... SET status = 'paused') without touching Go.
const StatusActive Status = "active"

// Transport is which Telegram API a binding speaks. TransportBot is the Bot
// API (BotToken is a BotFather token, the account is a bot, it cannot write
// first). TransportUser is a real account session over MTProto (BotToken is
// the session credential, the account looks like a person, it can open a
// dialog). The poller, the runtime and the CRM never see the difference: both
// transports implement TelegramClient, Manager picks the client per binding.
// Bot API is live today; the user-session client lands separately, until then
// a "user" row is stored but its poller is not started.
type Transport string

const (
	TransportBot  Transport = "bot"
	TransportUser Transport = "user"
)

// ParseTransport maps the wire value to a Transport; empty means bot.
func ParseTransport(raw string) (Transport, bool) {
	switch Transport(raw) {
	case "", TransportBot:
		return TransportBot, true
	case TransportUser:
		return TransportUser, true
	}
	return "", false
}

// Binding is one agent <-> Telegram account pairing, as tg_bindings holds it.
type Binding struct {
	AgentName   string
	ProjectID   string
	BotToken    string
	BotUsername string
	Transport   Transport
	Status      Status
	CreatedAt   time.Time
}

// Live reports whether this binding should have a running poller.
func (b Binding) Live() bool { return b.Status == StatusActive }

// transport defaults rows written before the column existed to bot.
func (b Binding) transport() Transport {
	if b.Transport == "" {
		return TransportBot
	}
	return b.Transport
}
