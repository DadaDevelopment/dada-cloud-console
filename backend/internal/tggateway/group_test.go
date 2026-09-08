package tggateway

import (
	"strings"
	"testing"
	"time"
)

func testPolicy() *GroupPolicy {
	return &GroupPolicy{
		BotUsername:     "vibecoder_bot",
		MinChars:        24,
		HourlyBudget:    2,
		QuestionMarkers: defaultQuestionMarkers,
		recent:          map[string][]time.Time{},
		now:             time.Now,
	}
}

func groupMsg(text string) TelegramUpdate {
	return TelegramUpdate{ChatID: -100, ChatType: "supergroup", ThreadID: 7, Text: text, UserID: 5}
}

func TestDecide_PrivateChatAlwaysEngages(t *testing.T) {
	p := testPolicy()
	d := p.Decide(TelegramUpdate{ChatID: 1, ChatType: "private", Text: "+"})
	if !d.Engage || d.Reason != ReasonPrivate {
		t.Fatalf("private chat must stay unfiltered, got %+v", d)
	}
}

func TestDecide_DropsNoise(t *testing.T) {
	p := testPolicy()
	cases := []struct {
		name   string
		u      TelegramUpdate
		reason string
	}{
		{"another bot", TelegramUpdate{ChatType: "supergroup", Text: "какой-то длинный текст от бота", FromIsBot: true}, ReasonFromBot},
		{"channel post copy", TelegramUpdate{ChatID: -100, ChatType: "supergroup", Text: "новый пост канала про агентов", IsAutomaticForward: true}, ReasonChannelPost},
		{"channel as author", TelegramUpdate{ChatID: -100, ChatType: "supergroup", Text: "пост от лица канала", SenderChatID: -200}, ReasonChannelPost},
		{"empty", TelegramUpdate{ChatID: -100, ChatType: "supergroup", Text: "   "}, ReasonNoContent},
		{"reaction", groupMsg("ахаха"), ReasonTooShort},
		{"plus one", groupMsg("+"), ReasonTooShort},
		{"thanks", groupMsg("спасибо"), ReasonTooShort},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := p.Decide(c.u)
			if d.Engage {
				t.Fatalf("must not engage, got %+v", d)
			}
			if d.Reason != c.reason {
				t.Fatalf("reason %q, want %q", d.Reason, c.reason)
			}
		})
	}
}

func TestDecide_EngagesWhenAddressed(t *testing.T) {
	p := testPolicy()
	mention := groupMsg("@vibecoder_bot а ты как?")
	if d := p.Decide(mention); !d.Engage || d.Reason != ReasonMention {
		t.Fatalf("mention must engage regardless of length, got %+v", d)
	}
	reply := groupMsg("а пруф?")
	reply.ReplyToIsBot = true
	reply.ReplyToUsername = "vibecoder_bot"
	if d := p.Decide(reply); !d.Engage || d.Reason != ReasonReplyToBot {
		t.Fatalf("reply to bot must engage, got %+v", d)
	}
	other := groupMsg("а пруф?")
	other.ReplyToIsBot = true
	other.ReplyToUsername = "some_other_bot"
	if d := p.Decide(other); d.Engage {
		t.Fatalf("reply to a different bot must not engage, got %+v", d)
	}
}

func TestDecide_ShortQuestionStillEngages(t *testing.T) {
	p := testPolicy()
	if d := p.Decide(groupMsg("а чем cursor лучше?")); !d.Engage || d.Reason != ReasonSubstantive {
		t.Fatalf("short question must engage, got %+v", d)
	}
	if d := p.Decide(groupMsg("норм?")); d.Engage {
		t.Fatalf("a question mark alone must not buy a reply, got %+v", d)
	}
}

func TestDecide_RequireMentionMode(t *testing.T) {
	p := testPolicy()
	p.RequireMention = true
	if d := p.Decide(groupMsg("развёрнутый вопрос про агентские рантаймы и оценку")); d.Engage {
		t.Fatalf("mention-only mode must stay silent, got %+v", d)
	}
	if d := p.Decide(groupMsg("@vibecoder_bot ну?")); !d.Engage {
		t.Fatalf("mention must still work in mention-only mode, got %+v", d)
	}
}

