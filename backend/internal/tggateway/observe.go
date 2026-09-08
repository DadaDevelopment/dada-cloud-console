package tggateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// observeTimeout caps one observation POST. The sink is a side channel: a
// slow or dead endpoint must never hold up the poll loop that feeds replies.
const observeTimeout = 5 * time.Second

// ObservedUpdate is what the gateway tells an agent about a message it saw.
//
// Engaged carries the same decision the poll loop acted on, so the agent's
// store keeps the messages it never answered too -- those are the majority,
// and they are the only record of how the room actually talks.
type ObservedUpdate struct {
	Agent            string     `json:"agent"`
	ChatID           int64      `json:"chat_id"`
	ThreadID         int64      `json:"thread_id"`
	MessageID        int64      `json:"message_id"`
	Conversation     string     `json:"conversation"`
	UserID           int64      `json:"user_id"`
	Username         string     `json:"username"`
	FirstName        string     `json:"first_name"`
	Text             string     `json:"text"`
	IsChannelPost    bool       `json:"is_channel_post"`
	ReplyToMessageID int64      `json:"reply_to_message_id"`
	ReplyToText      string     `json:"reply_to_text"`
	Engaged          bool       `json:"engaged"`
	Reason           string     `json:"reason"`
	SentAt           *time.Time `json:"sent_at,omitempty"`
}

// Observer forwards group traffic to an endpoint the agent owns.
//
// The agent only ever sees the handful of messages it was handed for a reply,
// so it can answer a question and still have no idea what the room is about.
// This is the other half: every comment, engaged or skipped, is posted to the
// agent's own store, which is what "learns from the comments" means in
// practice -- the agent later searches its own record of the chat with the
// same tool machinery it uses for news.
//
// It is off unless TG_OBSERVE_URL[_<AGENT>] is set, so no existing binding
// starts shipping its users' messages anywhere because of this code.
type Observer struct {
	URL    string
	Token  string
	Agent  string
	client *http.Client
}

// NewObserverForAgent returns nil when the agent has no sink configured.
func NewObserverForAgent(agentName string) *Observer {
	url := envStrFor("TG_OBSERVE_URL", agentName)
	if url == "" {
		return nil
	}
	return &Observer{
		URL:    url,
		Token:  envStrFor("TG_OBSERVE_TOKEN", agentName),
		Agent:  agentName,
		client: &http.Client{Timeout: observeTimeout},
	}
}

// Observe ships one update. It never returns an error: a broken sink is a
// gap in the agent's memory, not a failure of the reply path, and the log
// line is the only thing that should notice.
func (o *Observer) Observe(ctx context.Context, u TelegramUpdate, decision EngageDecision) {
	if o == nil || !IsGroup(u.ChatType) {
		return
	}
	payload := ObservedUpdate{
		Agent:            o.Agent,
		ChatID:           u.ChatID,
		ThreadID:         u.ThreadID,
		MessageID:        u.MessageID,
		Conversation:     ConversationKey(u),
		UserID:           u.UserID,
		Username:         u.Username,
		FirstName:        u.FirstName,
		Text:             u.Text,
		IsChannelPost:    IsChannelPost(u),
		ReplyToMessageID: u.ReplyToMessageID,
		ReplyToText:      u.ReplyToText,
		Engaged:          decision.Engage,
		Reason:           decision.Reason,
	}
	if !u.SentAt.IsZero() {
		sent := u.SentAt.UTC()
		payload.SentAt = &sent
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Warn().Err(err).Str("agent", o.Agent).Msg("tggateway: observation encode failed")
		return
	}
	go o.post(ctx, body)
}

func (o *Observer) post(ctx context.Context, body []byte) {
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, o.URL, bytes.NewReader(body))
	if err != nil {
		log.Warn().Err(err).Str("agent", o.Agent).Msg("tggateway: observation request build failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		log.Warn().Err(err).Str("agent", o.Agent).Msg("tggateway: observation post failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Warn().Int("status", resp.StatusCode).Str("agent", o.Agent).
			Msg("tggateway: observation rejected")
	}
}
