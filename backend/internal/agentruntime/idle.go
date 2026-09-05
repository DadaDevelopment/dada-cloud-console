package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// idleScanIntervalDefault is the scheduler tick. Overridable via
// AGENT_RUNTIME_IDLE_TICK_SECONDS; 0 disables the scheduler entirely.
const idleScanIntervalDefault = 60 * time.Second

// idleHookRow is one conversation.idle hook joined with its due
// conversation. ConversationID identifies what to invoke; AgentName and
// ChatExternalID route the delivery; HookMessage is the harness instruction
// for the follow-up content.
type idleHookRow struct {
	HookID         string
	AgentName      string
	ConversationID string
	ChatExternalID string
	ActorUsername  string
	IdleMinutes    int
	HookMessage    string
}

// IdleScheduler runs proactive agent invocations for conversations that have
// been quiet past a lifecycle hook's idle threshold. Detection and scheduling
// are deterministic platform work (this file); the follow-up CONTENT is the
// agent's job, invoked with an explicit reason rather than a fake user turn.
//
// Fire-once semantics live in conversations.metadata.idle_fired_at: the
// scheduler claims a conversation by writing that key before invoking, so
// concurrent ticks cannot double-fire, and the next inbound user message
// clears the key (ProcessMessage), re-arming the hook for the next idle
// period.
type IdleScheduler struct {
	pool     *pgxpool.Pool
	runtime  *Runtime
	a2a      A2AClient
	outbound ChannelOutbound
	interval time.Duration
}

// ChannelOutbound delivers a finished agent reply to the channel the
// conversation lives in. tg-gateway exposes the real implementation; a nil
// outbound means "persist but do not deliver" (logs say so) -- a delivery
// failure is never data loss because the reply is already persisted.
type ChannelOutbound interface {
	SendOutbound(ctx context.Context, agentName, chatExternalID, text, replyToChannelMessageID string) error
}

func NewIdleScheduler(pool *pgxpool.Pool, runtime *Runtime, a2a A2AClient, outbound ChannelOutbound, interval time.Duration) *IdleScheduler {
	if interval <= 0 {
		interval = idleScanIntervalDefault
	}
	return &IdleScheduler{pool: pool, runtime: runtime, a2a: a2a, outbound: outbound, interval: interval}
}

// Run blocks ticking the scheduler until ctx is cancelled.
func (s *IdleScheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				log.Warn().Err(err).Msg("agentruntime: idle scheduler tick failed")
			}
		}
	}
}

// dueIdleHooks joins enabled conversation.idle hooks with conversations that
// have been quiet past the threshold and are not already claimed by a newer
// idle_fired_at mark.
const dueIdleHooksSQL = `
SELECT h.id::text, h.agent_name, c.id::text, c.external_id, c.actor_username,
       COALESCE((h.trigger_config->>'idle_minutes')::int, 30),
       COALESCE(h.action_config->>'agent_message', '')
FROM lifecycle_hooks h
JOIN conversations c
  ON c.agent_name = h.agent_name
 AND c.status = 'active'
 AND c.updated_at < NOW() - make_interval(mins => COALESCE((h.trigger_config->>'idle_minutes')::int, 30))
 AND COALESCE(c.metadata->>'idle_fired_at', '') = ''
WHERE h.trigger_event = 'conversation.idle'
  AND h.enabled = true
  AND h.action_type = 'schedule'
LIMIT 50`

// claimIdle marks the conversation as fired. The WHERE clause re-checks the
// claim so two concurrent ticks cannot both pass: exactly one UPDATE wins.
const claimIdleSQL = `
UPDATE conversations SET metadata = jsonb_set(
		COALESCE(metadata, '{}'::jsonb), '{idle_fired_at}',
		to_jsonb(to_char(NOW() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')), true),
	updated_at = NOW()
WHERE id = $1 AND COALESCE(metadata->>'idle_fired_at', '') = ''`

