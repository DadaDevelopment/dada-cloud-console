package tggateway

import (
	"context"
	"testing"
)

func TestMatchReaction(t *testing.T) {
	rules := []ReactionRule{
		{Match: []string{"зарегистр", "registered"}, Emoji: "\U0001F44D"},
		{Match: []string{"депозит"}, Emoji: "\U0001F4B0"},
	}

	cases := []struct {
		name      string
		text      string
		wantOK    bool
		wantEmoji string
	}{
		{"case insensitive hit", "Я ЗАРЕГИСТРировался!", true, "\U0001F44D"},
		{"second rule hit", "положил депозит", true, "\U0001F4B0"},
		{"no hit", "привет как дела", false, ""},
		{"empty text", "", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			emoji, ok := matchReaction(rules, c.text)
			if ok != c.wantOK || emoji != c.wantEmoji {
				t.Fatalf("matchReaction(%q) = (%q, %v), want (%q, %v)", c.text, emoji, ok, c.wantEmoji, c.wantOK)
			}
		})
	}
}

func TestMatchReaction_NoRules(t *testing.T) {
	if _, ok := matchReaction(nil, "зарегистрировался"); ok {
		t.Fatal("nil rules must never match")
	}
}

func TestReactionConfigFromEnv(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("TG_GATEWAY_REACTION_RULES", "")
		if cfg := ReactionConfigFromEnv(); cfg != nil {
			t.Fatalf("empty env must yield nil config, got %+v", cfg)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Setenv("TG_GATEWAY_REACTION_RULES", "{not json")
		if cfg := ReactionConfigFromEnv(); cfg != nil {
			t.Fatalf("invalid JSON must yield nil config, got %+v", cfg)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		t.Setenv("TG_GATEWAY_REACTION_RULES", `{"broker-bot":[{"match":["депозит"],"emoji":"👍"}]}`)
		cfg := ReactionConfigFromEnv()
		if cfg == nil {
			t.Fatal("valid JSON must yield a config")
		}
		rules := cfg.RulesFor("broker-bot")
		if len(rules) != 1 || rules[0].Emoji != "👍" {
			t.Fatalf("unexpected rules: %+v", rules)
		}
		if got := cfg.RulesFor("other-bot"); got != nil {
			t.Fatalf("agent absent from config must get nil rules, got %+v", got)
		}
	})
}

func TestReactionConfigRulesFor_NilConfig(t *testing.T) {
	var cfg *ReactionConfig
	if rules := cfg.RulesFor("any"); rules != nil {
		t.Fatalf("nil *ReactionConfig must return nil rules, got %+v", rules)
	}
}

// reactionRecordingTelegram is a minimal TelegramClient recording every
// setMessageReaction call, to prove fireReaction actually calls the API
// method and not just the matcher.
type reactionRecordingTelegram struct {
	fakeTelegram
	gotChatID, gotMessageID int64
	gotEmoji                string
	called                  chan struct{}
}

func (r *reactionRecordingTelegram) SendMessageReaction(_ context.Context, _ string, chatID int64, messageID int64, emoji string) error {
	r.gotChatID, r.gotMessageID, r.gotEmoji = chatID, messageID, emoji
	close(r.called)
	return nil
}

func TestFireReaction_CallsAPIOnMatch(t *testing.T) {
	tg := &reactionRecordingTelegram{called: make(chan struct{})}
	rules := []ReactionRule{{Match: []string{"депозит"}}}
	rules[0].Emoji = "👍"

	u := TelegramUpdate{ChatID: 42, MessageID: 7, Text: "закинул депозит"}
	fireReaction(context.Background(), tg, "tok", u, rules)

	<-tg.called
	if tg.gotChatID != 42 || tg.gotMessageID != 7 || tg.gotEmoji != "👍" {
		t.Fatalf("unexpected call: chatID=%d messageID=%d emoji=%q", tg.gotChatID, tg.gotMessageID, tg.gotEmoji)
	}
}

func TestFireReaction_NoMatchNeverCalls(t *testing.T) {
	tg := &reactionRecordingTelegram{called: make(chan struct{})}
	rules := []ReactionRule{{Match: []string{"депозит"}, Emoji: "👍"}}
	u := TelegramUpdate{ChatID: 42, MessageID: 7, Text: "привет"}
	fireReaction(context.Background(), tg, "tok", u, rules)

	select {
	case <-tg.called:
		t.Fatal("fireReaction must not call the API when nothing matched")
	default:
	}
}
