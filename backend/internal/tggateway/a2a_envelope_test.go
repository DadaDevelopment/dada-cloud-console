package tggateway

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// capturingA2A records what the A2A branch actually puts on the wire. The
// golden test pins InboundContent, but the defect it could not see lived one
// layer up: the poller used to hand A2A the raw u.Text, so an agent reached
// over A2A got a bare comment while an agent reached over the runtime got the
// full transcript -- the same chat, two different inputs, and every eval
// graded the second one.
type capturingA2A struct {
	mu    sync.Mutex
	texts []string
}

func (c *capturingA2A) Send(context.Context, string, string) (string, error) { return "", nil }

func (c *capturingA2A) SendWithContext(_ context.Context, _ string, _ string, text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return "", nil
}

func (c *capturingA2A) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.texts))
	copy(out, c.texts)
	return out
}

func TestA2AEnvelope_ChannelPostCarriesMarkerAndMedia(t *testing.T) {
	t.Setenv("TG_GROUP_POST_COMMENT_RATE_ENVELOPE_POST", "1")

	tg := &onceTelegram{updates: []TelegramUpdate{{
		UpdateID:           1,
		ChatID:             -1002171703932,
		ChatType:           "supergroup",
		MessageID:          10,
		Text:               "Клод переезжает на новую память, вот что показывают в настройках",
		IsAutomaticForward: true,
		FromIsBot:          true,
		SenderChatID:       -1002432019190,
		Attachment:         &TelegramAttachment{Kind: "image", FileID: "photo-1"},
	}}}
	a2a := &capturingA2A{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, a2a, nil, Binding{AgentName: "envelope-post", BotToken: "tok", BotUsername: "bot"}, nil)

	waitFor(t, func() bool { return len(a2a.seen()) == 1 })
	got := a2a.seen()[0]

	if !strings.Contains(got, channelPostMarker) {
		t.Errorf("A2A envelope lost the channel-post marker:\n%s", got)
	}
	if !strings.Contains(got, "[изображение:") {
		t.Errorf("A2A envelope lost the picture the post was made of:\n%s", got)
	}
	if !strings.Contains(got, "Клод переезжает") {
		t.Errorf("A2A envelope lost the post text:\n%s", got)
	}
	if !strings.HasPrefix(got, "[telegram_username:") {
		t.Errorf("A2A envelope lost the identity header:\n%s", got)
	}
}

func TestA2AEnvelope_CommentCarriesQuoteAndSpeaker(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{{
		UpdateID:         2,
		ChatID:           -1002171703932,
		UserID:           501,
		ChatType:         "supergroup",
		MessageID:        11,
		ThreadID:         10,
		ReplyToMessageID: 10,
		Text:             "а вот это что значит, у них там миграция сама пройдёт?",
		FirstName:        "Игорь",
		Username:         "igor_dev",
		ReplyToText:      "Клод переезжает на новую память",
		ReplyToIsChannel: true,
		Attachment:       &TelegramAttachment{Kind: "image", FileID: "photo-2"},
	}}}
	a2a := &capturingA2A{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, a2a, nil, Binding{AgentName: "envelope-comment", BotToken: "tok", BotUsername: "bot"}, nil)

	waitFor(t, func() bool { return len(a2a.seen()) == 1 })
	got := a2a.seen()[0]

	if !strings.Contains(got, "[в ответ на пост канала:") {
		t.Errorf("A2A envelope lost the post the comment answers:\n%s", got)
	}
	if !strings.Contains(got, "Игорь (@igor_dev):") {
		t.Errorf("A2A envelope lost the speaker:\n%s", got)
	}
	if !strings.Contains(got, "[изображение:") {
		t.Errorf("A2A envelope lost the attached picture:\n%s", got)
	}
}
