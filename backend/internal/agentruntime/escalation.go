package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// escalationReasons is the closed list from call_center/docs/06 §escalation.
// The model picks one; anything else is rejected so the operator card never
// carries a made-up category.
var escalationReasons = map[string]string{
	"E_DEPOSIT_HANDOFF":     "клиент сообщил о пополнении, дальше ведёт куратор",
	"E_TECH_BLOCKED":        "клиент застрял технически",
	"E_LEGAL_TAX":           "юридический или налоговый вопрос",
	"E_DISTRUST":            "недоверие, требует доказательств",
	"E_LOST_MONEY":          "уже терял деньги, боится",
	"E_WITHDRAW":            "вопрос о выводе средств",
	"E_TERMS_OFF_LADDER":    "просит условия вне лестницы",
	"E_PAYMENT_UNCONFIRMED": "оплата не подтверждается",
	"E_GUARANTEE_DEMAND":    "требует гарантий",
	"E_SECOND_PERSON":       "в диалоге второй человек",
	"E_OTHER":               "другое",
}

// clientMessageFeminineVerbs is the same list qa/tools/corpus_voice.py's FEM
// regex flags in the main reply channel (S92 class): the agent's persona is
// male, but the model sometimes writes first-person past tense in feminine
// grammatical gender. core.md's voice rule fixed the main reply channel; it
// does not reliably reach escalate_to_operator's separate client_message
// argument, so this is a runtime backstop specific to that field.
var clientMessageFeminineVerbs = map[string]string{
	"поняла":        "понял",
	"отправила":     "отправил",
	"зафиксировала": "зафиксировал",
	"записала":      "записал",
	"знакома":       "знаком",
	"рада":          "рад",
	"услышала":      "услышал",
	"увидела":       "увидел",
	"прочитала":     "прочитал",
	"спрашивала":    "спрашивал",
	"вернулась":     "вернулся",
	"передала":      "передал",
	"посмотрела":    "посмотрел",
	"проверила":     "проверил",
	"получила":      "получил",
	"нашла":         "нашёл",
	"сказала":       "сказал",
	"написала":      "написал",
	"ответила":      "ответил",
	"подумала":      "подумал",
	"учла":          "учёл",
	"отметила":      "отметил",
	"занесла":       "занёс",
	"уточнила":      "уточнил",
	"разобралась":   "разобрался",
	"готова":        "готов",
}

var clientMessageFeminineVerbPattern = regexp.MustCompile(`(?i)(^|[^\p{L}])(` +
	strings.Join([]string{
		"поняла", "отправила", "зафиксировала", "записала", "знакома", "рада",
		"услышала", "увидела", "прочитала", "спрашивала", "вернулась", "передала",
		"посмотрела", "проверила", "получила", "нашла", "сказала", "написала",
		"ответила", "подумала", "учла", "отметила", "занесла", "уточнила",
		"разобралась", "готова",
	}, "|") + `)([^\p{L}]|$)`)

// fixClientMessageGender rewrites known feminine first-person past-tense verb
// forms to masculine in place. Looping a small, fixed number of times (rather
// than a single ReplaceAll pass) lets two flagged words sitting back to back
// both get corrected, since consuming a boundary character as part of one
// match can otherwise hide an immediately adjacent match.
func fixClientMessageGender(text string) string {
	for i := 0; i < 3 && clientMessageFeminineVerbPattern.MatchString(text); i++ {
		text = clientMessageFeminineVerbPattern.ReplaceAllStringFunc(text, func(m string) string {
			sub := clientMessageFeminineVerbPattern.FindStringSubmatch(m)
			lead, word, trail := sub[1], sub[2], sub[3]
			repl, known := clientMessageFeminineVerbs[strings.ToLower(word)]
			if !known {
				return m
			}
			if r := []rune(word); len(r) > 0 && unicode.IsUpper(r[0]) {
				rr := []rune(repl)
				rr[0] = unicode.ToUpper(rr[0])
				repl = string(rr)
			}
			return lead + repl + trail
		})
	}
	return text
}

// clientMessageStopwords are excluded from the S374-class echo check below:
// short function words common to both an operator summary and a customer
// line, so they should never count as evidence of duplicated content.
var clientMessageStopwords = map[string]bool{
	"вам": true, "вас": true, "вы": true, "ваш": true, "ваша": true, "ваше": true,
	"что": true, "это": true, "как": true, "при": true, "для": true, "или": true,
	"уже": true, "если": true, "есть": true, "будет": true, "здесь": true,
	"когда": true, "туда": true, "было": true, "было ": true, "него": true,
}

var clientMessageWordSplit = regexp.MustCompile(`[^\p{L}]+`)