// Tick runs one scheduler pass: find due conversations, claim each, invoke
// the agent with the idle-reason envelope, persist the turn, deliver.
func (s *IdleScheduler) Tick(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, dueIdleHooksSQL)
	if err != nil {
		return fmt.Errorf("query due idle hooks: %w", err)
	}
	var due []idleHookRow
	for rows.Next() {
		var r idleHookRow
		if err := rows.Scan(&r.HookID, &r.AgentName, &r.ConversationID, &r.ChatExternalID, &r.ActorUsername, &r.IdleMinutes, &r.HookMessage); err != nil {
			rows.Close()
			return fmt.Errorf("scan idle hook row: %w", err)
		}
		due = append(due, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range due {
		tag, err := s.pool.Exec(ctx, claimIdleSQL, r.ConversationID)
		if err != nil {
			log.Warn().Err(err).Str("conversation", r.ConversationID).Msg("agentruntime: idle claim failed")
			continue
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		s.invoke(ctx, r)
	}
	return nil
}

// invocationEnvelope renders the outbound-run reason the agent receives.
// Owner's spec: the agent must see WHY it was invoked, not a fake user
// message.
func invocationEnvelope(r idleHookRow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[invocation: cause=conversation_idle, idle=%dm]\n", r.IdleMinutes))
	msg := r.HookMessage
	if strings.TrimSpace(msg) == "" {
		msg = "Диалог давно без ответа. Составь короткое уместное follow-up сообщение клиенту: мягко вернись к его последнему вопросу и предложи продолжить."
	}
	sb.WriteString(msg)
	return sb.String()
}

// invoke persists the system turn, calls the agent, persists the reply, and
// hands it to the channel. Errors are logged per-conversation: one broken
// follow-up must not stop the rest of the pass.
func (s *IdleScheduler) invoke(ctx context.Context, r idleHookRow) {
	convID := r.ConversationID
	conv, err := s.runtime.store.GetConversation(ctx, parseUUID(convID))
	if err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: load conversation")
		return
	}

	lock := &s.runtime.runLocks[conv.ID[0]]
	lock.Lock()
	defer lock.Unlock()
	if s.runtime.states == nil {
		log.Warn().Str("conversation", convID).Msg("agentruntime: idle state unavailable")
		return
	}
	state, err := s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || !state.AgentEnabled {
		return
	}
	token, err := issueContextToken(s.runtime.contextKey, conv, time.Now().Add(15*time.Minute))
	if err != nil {
		log.Warn().Err(err).Msg("agentruntime: idle context unavailable")
		return
	}
	var skills []string
	if catalog, ok := s.runtime.domains.(DomainCatalog); ok {
		skills, err = catalog.ListDomains(ctx, conv.AgentName)
		if err != nil {
			return
		}
	}

	envelope := invocationEnvelope(r)
	if _, err := s.runtime.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:    "system",
		Content: envelope,
	}); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: save system message")
		return
	}

	history, err := s.runtime.store.GetRecentMessages(ctx, conv.ID, 20)
	if err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: history")
		return
	}

	reply, err := s.a2a.Send(ctx, AgentRunRequest{
		AgentName: conv.AgentName, ContextID: "runtime-" + conv.ID.String(), Messages: history,
		ConversationContext: AgentConversationContext{ConversationID: conv.ID.String(),
			Channel: conv.Channel, ExternalID: conv.ExternalID, Username: conv.ActorUsername,
			State: state, AvailableSkills: skills, ContextToken: token},
	})
	if err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: a2a")
		return
	}
	state, err = s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || !state.AgentEnabled {
		return
	}
	reply = redactContextToken(reply, token)
	if _, err := s.runtime.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:    "assistant",
		Content: reply,
	}); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: save reply")
		return
	}

	if s.outbound == nil {
		log.Info().Str("conversation", convID).Msg("agentruntime: idle follow-up persisted but no outbound configured")
		return
	}
	state, err = s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || !state.AgentEnabled {
		return
	}
	if err := s.outbound.SendOutbound(ctx, r.AgentName, r.ChatExternalID, reply, ""); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle follow-up delivery failed (reply persisted)")
	}
}

func parseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}
