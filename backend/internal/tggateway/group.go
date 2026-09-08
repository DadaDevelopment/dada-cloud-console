package tggateway

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// EngageDecision is why the gateway did or did not hand an update to the
// agent. It is a typed value rather than a bare bool so the reason shows up
// in logs and in tests: "the bot stayed quiet" and "the bot never saw the
// message" are different bugs, and without a reason they look identical.
type EngageDecision struct {
	Engage bool
	Reason string
}

const (
	ReasonPrivate         = "private_chat"
	ReasonMention         = "mentions_bot"
	ReasonReplyToBot      = "reply_to_bot"
	ReasonSubstantive     = "substantive_comment"
	ReasonFromBot         = "sender_is_bot"
	ReasonChannelPost     = "channel_post_itself"
	ReasonNoContent       = "no_text_or_media"
	ReasonTooShort        = "reaction_not_a_question"
	ReasonBudgetExhausted = "hourly_budget_exhausted"
	ReasonMentionRequired = "mention_required_by_config"
)

// GroupPolicy decides which group messages are worth an agent turn.
//
// A channel's discussion chat is not a support inbox: most comments are
// reactions addressed to the channel or to each other, and a bot that
// answers all of them is spam no matter how good each individual reply is.
// The private-chat path is untouched — every direct message still reaches
// the agent.
//
// The gateway only filters what it can judge without a model: who sent it,
// whether it names the bot, and whether it carries enough text to be a
// question. Relevance is left to the agent, which can decline by answering
// with nothing; that split keeps the cheap decision cheap and the expensive
// one informed.
type GroupPolicy struct {
	BotUsername     string
	MinChars        int
	HourlyBudget    int
	RequireMention  bool
	QuestionMarkers []string

	mu     sync.Mutex
	recent map[string][]time.Time
	now    func() time.Time
}

var defaultQuestionMarkers = []string{
	"?", "как ", "почему", "зачем", "чем ", "что лучше", "кто-нибудь", "подскажи",
	"стоит ли", "не работает", "ошибка", "help", "how ", "why ", "which ",
}

// NewGroupPolicy reads the tunables from env, falling back to values that
// keep a busy comment section survivable: a reply must be earned by either
// naming the bot, replying to it, or asking something that looks like a
// question.
func NewGroupPolicy(botUsername string) *GroupPolicy {
	return &GroupPolicy{
		BotUsername:     strings.TrimPrefix(strings.ToLower(botUsername), "@"),
		MinChars:        envInt("TG_GROUP_MIN_CHARS", 24),
		HourlyBudget:    envInt("TG_GROUP_HOURLY_BUDGET", 8),
		RequireMention:  os.Getenv("TG_GROUP_REQUIRE_MENTION") == "1",
		QuestionMarkers: defaultQuestionMarkers,
		recent:          map[string][]time.Time{},
		now:             time.Now,
	}
}

func envInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

// IsGroup reports whether the chat type is one where many people speak.
func IsGroup(chatType string) bool {
	return chatType == "group" || chatType == "supergroup"
}

func (p *GroupPolicy) mentionsBot(u TelegramUpdate) bool {
	if p.BotUsername == "" {
		return false
	}
	return strings.Contains(strings.ToLower(u.Text), "@"+p.BotUsername)
}