func significantWords(text string) []string {
	var out []string
	for _, w := range clientMessageWordSplit.Split(strings.ToLower(text), -1) {
		r := []rune(w)
		if len(r) < 5 || clientMessageStopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// clientMessageEchoesSummary catches the S374 class: the model writes
// client_message as a paraphrase or copy of the third-person, narrative
// summary meant for the human operator, so the customer reads a report about
// themselves instead of a line addressed to them. A high share of shared
// content words between the two fields is treated as evidence of that, since
// a genuine customer-facing line and an operator summary describing the same
// turn normally diverge in wording even when they cover the same facts.
func clientMessageEchoesSummary(clientMessage, summary string) bool {
	words := significantWords(clientMessage)
	if len(words) < 3 {
		return false
	}
	summarySet := make(map[string]bool, len(words))
	for _, w := range significantWords(summary) {
		summarySet[w] = true
	}
	matched := 0
	for _, w := range words {
		if summarySet[w] {
			matched++
		}
	}
	return float64(matched)/float64(len(words)) >= 0.6
}

// OperatorNotifier hands a conversation to a person: one Telegram message to
// the operator's own chat with everything the agent knows. Bot API can only
// write to a user who has opened the bot, so the operator's chat id is
// learned from that user's own conversation with the same agent; until the
// operator has written to the bot once, notifications log and skip.
type OperatorNotifier struct {
	username string
	resolve  func(ctx context.Context, agentName, username string) (string, error)
	outbound ChannelOutbound
}

var errOperatorUnknown = errors.New("operator has not written to the bot yet")

// NewOperatorNotifier reads AGENT_ESCALATION_OPERATOR (Telegram username,
// with or without @) and delivers through the same gateway /outbound path
// idle follow-ups use. Either value empty = escalation pauses the agent and
// records the reason, nobody is messaged.
func NewOperatorNotifier(pool *pgxpool.Pool, username, outboundURL string) *OperatorNotifier {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" || outboundURL == "" {
		log.Info().Bool("operator", username != "").Bool("outbound", outboundURL != "").Msg("agentruntime: operator escalation delivery disabled")
		return nil
	}
	return &OperatorNotifier{username: username, resolve: pgOperatorChat(pool), outbound: NewHTTPChannelOutbound(outboundURL)}
}

func pgOperatorChat(pool *pgxpool.Pool) func(ctx context.Context, agentName, username string) (string, error) {
	return func(ctx context.Context, agentName, username string) (string, error) {
		var chatID string
		err := pool.QueryRow(ctx, `SELECT external_id FROM conversations
WHERE agent_name=$1 AND channel='telegram' AND lower(actor_username)=lower($2)
ORDER BY updated_at DESC LIMIT 1`, agentName, username).Scan(&chatID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errOperatorUnknown
		}
		return chatID, err
	}
}

// Notify sends the card; a failure is logged and returned, never retried,
// and never undoes the pause that preceded it.
func (n *OperatorNotifier) Notify(ctx context.Context, conv Conversation, text string) error {
	if n == nil {
		return nil
	}
	chatID, err := n.resolve(ctx, conv.AgentName, n.username)
	if err != nil {
		log.Warn().Err(err).Str("agent", conv.AgentName).Str("operator", n.username).Msg("agentruntime: operator chat unresolved, escalation not delivered")
		return err
	}
	if err := n.outbound.SendOutbound(ctx, conv.AgentName, chatID, text, ""); err != nil {
		log.Warn().Err(err).Str("agent", conv.AgentName).Msg("agentruntime: operator notification failed")
		return err
	}
	return nil
}

// escalationCard is the operator-facing summary from docs/06: who, why, what
// the model wrote, then the facts and open questions the runtime holds so a
// thin model summary still leaves the operator with the state.
func escalationCard(title string, conv Conversation, reason, summary string, state RuntimeState) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString("Клиент: ")
	b.WriteString(clientLabel(conv))
	b.WriteString("\n")
	if reason != "" {
		b.WriteString("Причина: ")
		b.WriteString(reason)
		if label, ok := escalationReasons[reason]; ok {
			b.WriteString(" (" + label + ")")
		}
		b.WriteString("\n")
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		b.WriteString("\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if len(state.ReportedFacts) > 0 {
		b.WriteString("\nЧто известно:\n")
		for _, k := range sortedKeys(state.ReportedFacts) {
			b.WriteString("- " + k + ": " + state.ReportedFacts[k].Value + "\n")
		}
	}
	open := make([]string, 0, len(state.OpenLoops))
	for k, loop := range state.OpenLoops {
		if loop.Status == "open" {
			open = append(open, k+": "+loop.Question)
		}
	}
	if len(open) > 0 {
		sort.Strings(open)
		b.WriteString("\nНе выяснено:\n")
		for _, line := range open {
			b.WriteString("- " + line + "\n")
		}
	}
	b.WriteString("\nАгент на паузе, дальше пишет человек.")
	return b.String()
}

func clientLabel(conv Conversation) string {
	parts := make([]string, 0, 3)
	if conv.ActorUsername != "" {
		parts = append(parts, "@"+conv.ActorUsername)
	}
	if name, _ := conv.ActorMetadata["first_name"].(string); name != "" {
		parts = append(parts, name)
	}
	if conv.Channel == "telegram" && conv.ActorExternalID != "" {
		parts = append(parts, "tg://user?id="+conv.ActorExternalID)
	}
	if len(parts) == 0 {
		return conv.ExternalID
	}
	return strings.Join(parts, " · ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func escalationTitle(reason string) string {
	if reason == "E_DEPOSIT_HANDOFF" {
		return "🤝 Передача куратору"
	}
	return "🔺 Эскалация"
}

func (s *Server) handleEscalate(c *gin.Context) {
	var req struct {
		ContextToken  string `json:"context_token"`
		ReasonCode    string `json:"reason_code"`
		Summary       string `json:"summary"`
		ClientMessage string `json:"client_message"`
	}
	if !decodeControl(c, &req) {
		return
	}
	conv, ok := s.controlConversation(c, req.ContextToken)
	if !ok {
		return
	}
	req.ReasonCode = strings.ToUpper(strings.TrimSpace(req.ReasonCode))
	if _, known := escalationReasons[req.ReasonCode]; !known {
		rejectControl(c, "unknown_reason_code", "unknown escalation reason", "Use one of: "+strings.Join(sortedKeys(escalationReasons), ", ")+".")
		return
	}
	if strings.TrimSpace(req.Summary) == "" {
		rejectControl(c, "empty_summary", "empty escalation summary", "Write for the operator: what the customer wants, what is known, what is unclear, what you tried, how the customer feels.")
		return
	}
	req.ClientMessage = fixClientMessageGender(req.ClientMessage)
	if clientMessageEchoesSummary(req.ClientMessage, req.Summary) {
		rejectControl(c, "client_message_echoes_summary", "client_message duplicates the operator summary",
			"client_message is the line the customer reads, addressed to them directly (вы): do not repeat summary's third-person facts about them. Write 1-2 short sentences in the dialogue's own voice.")
		return
	}
	state, err := s.runtime.states.PauseAgent(c.Request.Context(), conv.ID, "escalated: "+req.ReasonCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pause rejected"})
		return
	}
	clientTold := s.tellClient(c.Request.Context(), conv, req.ClientMessage)
	notified := false
	if s.operator != nil {
		notified = s.operator.Notify(c.Request.Context(), conv, escalationCard(escalationTitle(req.ReasonCode), conv, req.ReasonCode, req.Summary, state)) == nil
	}
	s.runtime.mirrorState(c.Request.Context(), conv, state, req.Summary)
	state, err = s.syncPausedCRM(c.Request.Context(), conv)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"agent_enabled": false, "client_notified": clientTold, "operator_notified": notified, "crm_status_sync": "pending"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent_enabled": false, "client_notified": clientTold, "operator_notified": notified, "crm_status_sync": state.CRMStatusSync, "state_version": state.Version})
}

const escalationClientLine = "По этому вопросу вам напишет коллега"

// tellClient is the last line the client gets from the agent: the model's
// own reply is suppressed once the agent is paused (runtime.go re-reads the
// state after the run), so the hand-off sentence has to leave through the
// same outbound path idle follow-ups use. Text comes from the tool call so
// it matches the model's voice; empty falls back to a fixed line. The line
// is saved to history before delivery so a later operator sees it.
func (s *Server) tellClient(ctx context.Context, conv Conversation, text string) bool {
	if text = strings.TrimSpace(text); text == "" {
		text = escalationClientLine
	}
	if _, err := s.runtime.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: text}); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: escalation client line not saved")
		return false
	}
	if s.outbound == nil {
		log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: escalation client line persisted but no outbound configured")
		return false
	}
	if err := s.outbound.SendOutbound(ctx, conv.AgentName, conv.ExternalID, text, ""); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: escalation client line delivery failed")
		return false
	}
	return true
}

func stopCard(conv Conversation, reason string, state RuntimeState) string {
	return escalationCard("⏸ Клиент попросил не писать", conv, "", fmt.Sprintf("Что сказал агент в причине: %s", reason), state)
}
