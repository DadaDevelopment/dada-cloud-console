package agentruntime

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Narrow hand-off (plan 4.1). Today a hands-off escalation pauses the agent
// outright: every further customer message is answered by silence plus one
// fixed receipt, and QA 2026-09-15 found dead chats where the curator never
// actually wrote. The narrow mode keeps the agent alive but mute outside a
// short list of factual topics: inside the list it answers as usual, outside
// it says nothing and the operator's card grows by the customer's line, so
// the chat is neither dead nor a bot speaking for the curator.
//
// State lives in conversations.metadata under narrowModeKey, the same JSONB
// column idle_fired_at and escalation_ack_sent_at already use, so the mode
// needs no schema migration. agent_enabled stays true and pause_reason stays
// empty: a narrow conversation is not paused, and an operator pausing it by
// hand must still win.
const narrowModeKey = "narrow_since"

// narrowTopicPatterns is the white list from plan 4.1: commission/fees,
// withdrawal, MT5 and verification/KYC. These are the questions whose answer
// is a fixed fact from the KB, so answering them cannot contradict whatever
// the curator is arranging. Everything else is the curator's.
var narrowTopicPatterns = []string{
	`(?i)комисси|комисс|fee\b|fees\b|сбор за|процент за`,
	`(?i)вывод|вывест|снят(ь|ие)|withdraw`,
	`(?i)\bmt-?\s?[45]\b|метатрейд|meta\s?trader`,
	`(?i)верифик|kyc|подтвержден[ие]+ личност|документ[ыа]? для|паспорт`,
}

// ParseNarrowTopics compiles the white list. value is
// AGENT_RUNTIME_NARROW_TOPICS: a comma-separated list of Go regexps that
// REPLACES the built-in list (empty keeps the built-in one). A pattern that
// does not compile is skipped with the rest kept, because a typo in a
// manifest must narrow the list, never crash the runtime; a pattern that
// needs a literal comma writes it as [,].
func ParseNarrowTopics(value string) []*regexp.Regexp {
	raw := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			raw = append(raw, part)
		}
	}
	if len(raw) == 0 {
		raw = narrowTopicPatterns
	}
	out := make([]*regexp.Regexp, 0, len(raw))
	for _, pattern := range raw {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

// narrowTopicAllowed reports whether this customer turn is one the agent may
// still answer while the curator owns the chat.
func narrowTopicAllowed(text string, topics []*regexp.Regexp) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, re := range topics {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// narrowSince reads the moment the narrow mode started. ok is false when the
// conversation is not in narrow mode, including when the stored value is not
// a timestamp -- an unreadable mark must not mute a chat forever.
func narrowSince(conv Conversation) (time.Time, bool) {
	raw, _ := conv.Metadata[narrowModeKey].(string)
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// narrowExpired reports whether the agent returns to its normal mode.
// returnAfter is AGENT_RUNTIME_NARROW_RETURN_HOURS; 0 means never, which is
// the default because how long a curator owns a chat is the owner's call,
// not the runtime's guess.
func narrowExpired(since, now time.Time, returnAfter time.Duration) bool {
	return returnAfter > 0 && !since.IsZero() && now.Sub(since) >= returnAfter
}

// markPendingHandled drains the inbox for a turn the runtime has answered by
// other means than a model reply. Without it a hand-off followed by a silent
// model turn leaves the input pending, and AGENT_RUNTIME_SILENCE_RECOVERY
// replays it: a second client line and a second operator card for one event.
func (r *Runtime) markPendingHandled(ctx context.Context, convID uuid.UUID) error {
	inbox, ok := r.store.(interface {
		PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error)
	})
	receipts, hasReceipts := r.store.(interface {
		MarkRuntimeHandled(context.Context, []Message) error
	})
	if !ok || !hasReceipts {
		return errors.New("runtime pending input storage is not configured")
	}
	pending, err := inbox.PendingRuntimeMessages(ctx, convID)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	return receipts.MarkRuntimeHandled(ctx, pending)
}

// narrowCardFooter is what the operator reads under a card raised by a
// message the agent deliberately did not answer.
const narrowCardFooter = "Агент в узком режиме: отвечает сам только по списку тем, остальное ждёт вас."

// narrowGate is the inbound half of the narrow mode. handled=true means the
// turn is over: the customer's message is recorded and shown to the operator,
// and the agent says nothing. handled=false means the turn proceeds normally,
// either because the conversation is not in narrow mode, because the mode has
// expired (and is cleared here), or because the question is on the white list.
func (r *Runtime) narrowGate(ctx context.Context, conv Conversation, state RuntimeState, pending []Message) (MessageResponse, bool, error) {
	since, ok := narrowSince(conv)
	if !ok {
		return MessageResponse{}, false, nil
	}
	if narrowExpired(since, time.Now(), r.flags.NarrowReturnAfter) {
		if err := r.store.ClearNarrowMode(ctx, conv.ID); err != nil {
			return MessageResponse{}, false, err
		}
		log.Info().Str("conversation", conv.ID.String()).Time("since", since).
			Dur("after", r.flags.NarrowReturnAfter).Msg("agentruntime: narrow mode expired, agent back to normal")
		return MessageResponse{}, false, nil
	}
	for _, m := range pending {
		if narrowTopicAllowed(m.Content, r.flags.NarrowTopics) {
			log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: narrow mode, question on the white list, answering")
			return MessageResponse{}, false, nil
		}
	}

	texts := make([]string, 0, len(pending))
	for _, m := range pending {
		if t := strings.TrimSpace(m.Content); t != "" {
			texts = append(texts, t)
		}
	}
	summary := "Клиент написал, агент промолчал (тема вне списка):\n\n" + strings.Join(texts, "\n")
	if r.notifyOperator != nil {
		if err := r.notifyOperator(ctx, conv, escalationCardWithFooter("🔕 Сообщение клиенту в узком режиме", conv, "", summary, state, narrowCardFooter)); err != nil {
			log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: narrow mode card not delivered")
		}
	} else {
		log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: narrow mode silence, no operator notifier configured")
	}

	receipts, ok := r.store.(interface {
		MarkRuntimeHandled(context.Context, []Message) error
	})
	if !ok {
		return MessageResponse{}, true, errors.New("runtime receipt storage is not configured")
	}
	if err := receipts.MarkRuntimeHandled(ctx, pending); err != nil {
		return MessageResponse{}, true, err
	}
	if err := r.store.Touch(ctx, conv.ID); err != nil {
		return MessageResponse{}, true, err
	}
	log.Info().Str("conversation", conv.ID.String()).Int("messages", len(pending)).
		Msg("agentruntime: narrow mode, question outside the white list, staying silent")
	return MessageResponse{Suppressed: true}, true, nil
}
