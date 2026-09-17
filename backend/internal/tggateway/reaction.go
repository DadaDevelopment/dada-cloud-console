package tggateway

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// ReactionRule fires Emoji as an instant Telegram message reaction (not a
// reply) when the inbound text contains any of Match, case-insensitively.
// It exists for template outcome moments -- registration done, deposit
// landed, broker switch confirmed -- where the user needs proof-of-receipt
// faster than an LLM turn can produce one. It never replaces the real
// agent reply; that still comes through the normal debounce/pacing path.
type ReactionRule struct {
	Match []string `json:"match"`
	Emoji string   `json:"emoji"`
}

// ReactionConfig is per-agent so the feature stays opt-in: an agent absent
// from Agents gets zero reaction behaviour, zero overhead.
type ReactionConfig struct {
	Agents map[string][]ReactionRule
}

// ReactionConfigFromEnv reads TG_GATEWAY_REACTION_RULES: a JSON object
// mapping agent name to its rule list, e.g.
//
//	{"broker-bot":[{"match":["зарегистр","registered"],"emoji":"👍"}]}
//
// Empty/unset/invalid JSON all yield nil, which callers must treat as "no
// agent has reactions configured" -- never a fetch error.
func ReactionConfigFromEnv() *ReactionConfig {
	raw := strings.TrimSpace(os.Getenv("TG_GATEWAY_REACTION_RULES"))
	if raw == "" {
		return nil
	}
	var agents map[string][]ReactionRule
	if err := json.Unmarshal([]byte(raw), &agents); err != nil {
		log.Warn().Err(err).Msg("tggateway: TG_GATEWAY_REACTION_RULES invalid JSON, reactions disabled")
		return nil
	}
	if len(agents) == 0 {
		return nil
	}
	return &ReactionConfig{Agents: agents}
}

// RulesFor returns agentName's rules, or nil if it has none configured.
func (c *ReactionConfig) RulesFor(agentName string) []ReactionRule {
	if c == nil {
		return nil
	}
	return c.Agents[agentName]
}

// matchReaction returns the emoji of the first rule whose Match list has a
// case-insensitive substring hit in text. Deterministic keyword matching is
// the default and, so far, the only mode: it covers the template-outcome
// case without a model call, and a fuzzy/model-backed mode is deliberately
// left for later rather than built ahead of a second agent asking for it.
func matchReaction(rules []ReactionRule, text string) (emoji string, ok bool) {
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	for _, rule := range rules {
		for _, m := range rule.Match {
			if m == "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(m)) {
				return rule.Emoji, true
			}
		}
	}
	return "", false
}

// fireReaction matches u.Text against rules and, on a hit, sets the
// reaction in the background: it must never delay or fail the caller's
// poll loop, since the whole point is not sitting in the way of the real
// reply.
func fireReaction(ctx context.Context, tg TelegramClient, token string, u TelegramUpdate, rules []ReactionRule) {
	if len(rules) == 0 {
		return
	}
	emoji, ok := matchReaction(rules, u.Text)
	if !ok {
		return
	}
	go func() {
		if err := tg.SendMessageReaction(ctx, token, u.ChatID, u.MessageID, emoji); err != nil {
			log.Warn().Err(err).Int64("chatID", u.ChatID).Str("emoji", emoji).Msg("tggateway: setMessageReaction failed")
		}
	}()
}