func TestBudget_ExhaustsAndRecovers(t *testing.T) {
	p := testPolicy()
	now := time.Now()
	p.now = func() time.Time { return now }
	msg := groupMsg("длинный осмысленный вопрос про агентов и рантайм")
	key := ConversationKey(msg)

	for i := 0; i < p.HourlyBudget; i++ {
		if d := p.Decide(msg); !d.Engage {
			t.Fatalf("reply %d must be allowed, got %+v", i, d)
		}
		p.Charge(key)
	}
	if d := p.Decide(msg); d.Engage || d.Reason != ReasonBudgetExhausted {
		t.Fatalf("budget must stop the third reply, got %+v", d)
	}
	if got := p.Spent(key); got != p.HourlyBudget {
		t.Fatalf("spent %d, want %d", got, p.HourlyBudget)
	}

	now = now.Add(61 * time.Minute)
	if d := p.Decide(msg); !d.Engage {
		t.Fatalf("budget must recover after an hour, got %+v", d)
	}
	if got := p.Spent(key); got != 0 {
		t.Fatalf("window must be empty after an hour, spent %d", got)
	}
}

func TestBudget_IsPerThreadNotPerChat(t *testing.T) {
	p := testPolicy()
	a := groupMsg("вопрос под первым постом, вполне развёрнутый")
	b := groupMsg("вопрос под вторым постом, тоже развёрнутый")
	b.ThreadID = 9
	for i := 0; i < p.HourlyBudget; i++ {
		p.Charge(ConversationKey(a))
	}
	if d := p.Decide(a); d.Engage {
		t.Fatalf("first thread must be exhausted, got %+v", d)
	}
	if d := p.Decide(b); !d.Engage {
		t.Fatalf("second thread must have its own budget, got %+v", d)
	}
}

func TestConversationKey_SeparatesThreads(t *testing.T) {
	group := groupMsg("текст")
	if got := ConversationKey(group); got != "-100:7" {
		t.Fatalf("group key %q", got)
	}
	noThread := group
	noThread.ThreadID = 0
	if got := ConversationKey(noThread); got != "-100" {
		t.Fatalf("threadless group key %q", got)
	}
	private := TelegramUpdate{ChatID: 42, ChatType: "private", ThreadID: 3}
	if got := ConversationKey(private); got != "42" {
		t.Fatalf("private key %q, forum topics in DMs must not split the session", got)
	}
}

func TestGroupSpeaker(t *testing.T) {
	u := groupMsg("текст")
	u.FirstName = "Петя"
	u.Username = "petya"
	if got := GroupSpeaker(u); got != "Петя (@petya)" {
		t.Fatalf("speaker %q", got)
	}
	u.Username = ""
	if got := GroupSpeaker(u); got != "Петя" {
		t.Fatalf("speaker without username %q", got)
	}
	u.FirstName = ""
	if got := GroupSpeaker(u); got != "аноним" {
		t.Fatalf("anonymous speaker %q", got)
	}
	private := TelegramUpdate{ChatType: "private", FirstName: "Петя"}
	if got := GroupSpeaker(private); got != "" {
		t.Fatalf("private chat must carry no speaker prefix, got %q", got)
	}
}

func TestQuotedContext_ChannelPostAndUser(t *testing.T) {
	u := groupMsg("а это точно быстрее?")
	u.ReplyToText = "Выкатили новый рантайм, холодный старт 5 секунд"
	u.ReplyToIsChannel = true
	got := QuotedContext(u)
	want := `[в ответ на пост канала: "Выкатили новый рантайм, холодный старт 5 секунд"]`
	if got != want {
		t.Fatalf("channel quote %q, want %q", got, want)
	}

	u.ReplyToIsChannel = false
	u.ReplyToUsername = "petya"
	if got := QuotedContext(u); got != `[в ответ на @petya: "Выкатили новый рантайм, холодный старт 5 секунд"]` {
		t.Fatalf("user quote %q", got)
	}
}

func TestQuotedContext_AbsentWhenNothingToQuote(t *testing.T) {
	if got := QuotedContext(groupMsg("просто реплика")); got != "" {
		t.Fatalf("expected no quote, got %q", got)
	}
	private := TelegramUpdate{ChatType: "private", Text: "привет", ReplyToText: "прошлое сообщение"}
	if got := QuotedContext(private); got != "" {
		t.Fatalf("private chat needs no quote, got %q", got)
	}
}

func TestQuotedContext_LongPostIsTruncated(t *testing.T) {
	u := groupMsg("и что?")
	u.ReplyToIsChannel = true
	u.ReplyToText = strings.Repeat("я", quotedContextLimit+50)
	got := QuotedContext(u)
	if !strings.HasSuffix(got, `..."]`) {
		t.Fatalf("expected an ellipsis on a long post, got tail %q", got[len(got)-20:])
	}
	if len([]rune(got)) > quotedContextLimit+60 {
		t.Fatalf("quote not truncated, %d runes", len([]rune(got)))
	}
}

