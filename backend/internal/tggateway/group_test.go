package tggateway

import (
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
