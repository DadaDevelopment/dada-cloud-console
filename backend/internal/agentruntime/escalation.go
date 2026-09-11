package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// escalationReasons is the closed list from call_center/docs/06 §escalation.
// The model picks one; anything else is rejected so the operator card never
// carries a made-up category.
var escalationReasons = map[string]string{
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

func (s *Server) handleEscalate(c *gin.Context) {
	var req struct {
		ContextToken string `json:"context_token"`
		ReasonCode   string `json:"reason_code"`
		Summary      string `json:"summary"`
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
	state, err := s.runtime.states.PauseAgent(c.Request.Context(), conv.ID, "escalated: "+req.ReasonCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pause rejected"})
		return
	}
	notified := false
	if s.operator != nil {
		notified = s.operator.Notify(c.Request.Context(), conv, escalationCard("🔺 Эскалация", conv, req.ReasonCode, req.Summary, state)) == nil
	}
	state, err = s.syncPausedCRM(c.Request.Context(), conv)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"agent_enabled": false, "operator_notified": notified, "crm_status_sync": "pending"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent_enabled": false, "operator_notified": notified, "crm_status_sync": state.CRMStatusSync, "state_version": state.Version})
}

func stopCard(conv Conversation, reason string, state RuntimeState) string {
	return escalationCard("⏸ Клиент попросил не писать", conv, "", fmt.Sprintf("Что сказал агент в причине: %s", reason), state)
}