func channelPost(text string) TelegramUpdate {
	return TelegramUpdate{
		ChatID:             -100,
		ChatType:           "supergroup",
		ThreadID:           11,
		MessageID:          42,
		Text:               text,
		FromIsBot:          true,
		SenderChatID:       -200,
		IsAutomaticForward: true,
	}
}

func TestDecide_ChannelPostStaysSilentAtRateZero(t *testing.T) {
	p := testPolicy()
	p.roll = func() float64 { return 0 }
	if d := p.Decide(channelPost("вышел новый пост про агентов")); d.Engage {
		t.Fatalf("rate 0 must keep the old behaviour, got %+v", d)
	}
}

func TestDecide_ChannelPostCommentsOnAWinningRoll(t *testing.T) {
	p := testPolicy()
	p.PostCommentRate = 0.5
	p.roll = func() float64 { return 0.49 }
	d := p.Decide(channelPost("вышел новый пост про агентов"))
	if !d.Engage || d.Reason != ReasonPostComment {
		t.Fatalf("winning roll must open a comment, got %+v", d)
	}
	p.roll = func() float64 { return 0.5 }
	if d := p.Decide(channelPost("вышел новый пост про агентов")); d.Engage {
		t.Fatalf("losing roll must stay silent, got %+v", d)
	}
}

func TestDecide_EmptyChannelPostIsNeverCommented(t *testing.T) {
	p := testPolicy()
	p.PostCommentRate = 1
	p.roll = func() float64 { return 0 }
	if d := p.Decide(channelPost("   ")); d.Engage || d.Reason != ReasonNoContent {
		t.Fatalf("a post with no text is nothing to comment on, got %+v", d)
	}
}

func TestDecide_ChannelPostIsCheckedBeforeTheBotFilter(t *testing.T) {
	p := testPolicy()
	p.PostCommentRate = 1
	p.roll = func() float64 { return 0 }
	post := channelPost("пост канала приезжает как форвард от бота")
	if !post.FromIsBot {
		t.Fatal("the fixture must keep is_bot: that is exactly what hid the post")
	}
	if d := p.Decide(post); !d.Engage {
		t.Fatalf("the post must survive the bot filter, got %+v", d)
	}
}

func TestDecide_CommentUnderAPostStillEngagesNormally(t *testing.T) {
	p := testPolicy()
	p.PostCommentRate = 1
	p.roll = func() float64 { return 0 }
	comment := groupMsg("а на проде это правда держит нагрузку или только в демо?")
	comment.ReplyToText = "вышел новый пост про агентов"
	comment.ReplyToIsChannel = true
	d := p.Decide(comment)
	if !d.Engage || d.Reason != ReasonSubstantive {
		t.Fatalf("a human comment must not be graded as a post, got %+v", d)
	}
}

func TestInboundContent_ChannelPostDropsTheForwarder(t *testing.T) {
	post := channelPost("Собрали агента за вечер")
	post.FirstName = "Telegram"
	got := InboundContent(post)
	if !strings.HasPrefix(got, channelPostMarker) {
		t.Fatalf("post inbound = %q, want the post marker first", got)
	}
	if strings.Contains(got, "Telegram") {
		t.Fatalf("post inbound = %q, must not name a forwarder as the author", got)
	}
}

func TestNewGroupPolicyForAgent_PerAgentOverrideWins(t *testing.T) {
	t.Setenv("TG_GROUP_POST_COMMENT_RATE", "0")
	t.Setenv("TG_GROUP_POST_COMMENT_RATE_TG_VIBECODER", "0.5")
	t.Setenv("TG_GROUP_HOURLY_BUDGET", "8")

	other := NewGroupPolicyForAgent("bot", "tg-exchange-support")
	if other.PostCommentRate != 0 {
		t.Fatalf("another agent must keep the shared value, got %v", other.PostCommentRate)
	}
	mine := NewGroupPolicyForAgent("bot", "tg-vibecoder")
	if mine.PostCommentRate != 0.5 {
		t.Fatalf("agent override ignored, got %v", mine.PostCommentRate)
	}
	if mine.HourlyBudget != 8 {
		t.Fatalf("unset key must fall back to the shared value, got %d", mine.HourlyBudget)
	}
}