func (p *GroupPolicy) looksSubstantive(text string) bool {
	trimmed := strings.TrimSpace(text)
	letters := 0
	for _, r := range trimmed {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < p.MinChars {
		low := strings.ToLower(trimmed)
		for _, marker := range p.QuestionMarkers {
			if strings.Contains(low, marker) && letters >= 6 {
				return true
			}
		}
		return false
	}
	return true
}

// Decide runs the cheap gate. It never consumes budget; call Charge only
// once a reply has actually been sent, so a run that ends in silence does
// not eat the quota it did not use.
func (p *GroupPolicy) Decide(u TelegramUpdate) EngageDecision {
	if u.FromIsBot {
		return EngageDecision{false, ReasonFromBot}
	}
	if !IsGroup(u.ChatType) {
		return EngageDecision{true, ReasonPrivate}
	}
	if u.IsAutomaticForward || (u.SenderChatID != 0 && u.SenderChatID != u.ChatID) {
		return EngageDecision{false, ReasonChannelPost}
	}
	if strings.TrimSpace(u.Text) == "" && u.Attachment == nil && !u.HasLocation {
		return EngageDecision{false, ReasonNoContent}
	}
	if p.mentionsBot(u) {
		return EngageDecision{true, ReasonMention}
	}
	if u.ReplyToIsBot && u.ReplyToUsername != "" && strings.EqualFold(u.ReplyToUsername, p.BotUsername) {
		return EngageDecision{true, ReasonReplyToBot}
	}
	if p.RequireMention {
		return EngageDecision{false, ReasonMentionRequired}
	}
	if !p.looksSubstantive(u.Text) {
		return EngageDecision{false, ReasonTooShort}
	}
	if !p.hasBudget(ConversationKey(u)) {
		return EngageDecision{false, ReasonBudgetExhausted}
	}
	return EngageDecision{true, ReasonSubstantive}
}

func (p *GroupPolicy) prune(key string, now time.Time) []time.Time {
	cutoff := now.Add(-time.Hour)
	kept := p.recent[key][:0]
	for _, t := range p.recent[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	p.recent[key] = kept
	return kept
}

func (p *GroupPolicy) hasBudget(key string) bool {
	if p.HourlyBudget <= 0 {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prune(key, p.now())) < p.HourlyBudget
}

// Charge records one delivered reply against the conversation's hourly quota.
func (p *GroupPolicy) Charge(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recent[key] = append(p.prune(key, p.now()), p.now())
}

// Spent reports how many replies the conversation has used in the last hour.
func (p *GroupPolicy) Spent(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prune(key, p.now()))
}

// ConversationKey identifies the dialogue an update belongs to.
//
// In a channel's discussion chat every post gets its own comment thread, and
// Telegram carries that thread id on each comment. Keying only on the chat
// id would merge every post's comments into one conversation: the agent
// would answer a question about yesterday's post with context from today's,
// and one slow run would cancel an unrelated thread's run through the
// interrupt state, which is keyed the same way.
func ConversationKey(u TelegramUpdate) string {
	if IsGroup(u.ChatType) && u.ThreadID != 0 {
		return fmt.Sprintf("%d:%d", u.ChatID, u.ThreadID)
	}
	return strconv.FormatInt(u.ChatID, 10)
}

// quotedContextLimit caps how much of the quoted message travels with the
// comment. A channel post can be several thousand characters and the comment
// answering it is one line; the whole post would drown the actual question.
const quotedContextLimit = 400

// QuotedContext renders what a group message is answering, or "" when there
// is nothing to render.
//
// A comment under a channel post is unreadable on its own: "а это точно
// быстрее?" means nothing without the post it hangs under, and telegram
// delivers that post only inside reply_to_message. Without this the agent
// answers a question it cannot see, which reads to a human as a bot that did
// not read the thread.
//
// Private chats are excluded: there the previous message is already in the
// conversation the runtime keeps.
func QuotedContext(u TelegramUpdate) string {
	if !IsGroup(u.ChatType) {
		return ""
	}
	quoted := strings.TrimSpace(u.ReplyToText)
	if quoted == "" {
		return ""
	}
	if len([]rune(quoted)) > quotedContextLimit {
		quoted = string([]rune(quoted)[:quotedContextLimit]) + "..."
	}
	quoted = strings.Join(strings.Fields(quoted), " ")
	source := "сообщение"
	switch {
	case u.ReplyToIsChannel:
		source = "пост канала"
	case u.ReplyToUsername != "":
		source = "@" + u.ReplyToUsername
	}
	return fmt.Sprintf("[в ответ на %s: %q]", source, quoted)
}

// GroupSpeaker renders the author prefix for a group message.
//
// All comments in one thread share a single conversation, so without a
// per-message author the agent sees a merged monologue and answers the wrong
// person. Private chats keep the raw text: there the sender is already the
// conversation.
// InboundContent is documented in agentkit/transcript.py: the runtime never sees a bare
// comment, it sees who spoke and what was quoted. The rule is pinned across both runtimes
// by agentkit/transcript_golden.json.
func InboundContent(u TelegramUpdate) string {
	content := u.Text
	if speaker := GroupSpeaker(u); speaker != "" {
		content = fmt.Sprintf("%s: %s", speaker, content)
	}
	if quoted := QuotedContext(u); quoted != "" {
		content = fmt.Sprintf("%s\n%s", quoted, content)
	}
	return content
}

func GroupSpeaker(u TelegramUpdate) string {
	if !IsGroup(u.ChatType) {
		return ""
	}
	name := u.FirstName
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = "аноним"
	}
	if u.Username != "" {
		return fmt.Sprintf("%s (@%s)", name, u.Username)
	}
	return name
}
